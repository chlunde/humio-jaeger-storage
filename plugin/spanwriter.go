package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/storage/spanstore"
)

// SpanWriter creates a new spanstore.Writer which can write spans to
// humio
func (h *HumioPlugin) SpanWriter() spanstore.Writer {
	if h.spanWriter == nil {
		client := h.getClient(h.WriteToken)
		h.spanWriter = &humioSpanWriter{
			plugin: h,
			ingest: &humio.BatchIngester{Client: client},
		}

		// Create a background goroutine to sync events to humio in batches
		go func() {
			for {
				time.Sleep(1 * time.Second)
				if err := h.spanWriter.ingest.Flush(context.Background()); err != nil {
					h.Logger.Error("Flush to humio failed", "err", err)
				}
			}
		}()
	}
	return h.spanWriter
}

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

func (h *humioSpanWriter) SpanToEvent(span *model.Span) humio.Event {
	t := span.GetStartTime()

	// TODO: drop this?  I think humio returns an error if the timestamp is in the future (seen in syslog)
	if t.After(time.Now()) {
		h.plugin.Logger.Error("Fixing timestamp", "ts", time.Since(t))
		t = time.Now().Local()
	}

	buf := &bytes.Buffer{}
	// TODO: Consider creating a humio event per log event inside the span
	// TODO: Consider dropping some internal tags
	// TODO: Consider mapping tags as pure JSON k: v / map[string]string
	err := json.NewEncoder(buf).Encode(span)
	if err != nil {
		h.plugin.Logger.Error("json encoding", "err", err)
		return humio.Event{}
	}
	event := humio.Event{
		Timestamp: humio.IngestTime{Time: t},
		Attributes: map[string]string{
			"payload": buf.String(),
			"traceid": span.TraceID.String(),
		},
	}

	var tags []model.KeyValue
	tags = append(tags, span.Tags...)
	tags = append(tags, span.Process.Tags...)

	for _, tag := range tags {
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

type humioSpanWriter struct {
	plugin *HumioPlugin
	ingest *humio.BatchIngester
}

func (h *humioSpanWriter) WriteSpan(span *model.Span) error {
	event := h.SpanToEvent(span)
	event.Attributes["operation"] = span.GetOperationName()
	event.Attributes["duration_ms"] = fmt.Sprintf("%d", span.GetDuration().Milliseconds())

	h.ingest.AddEvent(map[string]string{
		"service": span.GetProcess().GetServiceName(),
	}, event)
	return nil
}

// Assert that we implement the upstream interface
var _ spanstore.Writer = &humioSpanWriter{}
