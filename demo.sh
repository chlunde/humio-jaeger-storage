#!/bin/bash
if [[ ! -f conf.json ]]; then
	echo "create conf.json first"
	cat <<EOF
{
	"readToken": "...",
	"writeToken": "...",
	"repo": "sandbox",
	"humio": "https://cloud.humio.com"
}
EOF
	exit 1
fi
CGO_ENABLED=0 go build -v -ldflags '-extldflags "-static"'
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
  -v $PWD/conf.json:/etc/conf.json:ro,Z \
  humio-jaeger-store:latest
sleep 2
docker logs jaeger

echo "Open http://localhost:16686"
