IMAGE ?= registry.acme.io/orders
TAG   ?= dev

.PHONY: image push

image:
	docker build -f build/Dockerfile -t $(IMAGE):$(TAG) .

push: image
	docker push $(IMAGE):$(TAG)
