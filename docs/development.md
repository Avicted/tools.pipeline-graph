# Development

Building and testing need Go (`make build`, `make test`). The easiest way to get
it is the VS Code devcontainer in `.devcontainer/` (Go plus the host's Docker, so
`make run` and the quality gate work inside it too): *Reopen in Container*.

## Adding a CI/CD system

Everything that is the same for every system (cloning, cross-repo id
resolution, scanning steps for `make`, `docker` and scripts, the graph and all
renderers) lives behind [`ci.Provider`](../internal/ci/ci.go). A provider only reads
one system's definitions from a checkout, and says what it calls things:

```go
type Provider interface {
    System() ci.System                  // name, title, and what a job and its id are called
    Detect(r ci.Repo) bool              // does the repo use this system?
    Load(r ci.Repo) (*ci.Pipelines, error)
}

// optional: the provider has settings of its own
type Configurable interface{ NewSettings() any }
```

`Load` returns neutral `ci.Job`s: a unique `Key` within the repo, a `QualID`
other repos refer to it by (plus `Aliases` for ids that name it too), its steps,
dependencies (`ci.Ref` to a job in the same repo or by id across repos),
triggers and artifact rules. [`internal/ci/github`](../internal/ci/github/github.go)
is a compact reference implementation.

`System()` is how the system shows up everywhere else, so no shared code needs
to know it: `Name` is the `provider:` value and the key of its settings block,
`Title` is for people ("GitHub Actions"), `Job` is what one job is called
("workflow job") and `ID` what its id is called ("job path"). They end up in
graph.json (`systems`, and `system` on every job), in the analyzer's warnings
and in the viewer, which words its labels and help from them.

Settings are a `<name>:` block in the config, at the top (for every repo) and on
a repo (which overrides it). Return a struct with `yaml` tags from `NewSettings`
and it is filled from both, strictly: a typo is a config error. It arrives as
`ci.Repo.Settings`; a provider without settings never sees any.

```yaml
teamcity:
  dir: .teamcity            # for every repo
repos:
  - url: git@host:org/base.git
    teamcity: {project_id: Base}
```

To add one:

1. Implement it in `internal/ci/<name>`.
2. Add it to `providers` in [`cmd/pipeline-graph/main.go`](../cmd/pipeline-graph/main.go).
3. Test it against fixture checkouts, and describe what it reads in [`docs/analysis.md`](analysis.md).

A repo that uses several systems is read by all of them; `provider: <name>` on a
repo in the config picks exactly one. The [`analyze` tests](../internal/analyze/provider_test.go)
use a stub provider as a template.

```sh
make          # menu of all targets
make test     # unit tests + end-to-end test against the fixture repos (golden files); `go test -cover ./...` for coverage
make golden   # accept an intended output change, then review the diff in test/integration/testdata
make archimate  # after changing an architecture diagram: regenerate the ArchiMate model (docs/architecture/archimate.md)
```

Output is deterministic: the same commits give the same files. Set
`SOURCE_DATE_EPOCH` to pin the "generated" timestamp.

### Quality gate

All checks run in Docker (`Dockerfile.pre-commit`) with pinned tool versions,
so Docker and make are all a machine needs. Docker images are pinned by tag
and digest (`name:tag@sha256:...`).

```sh
make hooks              # once per clone: run the gate on every commit and push
make pre-commit-run     # every hook on every file, plus govulncheck and a secret scan
make pre-commit-pr-run  # hooks on FROM_REF..TO_REF (default origin/master..HEAD): review a branch
make secrets            # gitleaks over the working tree and the full history
make unhook             # remove the git hooks
```

| Stage | What runs |
|-------|-----------|
| commit (staged files) | hygiene fixers, gofmt, `go mod tidy -diff`, `go vet`, golangci-lint, `go test`, cargo fmt, clippy, `cargo test`, shellcheck, hadolint, Makefile parse, gitleaks on the staged diff |
| push (all files) | all of the above, plus govulncheck and gitleaks over the full history |

There is no CI/CD. The hooks are the gate, so `make hooks` is required in every
clone. Before merging a branch, the maintainer runs
`make pre-commit-pr-run FROM_REF=master TO_REF=<branch>` on it. That catches a branch
from someone who never installed the hooks.

### Layout

How these fit together, with diagrams: [architecture.md](architecture.md).

```
cmd/pipeline-graph   CLI
internal/config      YAML config
internal/fetch       shallow git clones (system git, BatchMode ssh)
internal/ci          CI/CD provider interface (Provider, Job, Ref)
internal/ci/teamcity TeamCity provider: the DSL extraction (builds, templates, triggers) on top of internal/kts
internal/ci/github   GitHub Actions provider: reads workflow yaml
internal/kts         Kotlin tokenizer and tolerant parser (a loose syntax tree)
internal/makefile    Makefile parser
internal/shell       make/docker/script invocations in shell text
internal/analyze     builds the graph from any provider's jobs; resolves cross-repo references
internal/model       graph types (nodes, edges, steps, triggers)
internal/layout      layered left-to-right layout with repo swimlanes
internal/render      draw.io, Visio, Mermaid, DOT
test/fixtures/repos  sample repos covering the recognised constructs
examples/acme        larger fictional TeamCity example (`make example`)
examples/github      fictional GitHub Actions example (`make example-github`)
test/docs            checks the package and module diagrams against the code
tools/archimate      docs/architecture/model.yaml → model.archimate.xml (`make archimate`)
```

## The native viewer

`viewer/` is a separate Rust crate (`pipeline-graph-viewer`, egui) that reads `graph.json`; see
[the viewer docs](viewer.md) for what it does. `graph.json` is the whole contract between
the two programs: when you add a node or edge kind in Go, bump
`model.SchemaVersion` if the shape changes and teach `viewer/src/core/graph.rs`
the new kind (`viewer/tests/contract.rs` fails on an edge kind it does not know).

```sh
make viewer         # build viewer/target/release/pipeline-graph-viewer
make view           # open out/graph.json
make viewer-test    # unit, contract and smoke tests (the smoke tests drive every screen headlessly)
make viewer-bench   # 48k-node graph: load, search, lineage, layout and frame budgets
make viewer-shots   # regenerate docs/screenshots/pipeline-graph-viewer; opens a window, needs a desktop
make viewer-shots-check  # rebuild them from the commit in SOURCE and compare byte for byte
```

The README's sample `make run` output is checked by `TestReadmeSampleOutput`;
when the demo changes, the test prints the line to paste.

fmt, clippy (`-D warnings`) and the tests run in the pre-commit image with the
rest of the gate. Fonts are bundled (`viewer/assets/fonts`: Fira Sans under the
SIL OFL, DejaVu Sans under its own free licence) so arrows and symbols in job
names render the same everywhere.

## Releasing

The version lives in `VERSION`; `pipeline-graph -version`, the docker image tag
and the viewer's About dialog derive from it. On a clean `master`:

```sh
make new-version BUMP=minor   # or major / patch: bumps VERSION and viewer/Cargo.toml, commits, tags vX.Y.Z
git push origin HEAD vX.Y.Z
```

A release is that commit and its tag. pipeline-graph is an internal tool, so
nothing is built or published: check out the tag and run `make build` and
`make viewer`.

`make undo-version` drops the release commit and tag again as long as neither
is pushed.
