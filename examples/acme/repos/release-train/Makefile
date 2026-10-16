ENV ?= staging

.PHONY: changelog smoke

changelog:
	git-cliff --unreleased --prepend CHANGELOG.md

smoke:
	k6 run --env TARGET=https://$(ENV).acme.io smoke/checkout.js
