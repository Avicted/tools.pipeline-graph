# Architecture

pipeline-graph has two programs that share one file format:

- **`pipeline-graph`** (Go, `cmd/pipeline-graph`) reads CI/CD definitions from git
  repositories and writes diagrams plus `graph.json`.
- **`pipeline-graph-viewer`** (Rust/egui, `viewer/`) is a desktop app that reads
  `graph.json` and lets you explore it.

Everything below is static analysis: no CI server is contacted and nothing from the
analysed repositories is executed.

## System context

```mermaid
flowchart LR
    repos[("git repositories<br/>TeamCity Kotlin DSL,<br/>GitHub Actions, Makefiles")]
    cli["pipeline-graph<br/>(Go CLI, local or in Docker)"]
    out[("out/<br/>drawio, vsdx, html,<br/>mmd, dot, graph.json,<br/>warnings.txt, history/")]
    miro["draw.io / Visio / Miro"]
    html["HTML viewer<br/>(also the GitHub Pages demo)"]
    viewer["pipeline-graph-viewer<br/>(Rust/egui desktop app)"]
    scripts["your scripts"]

    repos -- "shallow clone<br/>or local dir" --> cli
    cli --> out
    out --> miro
    out --> html
    out -- "graph.json" --> viewer
    out -- "graph.json" --> scripts
```

## The CLI pipeline

One run is a straight pipeline. Each stage is a package under `internal/`, and only
`cmd/pipeline-graph` knows the order.

```mermaid
flowchart LR
    cfg["config<br/>pipeline-graph.yaml"] --> fetch["fetch<br/>shallow clones,<br/>cache, -offline"]
    fetch --> detect{"each provider:<br/>Detect(repo)?"}
    detect -- yes --> load["provider.Load<br/>→ neutral ci.Jobs"]
    load --> analyze["analyze<br/>steps → shell + makefile scan,<br/>cross-repo ids, external_ids"]
    analyze --> mgraph[("model.Graph")]
    mgraph --> render["render<br/>drawio · vsdx · html ·<br/>mermaid · dot · json"]
    analyze -- "anything unresolved" --> warn["warnings.txt"]
    mgraph -- "only when it changed" --> hist["history/&lt;time&gt;-&lt;hash&gt;.json"]
```

| Package | Responsibility |
|---|---|
| `internal/config` | Loads the YAML, folds deprecated keys into provider blocks, reports notices |
| `internal/fetch` | Shallow `git clone` with the system git and BatchMode ssh; local directories are cloned too, so every repo has a commit |
| `internal/ci` | The provider seam (below) and the neutral job model |
| `internal/kts` | Tolerant Kotlin tokenizer and parser for the TeamCity DSL |
| `internal/shell`, `internal/makefile` | Find `make`, `docker` and script calls in step text; parse Makefiles |
| `internal/analyze` | Turns every provider's jobs into one graph; resolves references across repositories |
| `internal/model` | The graph types and `graph.json` (`SchemaVersion`) |
| `internal/layout`, `internal/render` | Layered left-to-right layout with repository lanes; every output format |

## The provider seam

Everything that is the same for every CI system lives on the shared side. A provider
only reads one system's files and names its concepts.

```mermaid
classDiagram
    class Provider {
        <<interface>>
        System() System
        Detect(Repo) bool
        Load(Repo) Pipelines
    }
    class Configurable {
        <<interface>>
        NewSettings() any
    }
    class teamcity {
        .teamcity Kotlin DSL
        settings: dir, project_id
    }
    class github {
        .github/workflows YAML
    }
    class kts {
        tokenizer + tolerant parser
    }
    class analyze {
        shell and make scanning
        cross-repo resolution
        warnings
    }
    Provider <|.. teamcity
    Provider <|.. github
    Configurable <|.. teamcity
    teamcity --> kts : parses with
    analyze --> Provider : Detect, Load
```

