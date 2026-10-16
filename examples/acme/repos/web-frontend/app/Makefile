BUCKET ?= s3://acme-web-dev

.PHONY: deps lint bundle storybook publish

deps:
	npm ci

lint: deps
	npm run lint

bundle: lint
	npm run build

storybook: deps
	npm run build-storybook

publish:
	aws s3 sync dist/ $(BUCKET) --delete
