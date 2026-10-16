SHELL := /bin/bash
VERSION ?= dev

.PHONY: lint fetch-certs

lint:
	shellcheck ci/*.sh

fetch-certs: certs/ca.pem

certs/ca.pem:
	mkdir -p certs && curl -fsSL https://pki.acme.io/ca.pem -o $@
