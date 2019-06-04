#!/bin/bash
CGO_ENABLED=0 go build -v -ldflags '-extldflags "-static"'
cp /etc/pki/ca-trust/extracted/pem/tls-ca-bundle.pem .
docker stop jaeger
docker rm jaeger
docker build -t humio-jaeger-store:latest .
docker run -d --name jaeger \
  -e COLLECTOR_ZIPKIN_HTTP_PORT=9411 \
  -p 5775:5775/udp \
  -p 6831:6831/udp \
  -p 6832:6832/udp \
  -p 5778:5778 \
  -p 16686:16686 \
  -p 14268:14268 \
  -p 9411:9411 \
  -p 14250:14250 \
  humio-jaeger-store:latest
sleep 2
docker logs jaeger
