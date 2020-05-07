package plugin

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/storage/dependencystore"
	"github.com/opentracing/opentracing-go"
)

// DependencyReader can load service dependencies from storage.
func (h *HumioPlugin) DependencyReader() dependencystore.Reader {
	if h.spanReader == nil {
		h.dependencyReader = &humioDependencyReader{plugin: h, client: h.getClient(h.ReadToken)}
	}
	return h.dependencyReader
}

type humioDependencyReader struct {
	plugin *HumioPlugin
	client *humio.Client
}

func (h *humioDependencyReader) GetDependencies(endTs time.Time, lookback time.Duration) ([]model.DependencyLink, error) {
	span, ctx := opentracing.StartSpanFromContext(context.TODO(), "GetDependencies")
	defer span.Finish()

	var results []struct {
		Child  string `json:"child"`
		Parent string `json:"parent"`
		Count  string `json:"_count"`
	}

	if err := h.client.QueryDecode(ctx, h.plugin.Repo, humio.Q{
		// QueryString: "parseJson(payload) | groupBy([#service,peer.service,span.kind])",
		QueryString: `parseJson(payload) | child:=process.service_name | parent_span_id := references[0].span_id
| join({parseJson(payload) | parent := process.service_name }, key=[span_id], field=[parent_span_id], include=[parent]) | groupBy([parent,child])`,
		Start: humio.RelativeTime("1 day"),
		End:   humio.AbsoluteTime(endTs),
	}, &results); err != nil {
		return nil, err
	}

	var ret []model.DependencyLink
	for _, res := range results {
		if res.Child == res.Parent {
			continue
		}

		count, err := strconv.ParseUint(res.Count, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("unparsable _count from humio: %q (%+v)", res.Count, res)
		}

		ret = append(ret, model.DependencyLink{
			Parent:    res.Parent,
			Child:     res.Child,
			CallCount: count,
			Source:    "humio",
		})
	}

	return ret, nil
}

// We satisfy the dependencystore.Reader interface
var _ dependencystore.Reader = &humioDependencyReader{}