`Load` returns neutral `ci.Job`s (key, qualified id and aliases, steps, dependencies,
triggers, artifact rules). Adding a system means one new package and one line in
`cmd/pipeline-graph/main.go`; see
[Adding a CI/CD system](development.md#adding-a-cicd-system).

## The `graph.json` contract

`graph.json` is the only interface between the Go and Rust halves. Both sides are tied
to one golden file, so neither can change it alone.

```mermaid
flowchart LR
    model["Go: internal/model<br/>SchemaVersion = 1"] -- "writes" --> golden[("test/integration/testdata/<br/>graph.json.golden")]
    integ["Go: test/integration<br/>repos served over file://"] -- "compares with" --> golden
    golden -- "reads" --> contract["Rust: viewer/tests/contract.rs"]
    contract -- "same parser as" --> load["viewer: core::graph<br/>refuses a newer schemaVersion"]
```

- A change to the model fails the Go integration test until the golden file is
  regenerated (`-update`), and that regenerated file then has to pass the viewer's
  contract test.
- The viewer refuses a file whose `schemaVersion` is newer than its own, rather than
  showing it half-understood.

## The viewer

The viewer is split so that everything except drawing can run headless: in unit
tests, in the benchmarks, and in CI.

```mermaid
flowchart TB
    subgraph core["core (headless)"]
        cgraph["graph<br/>streaming load,<br/>struct-of-arrays + CSR"]
        derive["derive<br/>stages, reach,<br/>Worth a look facts"]
        search["search<br/>flat lowercase buffers"]
        layout["layout<br/>layered, deterministic"]
        spatial["spatial<br/>grid: draw only<br/>what is on screen"]
        diff["diff<br/>compare two snapshots"]
    end
    subgraph ui["ui (egui)"]
        app["app<br/>menus, phases,<br/>raw input"]
        screens["snapshot · table · canvas ·<br/>prod · images · compare ·<br/>inspector · palette · help"]
        job["job<br/>background threads"]
        shots["shots<br/>--shot: drives the app,<br/>saves PNGs"]
        headless["headless<br/>smoke tests and<br/>frame budgets"]
    end
    cgraph --> derive --> screens
    search --> screens
    layout --> spatial --> screens
    diff --> screens
    app --> screens
    job -. "load, layout,<br/>compare off the UI thread" .-> app
    shots --> app
    headless --> screens
```

The budgets are checked on a synthetic graph with 48,000 nodes by `make viewer-bench`.
That graph loads in about 0.3 s and stays at about 30 MB.

## Verification

| What | How | Where it runs |
|---|---|---|
| Go, Rust, shell, Dockerfiles, Makefile, secrets | pre-commit in a pinned Docker image (`Dockerfile.pre-commit`) | git hooks; `lint.yml` on every PR |
| Analyzer behaviour | unit tests, the integration test with golden files | `go test` (in the gate) |
| `graph.json` contract | `viewer/tests/contract.rs` against the golden file | `cargo test` (in the gate) |
| Viewer screens | `ui::headless` smoke tests drive every screen | `cargo test` (in the gate) |
| Viewer performance | load, search, lineage, layout and frame budgets | `make viewer-bench` |
| README sample output | `TestReadmeSampleOutput` runs the shipped demo | `go test` (in the gate) |
| Viewer screenshots | rebuilt from their `SOURCE` commit, compared byte for byte | `make viewer-shots-check` (needs a display) |
| HTML viewer screenshots | headless Chromium in `Dockerfile.screenshots` | `make screenshots` |

The screenshot run is made deterministic: no live mouse or keyboard input, the window
always counts as focused, a fixed 2000×1250 output, no animation, the time comes from
the commit (`SOURCE_DATE_EPOCH`), and the load time is left out.

```mermaid
sequenceDiagram
    actor dev as developer
    participant mk as make viewer-shots-check
    participant wt as temporary git worktree
    participant v as pipeline-graph-viewer --shot
    dev->>mk: run
    mk->>mk: read docs/screenshots/pipeline-graph-viewer/SOURCE
    mk->>wt: git worktree add --detach SOURCE
    mk->>wt: make viewer-shots (own build cache)
    wt->>v: Acme graph.json + compare baseline
    v-->>wt: 11 PNGs
    mk->>mk: cmp each PNG with the committed one
    mk->>wt: git worktree remove
    mk-->>dev: "reproduced" or the files that differ (exit 1)
```

## Release

```mermaid
sequenceDiagram
    actor dev as maintainer
    participant mk as make new-version
    participant git as git / origin
    participant gh as GitHub release
    participant ci as release.yml
    dev->>mk: BUMP=major, minor or patch (on a clean master)
    mk->>mk: bump VERSION, viewer/Cargo.toml, Cargo.lock
    mk->>git: commit "chore(release): vX.Y.Z" + annotated tag
    dev->>git: git push origin HEAD vX.Y.Z
    dev->>gh: publish the release for the tag
    gh->>ci: release: published
    ci->>gh: viewer for linux-amd64, windows-amd64, macos-arm64
```

`VERSION` is the only place the version lives. `pipeline-graph -version`, the Docker
image tag and the viewer's About dialog all derive from it. `make undo-version` takes
back a release that hasn't been pushed.

## Decisions and trade-offs

| Decision | Why | Cost |
|---|---|---|
| Static analysis only | Safe on any repository, no credentials for a CI server, reproducible | Whatever is decided at run time (parameters, generated config) is invisible; it shows up in `warnings.txt` |
| One neutral job model behind `ci.Provider` | A new CI system doesn't touch analysis or rendering | The model is the common denominator; system-specific detail has to fit `ci.Job` |
| `graph.json` as the only Go ↔ Rust interface | Two languages, one contract, tested on both sides | Every shape change touches two codebases and the golden file |
| A native egui viewer next to the HTML one | Tens of thousands of nodes at interactive speed | A second UI to maintain; builds per platform |
| Screenshots taken by the apps themselves | The docs show the real UI and can be rebuilt | Needs a display (native) or a Chromium image (web) |
| The quality gate runs in Docker | Same tool versions on every machine and in CI | Docker is required; the first run pulls the image |

## Known limits

Stated plainly, so nobody has to rediscover them:

- **Viewer screenshots are byte-identical only at the same display scaling.** At a
  different scaling the map and commit → prod pictures differ in the anti-aliasing of
  curved links. The camera is identical, so the cause is in rasterization; it isn't
  found yet.
- **`make viewer-shots-check` is manual.** It needs a display, so it isn't part of the
  gate or of CI.
- **Chromium isn't version-pinned** in `Dockerfile.screenshots`, because Debian removes
  superseded builds. The web screenshots can shift after a security update.
- **There's no written policy for bumping `schemaVersion`.** That covers when to bump it,
  and whether the viewer keeps reading older versions.
- **`make new-version` and `make undo-version` have no automated test.** Their guards
  were checked by hand; the full bump, commit and tag path hasn't run end to end.
