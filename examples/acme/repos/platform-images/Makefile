REGISTRY ?= registry.acme.io
TAG      ?= latest

.PHONY: lint scan image-go image-node image-jre push-go push-node push-jre

lint:
	hadolint images/*/Dockerfile

image-go: lint
	docker build -t $(REGISTRY)/base/go:$(TAG) images/go

image-node: lint
	docker build -t $(REGISTRY)/base/node:$(TAG) images/node

image-jre: lint
	docker build -t $(REGISTRY)/base/jre:$(TAG) images/jre

push-go: image-go
	docker push $(REGISTRY)/base/go:$(TAG)

push-node: image-node
	docker push $(REGISTRY)/base/node:$(TAG)

push-jre: image-jre
	docker push $(REGISTRY)/base/jre:$(TAG)

scan:
	trivy image --severity HIGH,CRITICAL $(REGISTRY)/base/go:$(TAG)
