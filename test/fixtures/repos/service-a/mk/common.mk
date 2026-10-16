VERSION ?= $(shell git describe --tags)
lint:
	golangci-lint run
