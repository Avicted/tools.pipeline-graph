.PHONY: lint build deploy

lint:
	golangci-lint run

build:
	go build -o bin/api ./cmd/api

deploy:
	@echo "kubectl apply -k deploy/$(ENV)"
