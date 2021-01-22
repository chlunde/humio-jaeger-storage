package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/storage/spanstore"
	"github.com/opentracing/opentracing-go"
	"github.com/opentracing/opentracing-go/ext"
)

// SpanReader creates a new spanstore.Reader, which finds and loads
// traces and other data from storage.
func (h *HumioPlugin) SpanReader() spanstore.Reader {
	if h.spanReader == nil {
		h.spanReader = &humioSpanReader{plugin: h, client: h.getClient(h.ReadToken)}
	}
	return h.spanReader
}

type humioSpanReader struct {
	plugin *HumioPlugin
	client *humio.Client

	serviceOpCache struct {
		mu          sync.RWMutex
		lastUpdated time.Time
		cache       map[string]map[string]struct{}
	}
}

func (h *humioSpanReader) GetTrace(ctx context.Context, traceID model.TraceID) (*model.Trace, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "GetTrace")
	defer span.Finish()

	var q = humio.Q{
		QueryString: "traceid=" + humio.EscapeFieldFilter(traceID.String()) + " | head(1000)",
		Start:       humio.RelativeTime("14 days"), // We have no idea what time this should be, so let's put our faith in bloom filters!
	}

	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
	}
	if err := h.client.QueryDecode(ctx, h.plugin.Repo, q, &result); err != nil {
		return nil, err
	}

	if len(result) == 0 {
		return nil, spanstore.ErrTraceNotFound
	}

	var trace model.Trace
	trace.Spans = make([]*model.Span, 0, len(result))
	for _, event := range result {
		var span model.Span
		if err := json.Unmarshal([]byte(event.Payload), &span); err != nil {
			return nil, err
		}
		trace.Spans = append(trace.Spans, &span)
	}

	return &trace, nil
}

func (h *humioSpanReader) GetServices(ctx context.Context) ([]string, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "GetServices")
	defer span.Finish()

	servicesAndOps, err := h.updateAndGetServicesAndOperations(ctx)
	if err != nil {
		return nil, err
	}

	var services []string
	for svc := range servicesAndOps {
		services = append(services, svc)
	}

	return services, nil
}

type serviceAndOperation struct {
	Service   string `json:"#service"`
	Operation string `json:"operation"`
}

func (h *humioSpanReader) getServicesAndOperations(ctx context.Context, updated time.Time) ([]serviceAndOperation, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "getServicesAndOperations")
	defer span.Finish()
	var queryStart *humio.QueryTime
	if updated.IsZero() {
		queryStart = humio.RelativeTime("1 day")
	} else {
		queryStart = humio.AbsoluteTime(updated)
	}

	var results []serviceAndOperation
	err := h.client.QueryDecode(ctx, h.plugin.Repo, humio.Q{
		QueryString: "groupBy(#service, function=groupBy(operation))",
		Start:       queryStart,
	}, &results)

	return results, err
}

func (h *humioSpanReader) updateAndGetServicesAndOperations(ctx context.Context) (map[string]map[string]struct{}, error) {
	h.serviceOpCache.mu.RLock()
	cached, updated := h.serviceOpCache.cache, h.serviceOpCache.lastUpdated
	h.serviceOpCache.mu.RUnlock()

	if time.Since(updated) < 30*time.Second {
		return cached, nil
	}

	h.serviceOpCache.mu.Lock()
	defer h.serviceOpCache.mu.Unlock()

	thisUpdate := time.Now()
	servicesAndOps, err := h.getServicesAndOperations(ctx, updated)
	if err != nil {
		return nil, err
	}

	h.serviceOpCache.lastUpdated = thisUpdate
	cache := make(map[string]map[string]struct{})
	for _, e := range servicesAndOps {
		m, exists := cache[e.Service]
		if !exists {
			m = make(map[string]struct{})
			cache[e.Service] = m
		}
		m[e.Operation] = struct{}{}
	}

	for svc, ops := range cached {
		m, exists := cache[svc]
		if !exists {
			m = make(map[string]struct{})
			cache[svc] = m
		}
		for op := range ops {
			m[op] = struct{}{}
		}
	}

	h.serviceOpCache.cache = cache

	return h.serviceOpCache.cache, err
}

