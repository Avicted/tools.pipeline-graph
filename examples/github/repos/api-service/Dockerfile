FROM ghcr.io/globex/base-go:latest AS build
WORKDIR /src
COPY . .
RUN make build

FROM alpine:3.20
COPY --from=build /src/bin/api /usr/local/bin/api
ENTRYPOINT ["api"]
