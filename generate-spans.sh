#!/bin/bash
docker run \
	   -d \
	   --rm \
	   --name demoapp \
	   --link jaeger \
	   --env JAEGER_AGENT_HOST=jaeger \
	   --env JAEGER_AGENT_PORT=6831 \
	   -p8080-8083:8080-8083 \
	   jaegertracing/example-hotrod:latest all
sleep 5
for a in $(seq 5)
do
	curl -v "http://localhost:8080/dispatch?customer=123&nonse=0.$RANDOM"
	sleep 3
done
docker logs demoapp
docker stop demoapp
