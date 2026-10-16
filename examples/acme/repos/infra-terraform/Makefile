ENV ?= staging
TF  := terraform -chdir=envs/$(ENV)

.PHONY: init validate plan apply

init:
	$(TF) init -input=false

validate: init
	$(TF) validate
	tflint --recursive

plan: init
	$(TF) plan -out=tfplan

apply: init
	$(TF) apply -input=false tfplan
