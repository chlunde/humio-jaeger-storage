package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/storage/spanstore"
)

func TagValueString(tag model.KeyValue) (string, bool) {
	switch tag.GetVType() {
	case model.ValueType_INT64:
		return fmt.Sprintf("%d", tag.GetVInt64()), true
	case model.ValueType_STRING:
		return tag.GetVStr(), true
	case model.ValueType_BOOL:
		if tag.GetVBool() {
			return "true", true
		} else {
			return "false", true
		}
	default:
		return "", false // binary, float
	}
}

func SpanToEvent(span *model.Span) humio.Event {
	t := span.GetStartTime()

	// TODO: drop this?  I think humio returns an error if the timestamp is in the future (seen in syslog)
	if t.After(time.Now()) {
		log.Printf("Fixing timestamp: %v", time.Since(t))
		t = time.Now().Local()
	}

	buf := &bytes.Buffer{}
	// TODO: Consider creating a humio event per log event inside the span
	// TODO: Consider dropping some internal tags
	// TODO: Consider mapping tags as pure JSON k: v / map[string]string
	err := json.NewEncoder(buf).Encode(span)
	if err != nil {
		log.Printf("err: %v", err)
		return humio.Event{}
	}
	event := humio.Event{
		Timestamp: humio.IngestTime{t},
		Attributes: map[string]string{
			"payload": buf.String(),
			"traceid": span.TraceID.String(),
		},
	}

	for _, tag := range span.Tags {
		k := tag.GetKey()
		if _, reserved := event.Attributes[k]; reserved {
			// don't overwrite existing attributes.  TODO: prepend _ here and in search?
			continue
		}

		if v, ok := TagValueString(tag); ok {
			event.Attributes[k] = v
		}
	}

	return event
}

func (h *humioSpanReader) queryServerAggregationWithTags(ctx context.Context, query *spanstore.TraceQueryParameters) ([]*model.Trace, error) {
	if query.NumTraces == 0 {
		query.NumTraces = 20
	}
	var queryPrefix = &bytes.Buffer{}
	for k, v := range query.Tags {
		queryPrefix.WriteString(k)
		queryPrefix.WriteString(`="`)
		v = humio.EscapeFieldFilter(v)
		queryPrefix.WriteString(v)
		queryPrefix.WriteString(`" `)
	}

	var q = humio.Q{
		QueryString: queryPrefix.String() + "payload=* | groupby(field=traceid, function=[max(@timestamp), min(@timestamp)]) | head(" + strconv.Itoa(query.NumTraces) + ")",
		Start:       humio.AbsoluteTime(query.StartTimeMin),
		End:         humio.AbsoluteTime(query.StartTimeMax),
	}

	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
		Max       int64  `json:"_max,string"`
		Min       int64  `json:"_min,string"`
	}
	if err := h.client.QueryDecode(ctx, "sandbox", q, &result); err != nil {
		return nil, err
	}

	if len(result) == 0 {
		// no matches, short circuit or else we will query for *all* spans
		return nil, nil
	}

	// Narrow timerange for next search to the period we found matches (+ 2 min slack)
	start, end := int64(math.MaxInt64), int64(math.MinInt64)
	for _, event := range result {
		if event.Min < start {
			start = event.Min
		}
		if event.Max > end {
			end = event.Max
		}
	}

	// Add a couple of minutes, but make sure we stay within the original boundaries
	// TODO: avoid conversions?
	slack := int64(1000 * 60 * 2)
	if start-slack > query.StartTimeMin.UnixNano()/1e6 {
		start -= slack
	}
	if end+slack < query.StartTimeMax.UnixNano()/1e6 {
		end += slack
	}

	qstart := humio.AbsoluteTime(time.Unix(0, int64(time.Duration(start)*time.Millisecond)))
	qend := humio.AbsoluteTime(time.Unix(0, int64(time.Duration(end)*time.Millisecond)))

	// TODO: Collect min/max time and send next query with shorter
	// time range if that makes sense
	queryPrefix.Reset()
	queryPrefix.WriteByte('(')
	for i, event := range result {
		if i != 0 {
			queryPrefix.WriteString(" OR ")
		}
		queryPrefix.WriteString("traceid=")
		queryPrefix.WriteString(event.TraceID)
	}
	queryPrefix.WriteByte(')')

	q = humio.Q{
		QueryString: queryPrefix.String() + " | groupby(field=traceid, function=session(maxpause=2m, collect([payload], multival=true))) | head(" + strconv.Itoa(query.NumTraces) + ")",
		Start:       qstart,
		End:         qend,
	}

	result = nil
	if err := h.client.QueryDecode(ctx, "sandbox", q, &result); err != nil {
		return nil, err
	}

	ret := make([]*model.Trace, 0, len(result))
	for _, event := range result {
		var trace model.Trace
		dec := json.NewDecoder(strings.NewReader(event.Payload))
	loop:
		for {
			var span model.Span
			switch err := dec.Decode(&span); err {
			case nil:
				trace.Spans = append(trace.Spans, &span)
			case io.EOF:
				break loop
			default: // unexpected error
				return nil, err
			}
		}
		ret = append(ret, &trace)
	}

	return ret, nil
}

func (h *humioSpanReader) queryServerAggregation(ctx context.Context, query *spanstore.TraceQueryParameters) ([]*model.Trace, error) {
	if query.NumTraces == 0 {
		query.NumTraces = 20
	}
	var q = humio.Q{
		QueryString: "payload=* | groupby(field=traceid, function=session(maxpause=2m, collect([payload], multival=true))) | head(" + strconv.Itoa(query.NumTraces) + ")",
		Start:       humio.AbsoluteTime(query.StartTimeMin),
		End:         humio.AbsoluteTime(query.StartTimeMax),
	}

	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
	}
	if err := h.client.QueryDecode(ctx, "sandbox", q, &result); err != nil {
		return nil, err
	}

	ret := make([]*model.Trace, 0, len(result))
	for _, event := range result {
		var trace model.Trace
		dec := json.NewDecoder(strings.NewReader(event.Payload))
	loop:
		for {
			var span model.Span
			switch err := dec.Decode(&span); err {
			case nil:
				trace.Spans = append(trace.Spans, &span)
			case io.EOF:
				break loop
			default: // unexpected error
				return nil, err
			}
		}
		ret = append(ret, &trace)
	}

	return ret, nil
}

func (h *humioSpanReader) queryClientAggregation(ctx context.Context, query *spanstore.TraceQueryParameters) ([]*model.Span, error) {
	if query.NumTraces == 0 {
		query.NumTraces = 20
	}
	limit := query.NumTraces * 10
	if limit > 10000 {
		limit = 10000
	}
	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
	}
	if err := h.client.QueryDecode(ctx, "sandbox", humio.Q{
		QueryString: "payload=* | head(" + strconv.Itoa(limit) + ")",
		Start:       humio.AbsoluteTime(query.StartTimeMin),
		End:         humio.AbsoluteTime(query.StartTimeMax),
	}, &result); err != nil {
		return nil, err
	}

	ret := make([]*model.Span, 0, len(result))
	for _, event := range result {
		var span model.Span
		if err := json.Unmarshal([]byte(event.Payload), &span); err != nil {
			return nil, err
		}
		ret = append(ret, &span)
	}

	return ret, nil
}
