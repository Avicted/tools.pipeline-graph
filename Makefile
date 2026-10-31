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
VERSION   ?= $(shell cat "$(ROOT_DIR)/VERSION")
LDFLAGS   := -s -w -X main.version=$(VERSION)
PLATFORMS := linux/amd64 linux/arm64 windows/amd64 darwin/arm64
CONFIG    ?= pipeline-graph.yaml

PC_IMAGE  := pipeline-graph-pre-commit:local
SHOTS_IMAGE := pipeline-graph-screenshots:local
CACHE_DIR := $(HOME)/.cache/pipeline-graph-pre-commit
HOOKS_DIR  = $(shell git -C "$(ROOT_DIR)" rev-parse --path-format=absolute --git-path hooks)

BOLD   := \033[1m
GREEN  := \033[32m
CYAN   := \033[36m
RESET  := \033[0m

# -t only when attached to a terminal, so colors work interactively while git
# hooks still get plain output.
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
# These run the tool in Docker, so Docker and make are all you need. The
# container runs as you (files in out/ are yours), mounts the project at /work,
# keeps clones in the host cache dir, and gets your ssh-agent and known_hosts
# when you have them - the bundled demo needs neither.
# shortcut: /etc/passwd is mounted so ssh finds your uid; Linux hosts only,
# Docker Desktop on macOS maps ownership itself - add a uname switch if it bites.
# The viewer is served on localhost and opened via $$BROWSER (set by VS Code
# devcontainers), else xdg-open / open; only on a terminal, never from a hook or script.
# shortcut: serves out/, so it assumes out_dir is out/.
PG_CACHE := $(HOME)/.cache/pipeline-graph
PG_DOCKER = mkdir -p "$(PG_CACHE)" && \
	docker run --rm --user "$$(id -u):$$(id -g)" -e HOME="$(HOME)" \
	  -v /etc/passwd:/etc/passwd:ro -v "$(PG_CACHE):$(PG_CACHE)" \
	  $${SSH_AUTH_SOCK:+-v "$$SSH_AUTH_SOCK:/ssh-agent" -e SSH_AUTH_SOCK=/ssh-agent} \
	  $$([ -f "$(HOME)/.ssh/known_hosts" ] && echo -v "$(HOME)/.ssh/known_hosts:$(HOME)/.ssh/known_hosts:ro") \
	  -v "$(ROOT_DIR):/work" -w /work $(BINARY):$(VERSION)

# First run: start from the example, which lists the bundled demo repos.
pipeline-graph.yaml:
	cp pipeline-graph.example.yaml $@
	@printf "$(GREEN)created $@ - put your repo URLs under 'repos:' to analyse your own projects$(RESET)\n"

.PHONY: run
run: docker-build $(CONFIG) ## Generate the diagrams from $(CONFIG) into out/ (first run: demo)
	@$(PG_DOCKER) -config $(CONFIG)
	@$(MAKE) --no-print-directory serve

# file:// links open as text in VS Code, so the viewer is served over http
# instead, by the tool itself (-serve): no extra image needed.
PORT ?= 8080
# Inside a devcontainer, localhost is the devcontainer, not the host Docker
# daemon's host: share its network so VS Code can forward the port to your browser.
SERVE_NET = $$(if [ -f /.dockerenv ] && docker inspect "$$HOSTNAME" >/dev/null 2>&1; then echo --network "container:$$HOSTNAME"; else echo -p "$(PORT):$(PORT)"; fi)
.PHONY: serve
serve: docker-build ## Serve out/ at http://localhost:$(PORT) in the background and open it (stop with make stop)
	@docker rm -f $(BINARY)-viewer >/dev/null 2>&1 || true
	@docker run -d --rm --name $(BINARY)-viewer $(SERVE_NET) -v "$(ROOT_DIR)/out:/www:ro" \
	  $(BINARY):$(VERSION) -serve :$(PORT) -out /www >/dev/null
	@url="http://localhost:$(PORT)/pipelines.html"; printf "$(GREEN)$$url$(RESET)  (make stop to stop serving)\n"; \
	  if [ -t 1 ]; then ( "$${BROWSER:-$$(command -v xdg-open || command -v open || echo true)}" "$$url" >/dev/null 2>&1 || true ); fi

.PHONY: stop
stop: ## Stop the viewer server
	@docker rm -f $(BINARY)-viewer >/dev/null 2>&1 || true

.PHONY: example
example: docker-build ## Generate diagrams for the Acme example (7 linked repos) into out/acme/
	@$(PG_DOCKER) -config examples/acme/pipeline-graph.yaml

.PHONY: example-github
example-github: docker-build ## Generate diagrams for the GitHub Actions example (4 linked repos) into out/github/
	@$(PG_DOCKER) -config examples/github/pipeline-graph.yaml

.PHONY: docker-build
docker-build: ## Build the pipeline-graph docker image
	@docker build -q --build-arg VERSION=$(VERSION) -t $(BINARY):$(VERSION) . >/dev/null

.PHONY: docker-run
docker-run: run ## Alias for run

##@ Viewer
# The native viewer in viewer/. Uses the local Rust toolchain; the
# quality gate (fmt, clippy, tests) runs in the pre-commit image like the Go code.
VIEWER_MANIFEST := $(ROOT_DIR)/viewer/Cargo.toml
GRAPH ?= out/graph.json
VIEWER_TARGET ?= $(ROOT_DIR)/viewer/target
VIEWER_BIN = $(VIEWER_TARGET)/release/pipeline-graph-viewer

.PHONY: viewer
viewer: ## Build the native viewer into viewer/target/release/pipeline-graph-viewer
	CARGO_TARGET_DIR="$(VIEWER_TARGET)" cargo build --release --locked --manifest-path $(VIEWER_MANIFEST)

.PHONY: view
view: viewer ## Open $(GRAPH) in the native viewer (make run or make example first): make view GRAPH=out/acme/graph.json
	@"$(VIEWER_BIN)" $(GRAPH) $(if $(COMPARE),--compare $(COMPARE))

.PHONY: viewer-test
viewer-test: ## Unit tests of the viewer
	cargo test --locked --manifest-path $(VIEWER_MANIFEST)

# A synthetic 48k-node organisation (3000 jobs, 45000 make targets, layered
# images) and the budgets the viewer has to keep on it. Fails when one is missed.
BENCH_GRAPH ?= out/bench.json
.PHONY: viewer-bench
viewer-bench: ## Generate a 48k-node graph and check load, search, lineage, layout and frame budgets
	@mkdir -p out
	cargo run --release --locked --manifest-path $(VIEWER_MANIFEST) --example gen -- $(BENCH_GRAPH)
	cargo run --release --locked --manifest-path $(VIEWER_MANIFEST) --example bench -- $(BENCH_GRAPH) --check
	cargo run --release --locked --manifest-path $(VIEWER_MANIFEST) --example frames -- $(BENCH_GRAPH) --check

# The pictures in docs/screenshots/pipeline-graph-viewer are taken by the viewer itself, so
# this needs a desktop session (it opens a window for a few seconds). SOURCE records
# the commit they show, which is what viewer-shots-check rebuilds them from.
SHOTS_DIR := docs/screenshots/pipeline-graph-viewer
.PHONY: viewer-shots
viewer-shots: viewer build ## Regenerate the viewer screenshots in docs/screenshots/pipeline-graph-viewer (needs a display)
	SOURCE_DATE_EPOCH=$$(git -C "$(ROOT_DIR)" log -1 --format=%ct) ./$(BINARY) -config examples/acme/pipeline-graph.yaml
	@mkdir -p $(SHOTS_DIR)
	"$(VIEWER_BIN)" out/acme/graph.json --compare viewer/testdata/acme-previous.json --theme light --shot $(SHOTS_DIR)
	@git -C "$(ROOT_DIR)" rev-parse HEAD > $(SHOTS_DIR)/SOURCE
	@[ -z "$$(git -C "$(ROOT_DIR)" status --porcelain -- . ':!docs/screenshots')" ] \
		|| printf "$(BOLD)viewer-shots: the tree has uncommitted changes, so these pictures match no commit$(RESET)\n"

# Rebuilds the screenshots from the commit in SOURCE, in a temporary git worktree,
# and fails if any differs from the committed one. Needs a display, like viewer-shots.
.PHONY: viewer-shots-check
viewer-shots-check: ## Check that the viewer screenshots are reproduced byte for byte from their SOURCE commit
	@src=$$(cat $(SHOTS_DIR)/SOURCE) && wt=$$(mktemp -d) && \
	trap 'git -C "$(ROOT_DIR)" worktree remove --force "$$wt"' EXIT && \
	git -C "$(ROOT_DIR)" worktree add -q --detach "$$wt" "$$src" && \
	$(MAKE) -s --no-print-directory -C "$$wt" viewer-shots VIEWER_TARGET="$(ROOT_DIR)/viewer/target/shots-check" >/dev/null && \
	bad=0 && for f in $(SHOTS_DIR)/*.png; do \
		cmp -s "$$f" "$$wt/$$f" || { echo "differs: $$f"; bad=1; }; \
	done && \
	if [ $$bad = 0 ]; then printf "$(GREEN)viewer screenshots reproduced from $${src:0:8}$(RESET)\n"; else exit 1; fi

##@ Docs
# Screenshots are taken in Docker (Dockerfile.screenshots) so that browser and
# fonts, and therefore the pixels, are the same on every machine.
SHOTS_RUN = docker build -q -t $(SHOTS_IMAGE) - < "$(ROOT_DIR)/Dockerfile.screenshots" >/dev/null && \
  docker run --rm --user "$$(id -u):$$(id -g)" -e HOME=/tmp -v "$(ROOT_DIR)":/repo -w /repo $(SHOTS_IMAGE) node scripts/screenshots.mjs

.PHONY: screenshots
screenshots: build ## Regenerate the README screenshots in docs/screenshots from the Acme example
	SOURCE_DATE_EPOCH=$$(git -C "$(ROOT_DIR)" log -1 --format=%ct) ./$(BINARY) -config examples/acme/pipeline-graph.yaml
	@$(SHOTS_RUN) out/acme/pipelines.html docs/screenshots

HTML   ?= out/acme/pipelines.html
SCENES ?= scripts/scenes.json
.PHONY: shot
shot: build ## Render scenes into out/shots: make shot [SCENE=focus,table] [SCENES=file] [HTML=file] (paths inside the repo)
	$(if $(filter out/acme/pipelines.html,$(HTML)),SOURCE_DATE_EPOCH=$$(git -C "$(ROOT_DIR)" log -1 --format=%ct) ./$(BINARY) -config examples/acme/pipeline-graph.yaml)
	@$(SHOTS_RUN) $(HTML) out/shots --scenes $(SCENES) $(if $(SCENE),--only $(SCENE))

##@ Test
.PHONY: test
test: ## Unit + end-to-end tests (needs git)
	$(GO) test ./...

.PHONY: golden
golden: ## Regenerate golden files after an intended output change (review the diff!)
	$(GO) test ./test/integration -update

.PHONY: archimate
archimate: ## Regenerate docs/architecture/model.archimate.xml from model.yaml (after changing an architecture diagram)
	$(GO) run ./tools/archimate

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

##@ Release
# VERSION is the one place the version lives: the binary's -version, the docker
# image tag and the viewer's About dialog derive from it. The viewer reads it
# from viewer/Cargo.toml, which new-version keeps equal. A release is that commit
# and tag; nothing is built or published, everyone builds from source.
BUMP ?=
CARGO_TOML := viewer/Cargo.toml
CARGO_LOCK := viewer/Cargo.lock
.PHONY: new-version
new-version: ## Bump VERSION on master, commit and tag vX.Y.Z: make new-version BUMP=<major|minor|patch>
	@case "$(BUMP)" in major|minor|patch) ;; \
		"") echo "new-version: specify the bump: make new-version BUMP=<major|minor|patch>"; exit 1;; \
		*) echo "new-version: BUMP must be major, minor or patch, not '$(BUMP)'"; exit 1;; esac
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = master ] || { echo "new-version: releases are tagged from master only"; exit 1; }
	@git diff --quiet HEAD || { echo "new-version: the tree has uncommitted changes"; exit 1; }
	@old=$$(cat VERSION); \
	major=$${old%%.*}; rest=$${old#*.}; minor=$${rest%%.*}; patch=$${rest#*.}; \
	case "$(BUMP)" in \
		major) major=$$((major + 1)); minor=0; patch=0;; \
		minor) minor=$$((minor + 1)); patch=0;; \
		patch) patch=$$((patch + 1));; \
	esac; \
	new=$$major.$$minor.$$patch; tag=v$$new; \
	if git rev-parse -q --verify "refs/tags/$$tag" >/dev/null; then echo "new-version: tag $$tag already exists"; exit 1; fi; \
	echo "$$new" > VERSION; \
	sed -i '/^\[package\]/,/^\[/s/^version = ".*"/version = "'"$$new"'"/' $(CARGO_TOML); \
	sed -i '/^name = "pipeline-graph-viewer"$$/{n;s/^version = ".*"/version = "'"$$new"'"/}' $(CARGO_LOCK); \
	grep -q '^version = "'"$$new"'"' $(CARGO_TOML) && grep -A1 '^name = "pipeline-graph-viewer"$$' $(CARGO_LOCK) | grep -q '^version = "'"$$new"'"' \
		|| { echo "new-version: could not write $$new into $(CARGO_TOML) and $(CARGO_LOCK)"; git checkout -- VERSION $(CARGO_TOML) $(CARGO_LOCK); exit 1; }; \
	git commit -q -m "chore(release): $$tag" VERSION $(CARGO_TOML) $(CARGO_LOCK) && \
	git tag -a "$$tag" -m "pipeline-graph $$new" && \
	printf "$(CYAN)$$old -> $$new, tagged $$tag.$(RESET) Push it with: git push origin HEAD $$tag\n"

.PHONY: undo-version
undo-version: ## Undo the last new-version (drops its commit and tag) while it is unpushed
	@[ "$$(git rev-parse --abbrev-ref HEAD)" = master ] || { echo "undo-version: run it on master"; exit 1; }
	@git diff --quiet HEAD || { echo "undo-version: the tree has uncommitted changes"; exit 1; }
	@tag=v$$(cat VERSION); \
	[ "$$(git rev-parse -q --verify "refs/tags/$$tag^{commit}")" = "$$(git rev-parse HEAD)" ] \
		|| { echo "undo-version: tag $$tag does not point at HEAD, so HEAD is not a release"; exit 1; }; \
	[ "$$(git log -1 --format=%s)" = "chore(release): $$tag" ] \
		|| { echo "undo-version: HEAD is not the 'chore(release): $$tag' commit"; exit 1; }; \
	if git rev-parse -q --verify '@{upstream}' >/dev/null && git merge-base --is-ancestor HEAD '@{upstream}'; then \
		echo "undo-version: $$tag is already pushed; revert it instead"; exit 1; fi; \
	if git ls-remote --exit-code --tags origin "refs/tags/$$tag" >/dev/null 2>&1; then \
		echo "undo-version: tag $$tag is on origin; delete it there first"; exit 1; fi; \
	git tag -d "$$tag" >/dev/null && git reset -q --hard HEAD~1 && \
	printf "$(CYAN)removed $$tag;$(RESET) VERSION is back to $$(cat VERSION)\n"

##@ Cleanup
.PHONY: clean
clean: stop ## Remove build artifacts and generated diagrams
	rm -f $(BINARY)
	rm -rf $(DIST) out

.PHONY: clean-all
clean-all: clean ## Also remove the pre-commit image and its cache
	@docker image rm -f $(PC_IMAGE) $(SHOTS_IMAGE) >/dev/null 2>&1 || true
	@rm -rf "$(CACHE_DIR)"

##@ Help
.PHONY: help
help: ## Show this menu
	@awk 'BEGIN {FS = ":.*##"; printf "\n$(BOLD)pipeline-graph$(RESET)\n"} \
	      /^[a-zA-Z_.-]+:.*?##/ { printf "  $(CYAN)%-18s$(RESET) %s\n", $$1, $$2 } \
	      /^##@/ { printf "\n$(BOLD)%s$(RESET)\n", substr($$0, 5) }' $(MAKEFILE_LIST)
	@printf "\n"
