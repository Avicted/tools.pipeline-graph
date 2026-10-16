.PHONY: lint test build generate

lint:
	golangci-lint run ./...

generate:
	sqlc generate

test: generate
	go test -race -coverprofile=reports/coverage.out ./...

build: generate
	CGO_ENABLED=0 go build -o bin/orders ./cmd/orders
