include mk/common.mk

IMAGE ?= registry.acme.io/service-a
ENV ?= dev

.PHONY: build test publish deploy smoke

build: generate
	go build -o dist/service-a ./cmd/service-a
	tar czf dist/service-a.tar.gz -C dist service-a

test: build
	go test ./...

generate:
	go generate ./...

publish: build
	docker push $(IMAGE):$(VERSION)

# runs inside the base image, and drives the web Makefile: a runsIn and a calls edge
smoke: build
	$(MAKE) -C web bundle
	docker run --rm registry.acme.io/base:1.2 true

deploy:
	$(MAKE) -C deploy/$(ENV) apply
	helm upgrade --install service-a chart/ --set image=$(IMAGE)