func (h *humioSpanReader) GetOperations(ctx context.Context, q spanstore.OperationQueryParameters) ([]spanstore.Operation, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "GetOperations")
	defer span.Finish()

	defer func() {
		if r := recover(); r != nil {
			h.plugin.Logger.Error(fmt.Sprintf("%+v", r))
			h.plugin.Logger.Error(string(debug.Stack()))
			span.LogKV("error", r)
			ext.Error.Set(span, true)
		}
	}()

	servicesAndOps, err := h.updateAndGetServicesAndOperations(ctx)
	if err != nil {
		return nil, err
	}

	var ret []spanstore.Operation
	for svc, svcops := range servicesAndOps {
		if q.ServiceName == "" || svc == q.ServiceName {
			for op := range svcops {
				ret = append(ret, spanstore.Operation{Name: op, SpanKind: "server" /* TODO*/})
			}
		}
	}

	return ret, nil
}

func (h *humioSpanReader) findTraceIDs(ctx context.Context, query *spanstore.TraceQueryParameters) ([]string, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "findTraceIDs")
	defer span.Finish()

	if query.NumTraces == 0 {
		query.NumTraces = 20
	}

	tags := make(map[string]string)
	for k, v := range query.Tags {
		tags[k] = v
	}

	if query.ServiceName != "" {
		tags[`#service`] = query.ServiceName
	}

	if query.OperationName != "" {
		tags[`operation`] = query.OperationName
	}

	// Find trace IDs of matching spans
	var queryPrefix = &bytes.Buffer{}
	for k, v := range tags {
		queryPrefix.WriteString(k)
		queryPrefix.WriteString(`="`)
		v = humio.EscapeFieldFilter(v)
		queryPrefix.WriteString(v)
		queryPrefix.WriteString(`" `)
	}

	if query.DurationMin.Nanoseconds() != 0 {
		fmt.Fprintf(queryPrefix, "duration_ms > %v ", query.DurationMin.Milliseconds())
	}

	if query.DurationMax.Nanoseconds() != 0 {
		fmt.Fprintf(queryPrefix, "duration_ms < %v ", query.DurationMax.Milliseconds())
	}

	numTraces := strconv.Itoa(query.NumTraces)

	var q = humio.Q{
		QueryString: queryPrefix.String() + `| groupBy(traceid, limit=` + numTraces + `, function=[count()])`,
		Start:       humio.AbsoluteTime(query.StartTimeMin),
		End:         humio.AbsoluteTime(query.StartTimeMax),
	}

	var result []struct {
		TraceID string `json:"traceid"`
	}

	if err := h.client.QueryDecode(ctx, h.plugin.Repo, q, &result); err != nil {
		return nil, err
	}

	ret := make([]string, 0, len(result))
	for _, event := range result {
		ret = append(ret, event.TraceID)
	}

	return ret, nil
}

func (h *humioSpanReader) FindTraces(ctx context.Context, query *spanstore.TraceQueryParameters) ([]*model.Trace, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "FindTraces")
	defer span.Finish()

	defer func() {
		if r := recover(); r != nil {
			h.plugin.Logger.Error(fmt.Sprintf("%+v", r))
			h.plugin.Logger.Error(string(debug.Stack()))
			span.LogKV("error", r)
			ext.Error.Set(span, true)
		}
	}()

	traceIDs, err := h.findTraceIDs(ctx, query)
	if err != nil {
		return nil, err
	}

	if len(traceIDs) == 0 {
		return nil, nil
	}

	var queryPrefix = &bytes.Buffer{}
	queryPrefix.WriteByte('(')
	for i, traceID := range traceIDs {
		if i != 0 {
			queryPrefix.WriteString(" OR ")
		}
		queryPrefix.WriteString("traceid=")
		queryPrefix.WriteString(traceID)
	}
	queryPrefix.WriteByte(')')

	var q = humio.Q{
		QueryString: queryPrefix.String() + `| groupBy(field=traceid, function=session(maxpause=5m, collect([payload], multival=true)))`,
		Start:       humio.AbsoluteTime(query.StartTimeMin),
		End:         humio.AbsoluteTime(query.StartTimeMax),
	}

	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
	}

	if err := h.client.QueryDecode(ctx, h.plugin.Repo, q, &result); err != nil {
		return nil, err
	}
	span.LogKV("event", "backend query complete")

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

func (h *humioSpanReader) FindTraceIDs(ctx context.Context, query *spanstore.TraceQueryParameters) ([]model.TraceID, error) {
	span, _ := opentracing.StartSpanFromContext(ctx, "FindTraceIDs")
	defer span.Finish()

	h.plugin.Logger.Warn("FindTraceIDs " + fmt.Sprintf("%+v", query))
	return nil, errors.New("not implemented") // TODO: Implement
}

// Assert that we implement the right interface
var _ spanstore.Reader = &humioSpanReader{}
