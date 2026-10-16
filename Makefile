# pipeline-graph - single entry point. Run `make` for the menu.
#
# Building and testing use the local Go toolchain. Every quality check runs
# pre-commit inside Dockerfile.pre-commit, so the gate needs only Docker and
# make. Containers run as the calling user, so files they fix are never owned
# by root.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c

ROOT_DIR  := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))
BINARY    := pipeline-graph
GO        := go
DIST      := dist
VERSION   ?= dev
LDFLAGS   := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 darwin/arm64
CONFIG    ?= pipeline-graph.yaml

PC_IMAGE  := pipeline-graph-pre-commit:local
CACHE_DIR := $(HOME)/.cache/pipeline-graph-pre-commit
HOOKS_DIR  = $(shell git -C "$(ROOT_DIR)" rev-parse --path-format=absolute --git-path hooks)

BOLD   := \033[1m
GREEN  := \033[32m
CYAN   := \033[36m
RESET  := \033[0m

# -t only when attached to a terminal, so colors work interactively while git
# hooks and CI still get plain output.
DOCKER = mkdir -p "$(CACHE_DIR)" && \
	docker run --rm $$([ -t 1 ] && echo -t) \
	  --user "$$(id -u):$$(id -g)" -e HOME=/tmp \
	  -v "$(ROOT_DIR)":/repo \
	  -v "$(CACHE_DIR)":/cache
DOCKER_RUN = $(DOCKER) $(PC_IMAGE)

# The commit range pre-commit-pr-run checks: what a PR from HEAD would contain.
FROM_REF ?= origin/master
TO_REF   ?= HEAD

.DEFAULT_GOAL := help

##@ Build
.PHONY: build
build: ## Build the native binary
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(BINARY) ./cmd/pipeline-graph

.PHONY: build-all
build-all: ## Cross-compile linux/windows/darwin binaries into dist/
	@mkdir -p $(DIST)
	@for p in $(PLATFORMS); do \
		os=$${p%/*}; arch=$${p#*/}; ext=; \
		if [ "$$os" = windows ]; then ext=.exe; fi; \
		echo "building $(DIST)/$(BINARY)-$$os-$$arch$$ext"; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$(BINARY)-$$os-$$arch$$ext ./cmd/pipeline-graph; \
	done

##@ Run
.PHONY: run
run: build ## Generate the diagrams from $(CONFIG) into out/
	./$(BINARY) -config $(CONFIG)

.PHONY: example
example: build ## Generate diagrams for the Acme example (7 linked repos) into out/acme/
	./$(BINARY) -config examples/acme/pipeline-graph.yaml

.PHONY: docker-build
docker-build: ## Build the pipeline-graph docker image
	docker build -t $(BINARY):$(VERSION) .

.PHONY: docker-run
docker-run: docker-build ## Generate in a container, using the host ssh-agent for git
	docker run --rm \
		-v $$SSH_AUTH_SOCK:/ssh-agent -e SSH_AUTH_SOCK=/ssh-agent \
		-v $$HOME/.ssh/known_hosts:/home/pg/.ssh/known_hosts:ro \
		-v $(CURDIR):/work -w /work \
		$(BINARY):$(VERSION) -config $(CONFIG)

##@ Test
.PHONY: test
test: ## Unit + end-to-end tests (needs git)
	$(GO) test ./...

.PHONY: golden
golden: ## Regenerate golden files after an intended output change (review the diff!)
	$(GO) test ./test/integration -update

.PHONY: fmt
fmt: ## gofmt all packages
	$(GO) fmt ./...

.PHONY: vet
vet: ## go vet all packages
	$(GO) vet ./...

.PHONY: lint
lint: vet ## vet + gofmt cleanliness check (local toolchain; the full gate is pre-commit-run)
	@out=$$(gofmt -l .); \
	if [ -n "$$out" ]; then echo "gofmt needed on:"; echo "$$out"; exit 1; fi

##@ Setup
.PHONY: hooks
hooks: pre-commit-image ## Install git hooks that run pre-commit in Docker on commit and push
	@for stage in pre-commit pre-push; do \
	  printf '#!/bin/sh\n# Installed by `make hooks` - runs pre-commit in Docker.\nexec make -C "%s" --no-print-directory lint-staged STAGE=%s\n' \
	    "$(ROOT_DIR)" "$$stage" > "$(HOOKS_DIR)/$$stage"; \
	  chmod +x "$(HOOKS_DIR)/$$stage"; \
	done
	@printf "$(GREEN)hooks installed in $(HOOKS_DIR)$(RESET)\n"

.PHONY: unhook
unhook: ## Remove the git hooks
	@rm -f "$(HOOKS_DIR)/pre-commit" "$(HOOKS_DIR)/pre-push"
	@printf "$(GREEN)hooks removed$(RESET)\n"

##@ Quality
.PHONY: pre-commit-run
pre-commit-run: pre-commit-image ## Run every hook on every file, plus govulncheck and a secret scan of the tree
	@$(DOCKER_RUN) run --all-files
	@$(DOCKER_RUN) run --all-files --hook-stage manual govulncheck
	@$(DOCKER_RUN) run --all-files --hook-stage manual gitleaks-tree

# Hooks on the files changed in FROM_REF..TO_REF, and gitleaks on those commits
# - so a secret added and then removed again within the PR is still caught.
.PHONY: pre-commit-pr-run
pre-commit-pr-run: pre-commit-image ## Run the hooks on a PR's changes: FROM_REF..TO_REF
	@printf "$(BOLD)checking $(FROM_REF)..$(TO_REF)$(RESET)\n"
	@$(DOCKER_RUN) run --from-ref "$(FROM_REF)" --to-ref "$(TO_REF)"
	@$(DOCKER) --entrypoint gitleaks $(PC_IMAGE) git --redact --no-banner \
	  --log-opts="$(FROM_REF)..$(TO_REF)"

# On commit the hooks see the staged files. On push there is nothing staged, so
# every pre-push hook runs on every file: the full gate before code leaves.
STAGE ?= pre-commit
.PHONY: lint-staged
lint-staged: pre-commit-image ## Run the hooks for a git stage (what the git hooks call)
	@$(DOCKER_RUN) run --hook-stage $(STAGE) $(if $(filter pre-push,$(STAGE)),--all-files)

.PHONY: secrets
secrets: pre-commit-image ## Scan the working tree and the full git history for secrets
	@$(DOCKER_RUN) run --all-files --hook-stage manual gitleaks-tree
	@$(DOCKER_RUN) run --all-files --hook-stage manual gitleaks-history

.PHONY: autoupdate
autoupdate: pre-commit-image ## Bump the hook revisions in .pre-commit-config.yaml
	@$(DOCKER_RUN) autoupdate

##@ Docker
.PHONY: pre-commit-image
pre-commit-image: ## Build the pre-commit image (cached, fast when unchanged)
	@docker build -q -t $(PC_IMAGE) - < "$(ROOT_DIR)/Dockerfile.pre-commit" >/dev/null

.PHONY: shell
shell: pre-commit-image ## Open a shell in the pre-commit image
	@docker run --rm -it --user "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(ROOT_DIR)":/repo \
	  -v "$(CACHE_DIR)":/cache --entrypoint bash $(PC_IMAGE)

##@ Cleanup
.PHONY: clean
clean: ## Remove build artifacts and generated diagrams
	rm -f $(BINARY)
	rm -rf $(DIST) out

.PHONY: clean-all
clean-all: clean ## Also remove the pre-commit image and its cache
	@docker image rm -f $(PC_IMAGE) >/dev/null 2>&1 || true
	@rm -rf "$(CACHE_DIR)"

##@ Help
.PHONY: help
help: ## Show this menu
	@awk 'BEGIN {FS = ":.*##"; printf "\n$(BOLD)pipeline-graph$(RESET)\n"} \
	      /^[a-zA-Z_.-]+:.*?##/ { printf "  $(CYAN)%-18s$(RESET) %s\n", $$1, $$2 } \
	      /^##@/ { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5) }' $(MAKEFILE_LIST)
	@printf "\n"
