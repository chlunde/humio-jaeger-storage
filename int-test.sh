#!/bin/bash

version=$(grep github.com/jaegertracing/jaeger go.mod | awk -F ' v' '{print $2}')

test -f jaeger-$version-linux-amd64.tar.gz || curl --fail -SsLO https://github.com/jaegertracing/jaeger/releases/download/v$version/jaeger-$version-linux-amd64.tar.gz
tar -xvzf jaeger-$version-linux-amd64.tar.gz
mv jaeger-$version-linux-amd64/jaeger-all-in-one .
mv jaeger-$version-linux-amd64/example-hotrod .


