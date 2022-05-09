package plugin

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/chlunde/humio-jaeger-storage/humio"
	"github.com/jaegertracing/jaeger/model"
	"github.com/jaegertracing/jaeger/storage/dependencystore"
	"github.com/opentracing/opentracing-go"
)

// DependencyReader can load service dependencies from storage.
func (h *HumioPlugin) DependencyReader() dependencystore.Reader {
	if h.dependencyReader == nil {
		h.dependencyReader = &humioDependencyReader{plugin: h, client: h.getClient(h.ReadToken)}
		go func() {
			h.dependencyReader.refreshDependencies()
			time.Sleep(90 * time.Minute)
		}()

	}
	return h.dependencyReader
}

type humioDependencyReader struct {
	plugin *HumioPlugin
	client *humio.Client

	cache     []model.DependencyLink
	cacheLock sync.Mutex
}

func (h *humioDependencyReader) refreshDependencies() error {
	var results []struct {
		Child  string `json:"child"`
		Parent string `json:"parent"`
		Count  string `json:"_count"`
	}

	h.plugin.Logger.Warn("refreshDependencies")
	defer func() {
		h.plugin.Logger.Warn("refreshDependencies done")
	}()

	start := time.Now()
	delta := time.Minute * 15

	type key struct {
		child  string
		parent string
	}
	hits := make(map[key]model.DependencyLink)

	for i := 0; i < int((24*time.Hour)/delta); i++ {
		partStart := start.Add(time.Duration(i+1) * -delta)
		partEnd := start.Add(time.Duration(i) * -delta)
		h.plugin.Logger.Warn("refreshDependencies subquery", "partStart", partStart, "partEnd", partEnd)
		if err := h.client.QueryDecode(context.Background(), h.plugin.Repo, humio.Q{
			// QueryString: "parseJson(payload) | groupBy([#service,peer.service,span.kind])",
			QueryString: `parseJson(payload) | child:=process.service_name | parent_span_id := references[0].span_id
| join({parseJson(payload) | parent := process.service_name }, key=[span_id], field=[parent_span_id], include=[parent]) | groupBy([parent,child])`,
			Start: humio.AbsoluteTime(partStart),
			End:   humio.AbsoluteTime(partEnd),
		}, &results); err != nil {
			return err
		}

		for _, res := range results {
			if res.Child == res.Parent {
				continue
			}

			count, err := strconv.ParseUint(res.Count, 10, 64)
			if err != nil {
				return fmt.Errorf("unparsable _count from humio: %q (%+v)", res.Count, res)
			}

			k := key{res.Child, res.Parent}
			hit := hits[k]
			hit.Child = res.Child
			hit.Parent = res.Parent
			hit.CallCount += count
			hit.Source = "humio"
			hits[k] = hit
		}
	}

	var ret []model.DependencyLink
	for _, res := range hits {
		ret = append(ret, res)
	}

	h.cacheLock.Lock()
	defer h.cacheLock.Unlock()
	h.cache = ret
	return nil
}

func (h *humioDependencyReader) GetDependencies(ctx context.Context, endTs time.Time, lookback time.Duration) ([]model.DependencyLink, error) {
	span, ctx := opentracing.StartSpanFromContext(ctx, "GetDependencies")
	defer span.Finish()

	h.cacheLock.Lock()
	defer h.cacheLock.Unlock()

	return h.cache, nil
}

// We satisfy the dependencystore.Reader interface
var _ dependencystore.Reader = &humioDependencyReader{}
