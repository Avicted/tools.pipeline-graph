VERSION ?= 0.0.0-dev

.PHONY: lint test release

lint:
	golangci-lint run ./...

test: generate
	go test -race ./...

generate:
	buf generate ../proto

release: test
	mkdir -p ../dist
	tar czf ../dist/acme-go-sdk.tar.gz --transform 's,^,acme-go-sdk-$(VERSION)/,' .
