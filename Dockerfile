FROM jaegertracing/all-in-one:1.38.1
# https://github.com/jaegertracing/jaeger/tree/master/plugin/storage/grpc#running-with-a-plugin
ENV SPAN_STORAGE_TYPE="grpc-plugin"
# https://github.com/jaegertracing/jaeger/blob/master/cmd/all-in-one/Dockerfile
CMD ["--sampling.strategies-file=/etc/jaeger/sampling_strategies.json", "--grpc-storage-plugin.binary=/go/bin/humio-jaeger-storage", "--log-level=debug", "--grpc-storage-plugin.configuration-file=/etc/conf.json"]
COPY ./humio-jaeger-storage /go/bin
