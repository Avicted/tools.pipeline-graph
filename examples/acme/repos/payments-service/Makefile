ENV ?= dev

.PHONY: rollout smoke

rollout:
	kubectl -n payments-$(ENV) set image deploy/payments app=registry.acme.io/payments:$(TAG)
	$(MAKE) smoke ENV=$(ENV)

smoke:
	curl -fsS https://payments.$(ENV).acme.io/health
