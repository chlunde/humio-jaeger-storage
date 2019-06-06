package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/hashicorp/go-hclog"

	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/plugin/storage/grpc"
	"github.com/jaegertracing/jaeger/storage/dependencystore"
	"github.com/jaegertracing/jaeger/storage/spanstore"
)

type humioPlugin struct {
	logger     hclog.Logger
	humio      *humio.Client
	repo       string
	readToken  string
	writeToken string

	spanReader *humioSpanReader
	spanWriter *humioSpanWriter
}

func (h *humioPlugin) SpanReader() spanstore.Reader {
	if h.spanReader == nil {
		client := *h.humio
		client.Token = h.readToken
		h.spanReader = &humioSpanReader{plugin: h, client: &client}
	}
	return h.spanReader
}

type humioSpanReader struct {
	plugin *humioPlugin
	client *humio.Client
}

func (h *humioSpanReader) GetTrace(ctx context.Context, traceID model.TraceID) (*model.Trace, error) {
	var q = humio.Q{
		QueryString: "traceid=" + humio.EscapeFieldFilter(traceID.String()) + " | head(1000)",
		Start:       humio.RelativeTime("14 days"), // We have no idea what time this should be, so let's put our faith in bloom filters!
	}

	var result []struct {
		Timestamp int64  `json:"timestamp"`
		Payload   string `json:"payload"`
		TraceID   string `json:"traceid"`
	}
	if err := h.client.QueryDecode(ctx, "sandbox", q, &result); err != nil {
		return nil, err
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
	var results []struct {
		Service string `json:"process.service_name"`
	}

	if err := h.client.QueryDecode(ctx, "sandbox", humio.Q{
		QueryString: "payload=* | parseJson(payload) | groupBy(process.service_name)",
		Start:       humio.RelativeTime("1 day"),
	}, &results); err != nil {
		return nil, err
	}

	var ret []string
	for _, res := range results {
		ret = append(ret, res.Service)
	}
	return ret, nil
}

func (h *humioSpanReader) GetOperations(ctx context.Context, service string) ([]string, error) {
	h.plugin.logger.Warn("GetOperations called")
	return nil, errors.New("not implemented")
}

func (h *humioSpanReader) FindTraces(ctx context.Context, q *spanstore.TraceQueryParameters) ([]*model.Trace, error) {
	h.plugin.logger.Warn("FindTraces " + fmt.Sprintf("%+v", q))
	serverAggr := true
	if serverAggr {
		if len(q.Tags) != 0 {
			return h.queryServerAggregationWithTags(ctx, q)
		}
		return h.queryServerAggregation(ctx, q)
	}

	spans, err := h.queryClientAggregation(ctx, q)
	if err != nil {
		return nil, err
	}
	h.plugin.logger.Warn("FindTraces backend q complete")
	var ret []*model.Trace
	traces := make(map[model.TraceID]*model.Trace)
	for _, span := range spans {
		trace, found := traces[span.TraceID]
		if !found {
			trace = &model.Trace{}
			traces[span.TraceID] = trace
			ret = append(ret, trace)
		}

		trace.Spans = append(trace.Spans, span)
	}

	if len(q.Tags) != 0 {
		// Filter on tags (client side).  We need to check each tag in
		// the query, each tag should be matched by at least one span.
		// As soon as all spans are matched we can accept this trace

		// TODO: Alternatively, should it be "return trace iff at least one span matches all queried tags"?
		ret = nil

	traceLoop:
		for _, trace := range traces {
			foundTags := make(map[string]bool)
			for _, span := range trace.Spans {
				for _, tag := range span.Tags {
					expectedValue, isQueried := q.Tags[tag.Key]

					if isQueried && !foundTags[tag.Key] {
						tagValue, ok := TagValueString(tag)
						if ok && tagValue == expectedValue {
							foundTags[tag.Key] = true
							allFound := len(foundTags) == len(q.Tags)
							if allFound {
								ret = append(ret, trace)
								continue traceLoop
							}
						}
					}
				}
			}
		}
	}
	return ret, nil
}

func (h *humioSpanReader) FindTraceIDs(ctx context.Context, query *spanstore.TraceQueryParameters) ([]model.TraceID, error) {
	h.plugin.logger.Warn("FindTraceIDs " + fmt.Sprintf("%+v", query))
	return nil, errors.New("not implemented") // TODO: Implement
}

func (h *humioPlugin) SpanWriter() spanstore.Writer {
	if h.spanWriter == nil {
		client := *h.humio
		client.Token = h.writeToken
		h.spanWriter = &humioSpanWriter{
			plugin: h,
			ingest: &humio.BatchIngester{Client: &client},
		}
		go func() {
			for {
				time.Sleep(10 * time.Second)
				h.spanWriter.ingest.Flush(context.Background())
			}
		}()
	}
	return h.spanWriter
}

type humioSpanWriter struct {
	plugin *humioPlugin
	ingest *humio.BatchIngester
}

func (h *humioSpanWriter) WriteSpan(span *model.Span) error {
	h.ingest.AddEvent(map[string]string{
		"operation": span.GetOperationName(),
		"service":   span.GetProcess().GetServiceName(),
	}, SpanToEvent(span))
	return nil
}

func (h *humioPlugin) DependencyReader() dependencystore.Reader {
	h.logger.Warn("DependencyReader called")
	return &humioDependencyReader{}
}

type humioDependencyReader struct{}

func (h *humioDependencyReader) GetDependencies(endTs time.Time, lookback time.Duration) ([]model.DependencyLink, error) {
	return nil, nil
}

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", "", "A path to the plugin's configuration file")
	flag.Parse()

	logger := hclog.New(&hclog.LoggerOptions{
		Name:       "humio-jaeger-storage",
		Level:      hclog.Warn, // Only warning supported currently
		JSONFormat: true,
	})

	plugin := humioPlugin{logger: logger, repo: "sandbox", readToken: "***REMOVED***", writeToken: "***REMOVED***", humio: &humio.Client{}}

	grpc.Serve(&plugin)
}
