FROM jaegertracing/all-in-one:1.22.0
# https://github.com/jaegertracing/jaeger/tree/master/plugin/storage/grpc#running-with-a-plugin
ENV SPAN_STORAGE_TYPE="grpc-plugin"
# https://github.com/jaegertracing/jaeger/blob/master/cmd/all-in-one/Dockerfile
CMD ["--sampling.strategies-file=/etc/jaeger/sampling_strategies.json", "--grpc-storage-plugin.binary=/go/bin/humio-jaeger-storage", "--log-level=debug"] #, "--grpc-storage-plugin.configuration-file=/path/to/my/config"]
COPY ./tls-ca-bundle.pem /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem
COPY ./tmp /tmp
COPY ./humio-jaeger-storage /go/bin
