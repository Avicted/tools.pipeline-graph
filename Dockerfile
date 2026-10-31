# syntax=docker/dockerfile:1@sha256:4edf897a3ffa55b89f906fc8cc78afdb3f1834cc9c7083565e611a8a7d5fe99e
FROM golang:1.27.2-alpine3.24@sha256:f92b6ef800e499660581efdabdf25d9d817a9d124eaf900924f0504e7e27e12d AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The version lives in VERSION; make docker-build passes it, so `-version` is right in the image.
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${VERSION}" -o /out/pipeline-graph ./cmd/pipeline-graph

FROM alpine:3.24@sha256:be2656061b536c1883c4034790e21d0a09c5aaaf51968b89368a3c0371ea2c9a
# git + ssh to clone, graphviz so pipelines.svg is rendered from the DOT output
# hadolint ignore=DL3018
RUN apk add --no-cache git openssh-client graphviz font-dejavu \
 && adduser -D -u 10001 pg
COPY --from=builder /out/pipeline-graph /usr/local/bin/pipeline-graph
USER 10001
ENTRYPOINT ["/usr/local/bin/pipeline-graph"]
CMD ["-config", "pipeline-graph.yaml"]
