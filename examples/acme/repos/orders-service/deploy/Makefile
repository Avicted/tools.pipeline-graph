ENV ?= dev

helm-upgrade: render
	helm upgrade --install orders ./chart -f values-$(ENV).yaml

render:
	helm template orders ./chart -f values-$(ENV).yaml > /dev/null
