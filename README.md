# humio-jaeger-storage

This is a storage plugin for Jaeger, an OpenTelemetry implementation.  It allows using Humio for storage of spans/traces/logs, which can be queried using jaeger-ui or directly from Humio.  By using jaeger-ui, you get a nice user interface showing the spans.  By using humio, you can write advanced queries.

## Why would you do this?

* You may want to write more advanced queries to find traces, which is possible in Humio
* Operating a stateful systems is a non-trivial task, so you may want to have as few stateful systems as possible

## Deploying

The plugin is a Go binary, which you can add to the jaeger container images.  Then you must specify the path to the storage plugin as a startup parameter (`--grpc-storage-plugin.binary=/go/bin/humio-jaeger-storage`), [as show in the documentation](https://www.jaegertracing.io/docs/1.12/deployment/#storage-plugin).

With jaeger running on kubernetes, you can deploy the container provided in this repo as an init container to install the plugin. https://github.com/jaegertracing/jaeger-operator/pull/1517

You should also be able to deploy it with dedicated storage nodes as of https://github.com/jaegertracing/jaeger/issues/3835

To deploy a demo setup,
* create `conf.json` (`{ "readToken": "................", "writeToken": "........-....-....-....-............", "repo": "sandbox", "humio": "https://cloud.humio.com" }`)
* run [demo.sh](demo.sh)
* run [generate-spans.sh](generate-spans.sh)
* open [http://localhost:16686](http://localhost:16686/)

## Implementation

We implement the [StoragePlugin](https://godoc.org/github.com/jaegertracing/jaeger/plugin/storage/grpc/shared#StoragePlugin) interface, which means we must provide implementations for the following methods:

```go
type Reader interface {
    GetTrace(ctx context.Context, traceID model.TraceID) (*model.Trace, error)
    GetServices(ctx context.Context) ([]string, error)
    GetOperations(ctx context.Context, service string) ([]string, error)
    FindTraces(ctx context.Context, query *TraceQueryParameters) ([]*model.Trace, error)
    FindTraceIDs(ctx context.Context, query *TraceQueryParameters) ([]model.TraceID, error)
}

type Writer interface {
    WriteSpan(span *model.Span) error
}
```

See https://github.com/jaegertracing/jaeger/tree/master/plugin/storage/grpc

### Storage strategies

Currently everything is stored with spans as events in humio. In the field `payload`, the JSON-serialized representation of the internal Jaeger span data structure is stored.

### Query strategies

Check code for current implementation. It does it in two queries instead of a single query to avoid a slow and too-fancy humio query.

## Issues

* search for TODO
* `head()`/`tail()`/`limit=` does not stop query when fulfilled.  This is not trivial to implement for anything except simple queries.  Humio is so fast this might be a non-issue.
* No escaping of * in field queries
