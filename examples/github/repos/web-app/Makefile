.PHONY: image publish

image:
	docker build -t ghcr.io/globex/web:$(TAG) .
	docker push ghcr.io/globex/web:$(TAG)

publish:
	@echo "aws s3 sync dist s3://globex-cdn"
