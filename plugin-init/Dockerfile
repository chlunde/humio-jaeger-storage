FROM golang:1.14.2 as build

WORKDIR /src
COPY . /src
RUN CGO_ENABLED=0 go build -v

# Now copy it into our base image.
FROM registry.access.redhat.com/ubi8-minimal
COPY --from=build /src/humio-jaeger-storage /
CMD ["cp", "/humio-jaeger-storage", "/plugin/humio-jaeger-storage"]
