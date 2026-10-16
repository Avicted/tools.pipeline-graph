include mk/go.mk
include mk/docker.mk

ENV ?= dev

.PHONY: ci deploy pact-verify

ci: lint test build

deploy:
	$(MAKE) -C deploy ENV=$(ENV) helm-upgrade

pact-verify:
	pact-provider-verifier --provider orders --pact-broker-base-url https://pact.acme.io
