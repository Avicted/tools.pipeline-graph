include mk/common.mk

IMAGE ?= registry.acme.io/service-a
ENV ?= dev

.PHONY: build test publish deploy

build: generate
	go build -o dist/service-a ./cmd/service-a
	tar czf dist/service-a.tar.gz -C dist service-a

test: build
	go test ./...

generate:
	go generate ./...

publish: build
	docker push $(IMAGE):$(VERSION)

deploy:
	$(MAKE) -C deploy/$(ENV) apply
	helm upgrade --install service-a chart/ --set image=$(IMAGE)
