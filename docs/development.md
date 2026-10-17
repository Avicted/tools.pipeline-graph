# Development

Building and testing need Go (`make build`, `make test`). The easiest way to get
it is the VS Code devcontainer in `.devcontainer/` (Go plus the host's Docker, so
`make run` and the quality gate work inside it too): *Reopen in Container*.

## Adding a CI/CD system

Everything that is the same for every system (cloning, cross-repo id
resolution, scanning steps for `make`, `docker` and scripts, the graph and all
renderers) lives behind [`ci.Provider`](internal/ci/ci.go). A provider only reads
one system's definitions from a checkout:

```go
type Provider interface {
    Name() string                       // `provider:` in the config
    Detect(r ci.Repo) bool              // does the repo use this system?
    Load(r ci.Repo) (*ci.Pipelines, error)
}
```

`Load` returns neutral `ci.Job`s: a unique `Key` within the repo, a `QualID`
other repos refer to it by (plus `Aliases` for ids that name it too), its steps,
dependencies (`ci.Ref` to a job in the same repo or by id across repos),
triggers and artifact rules. [`internal/ci/github`](internal/ci/github/github.go)
is a compact reference implementation.

To add one: implement it in `internal/ci/<name>`, add it to `providers` in
[`cmd/pipeline-graph/main.go`](cmd/pipeline-graph/main.go), and test it. A repo
that uses several systems is read by all of them; `provider: <name>` on a repo
in the config picks exactly one. The [`analyze` tests](internal/analyze/provider_test.go)
use a stub provider as a template.

```sh
make          # menu of all targets
make test     # unit tests + end-to-end test against the fixture repos (golden files); `go test -cover ./...` for coverage
make golden   # accept an intended output change, then review the diff in test/integration/testdata
```

Output is deterministic: the same commits give the same files. Set
`SOURCE_DATE_EPOCH` to pin the "generated" timestamp.

### Screenshots

The images in `docs/screenshots` are generated, not hand-made. After a change
to the viewer (or the Acme example), run:

```sh
make screenshots
```

This builds the example and drives headless Chromium through the scenes
defined in `scripts/scenes.json`, one per PNG, and rewrites the
gallery in `docs/screenshots/README.md`. It runs in Docker
(`Dockerfile.screenshots`), so the browser, the fonts, and therefore the
pixels, are the same on every machine, and the browser clock is frozen. A
second run gives byte-identical files, so `git status` shows only what really
changed. Each scene also checks the page reached the expected state and fails
on any JavaScript error, so a broken viewer fails the run instead of producing
a bad picture. To add a screenshot, add a scene to `scripts/scenes.json`.
`make shot SCENE=focus,table` renders only some scenes into `out/shots` (not
committed); `HTML=` and `SCENES=` (paths inside the repo) point it at your own
page or scene file.
Without Docker, `node scripts/screenshots.mjs` (Node 22 or later) uses a local
`chromium`, or the browser named in `$CHROME`.

### Quality gate

All checks run in Docker (`Dockerfile.pre-commit`) with pinned tool versions,
so Docker and make are all a machine needs. Docker images are pinned by tag
and digest (`name:tag@sha256:...`).

```sh
make hooks              # once per clone: run the gate on every commit and push
make pre-commit-run     # every hook on every file, plus govulncheck and a secret scan
make pre-commit-pr-run  # hooks on FROM_REF..TO_REF (default origin/master..HEAD)
make secrets            # gitleaks over the working tree and the full history
make unhook             # remove the git hooks
```

| Stage | What runs |
|-------|-----------|
| commit (staged files) | hygiene fixers, gofmt, `go mod tidy -diff`, `go vet`, golangci-lint, `go test`, shellcheck, hadolint, Makefile parse, gitleaks on the staged diff |
| push (all files) | all of the above, plus govulncheck and gitleaks over the full history |

### Layout

```
cmd/pipeline-graph   CLI
internal/config      YAML config
internal/fetch       shallow git clones (system git, BatchMode ssh)
internal/ci          CI/CD provider interface (Provider, Job, Ref)
internal/ci/teamcity TeamCity provider: reads the Kotlin DSL through internal/kts
internal/ci/github   GitHub Actions provider: reads workflow yaml
internal/kts         Kotlin tokenizer, tolerant parser, TeamCity DSL extraction
internal/makefile    Makefile parser
internal/shell       make/docker/script invocations in shell text
internal/analyze     builds the graph from any provider's jobs; resolves cross-repo references
internal/model       graph types (nodes, edges, steps, triggers)
internal/layout      layered left-to-right layout with repo swimlanes
internal/render      draw.io, Visio, HTML viewer, Mermaid, DOT
test/fixtures/repos  sample repos covering the recognised constructs
examples/acme        larger fictional TeamCity example (`make example`)
examples/github      fictional GitHub Actions example (`make example-github`)
scripts              screenshots.mjs, scenes.json: regenerate docs/screenshots (`make screenshots`)
```
