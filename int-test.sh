#!/bin/bash

test -f jaeger-all-in-one && test -f example-hotrod && exit 0 || :
curl --fail -SsLO https://github.com/jaegertracing/jaeger/releases/download/v1.21.0/jaeger-1.21.0-linux-amd64.tar.gz
tar -xvzf jaeger-1.21.0-linux-amd64.tar.gz
mv jaeger-1.21.0-linux-amd64/jaeger-all-in-one .
mv jaeger-1.21.0-linux-amd64/example-hotrod .


