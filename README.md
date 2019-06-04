# humio-jaeger-storage

This is a storage plugin for Jaeger, an OpenTelemetry implementation.  It allows using Humio for storage of spans/traces/logs, which can be queried using jaeger-ui or directly from Humio.  By using jaeger-ui, you get a nice user interface showing the spans.  By using humio, you can write advanced queries.

## Why would you do this?

* You may want to write more advanced queries to find traces, which is possible in Humio
* Operating a stateful systems is a non-trivial task, so you may want to have as few stateful systems as possible

## Deploying

TODO: Provide docker images?

The plugin is a Go binary, which you can add to the jaeger container images.  Then you must specify the path to the storage plugin as a startup parameter (`--grpc-storage-plugin.binary=/go/bin/humio-jaeger-storage`), [as show in the documentation](https://www.jaegertracing.io/docs/1.12/deployment/#storage-plugin
). To deploy a demo setup, see [demo.sh](demo.sh) and [Dockerfile](Dockerfile) for an example.

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

Currently everything is stored with spans as events in humio, with tags extracted as fields to make them queriable.  In the field `payload`, the JSON-serialized representation of the internal Jaeger span data structure is stored.

TODO: The serialized format is not best for human consuption, consider changing tags to map[string]string and consider storing the logs as one event per log entry.

### Query strategies

#### If there is no query string
##### Alt A
1. Perform a single query to group spans to traces in Humio and return them directly.  Might be over-fetching?
##### Alt B
1. Perform a time constrained single query to group spans to traces in Humio and return them directly.
2. Repeat above with new time range to fetch more data if we did not get enough hits (under-fetching)?

#### If there is a query string
1. find matching trace IDs (see Alt A and Alt B obve)
2. The perform another query on those IDs to group spans to traces in Humio and return them directly. The start/stop time is constrained to the matching range +/- X minutes

## Issues

* no docker image
* how to provide configuration
* logging does not work
* search for TODO
* `head()`/`tail()`/`limit=` does not stop query when fulfilled.  This is not trivial to implement for anything except simple queries.  Humio is so fast this might be a non-issue.
* Multiple ways to log in to get search token - no good way to renew them
* Multi-tenancy access control
* No escaping of * in field queries
