ARG BASE=registry.acme.io/base/go:latest
FROM ${BASE} AS build
COPY bin/orders /usr/local/bin/orders
FROM gcr.io/distroless/static@sha256:58133991db06659feaabe0f4e97a35cebf15ef4ea08f8a4c6d2ee5f75e4aa6a0
COPY --from=build /usr/local/bin/orders /orders
ENTRYPOINT ["/orders"]
