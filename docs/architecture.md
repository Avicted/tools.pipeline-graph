# Architecture

pipeline-graph has two programs that share one file format:

- **`pipeline-graph`** (Go, `cmd/pipeline-graph`) reads CI/CD definitions from git
  repositories and writes diagrams plus `graph.json`.
- **`pipeline-graph-viewer`** (Rust/egui, `viewer/`) is a desktop app for exploring
  `graph.json`.

Each diagram below answers one question, stated under its heading.

**Legend**, the same in every diagram:

| Colour | Meaning |
|---|---|
| 🟥 red | untrusted input: the analysed repositories |
| 🟦 blue | the Go CLI |
| 🟨 amber | data files and the contract between the two programs |
| 🟩 green | the Rust viewer |
| ⬜ grey | generated output |
| dashed box | a trust or runtime boundary |

## 1. Context and trust boundaries

*Answers: where does untrusted input enter, and what can it touch?*

```mermaid
flowchart LR
    users(["pipeline designers · tech leads · QA · DevSecOps"])
    subgraph untrusted["untrusted"]
        repos[("analysed repositories<br/>.teamcity · .github/workflows<br/>Makefiles · Dockerfiles")]
    end
    subgraph host["your machine"]
        subgraph box["Docker container · runs as your uid"]
            cli["pipeline-graph<br/>parses only, runs nothing<br/>from the repositories"]
        end
        creds["ssh-agent socket<br/>known_hosts · /etc/passwd (ro)"]
        cache[("~/.cache/pipeline-graph<br/>shallow clones (rw)")]
        out[("project dir → /work (rw)<br/>config in · out/ written")]
        viewer["pipeline-graph-viewer"]
    end
    repos -- "git clone --depth 1" --> cli
    creds -. "auth for git only" .-> cli
    cli <--> cache
    cli -- "drawio · vsdx · html · mmd · dot · graph.json" --> out
    out -- "graph.json" --> viewer
    users --> viewer
    users -- "browser · draw.io · Miro" --> out

    classDef untrusted fill:#fde2e1,stroke:#c0392b,color:#3b0d0a,stroke-width:2px
    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    classDef rust fill:#dcfce7,stroke:#15803d,color:#0a2e16
    classDef actor fill:#ffffff,stroke:#111827,color:#111827
    class repos untrusted
    class cli go
    class creds,cache,out data
    class viewer rust
    class users actor
    style untrusted fill:none,stroke:#c0392b,stroke-dasharray:6 4
    style host fill:none,stroke:#6b7280,stroke-dasharray:6 4
    style box fill:none,stroke:#1d4ed8,stroke-dasharray:6 4
```

- Repository content is **parsed, never executed**. The only programs the CLI starts are
  `git`, in `internal/fetch`, and Graphviz `dot`, on its own `pipelines.dot`.
- The container sees only what `make run` mounts: the project directory, the clone
  cache, and, read-only, what git needs to authenticate.
- Its only network traffic is git. `-serve` is a local file server for `out/`, mounted
  read-only (`make serve`).

## 2. Domain model

*Answers: what does the tool understand?*

```mermaid
erDiagram
    BUILD }o--o{ BUILD : "snapshot · artifact · trigger"
    EXTERNAL }o--o{ BUILD : "snapshot · artifact · trigger"
    BUILD }o--o{ MAKE_TARGET : "runs"
    MAKE_TARGET }o--o{ MAKE_TARGET : "depends · calls"
    BUILD }o--o{ IMAGE : "produces · consumes · runsIn"
    MAKE_TARGET }o--o{ IMAGE : "produces · consumes · runsIn"
    IMAGE }o--o{ IMAGE : "derives"

    BUILD {
        string kind "build: a TeamCity build configuration or a GitHub Actions job"
        string system "teamcity or github"
    }
    MAKE_TARGET {
        string kind "make: a target in a Makefile that a step reaches"
    }
    IMAGE {
        string kind "image: produced by an analysed pipeline"
    }
    EXTERNAL {
        string kind "external: referenced, not in any analysed repo"
    }
```

Every relationship is many-to-many. In `graph.json` each edge has a direction, the way
work flows: from the dependency to what depends on it.

| Edge | From → to | Meaning |
|---|---|---|
| `snapshot`, `artifact` | build → build | the second needs the first (and its artifacts) |
| `trigger` | build → build | the first finishing starts the second |
| `runs` | build → make | a step runs `make <target>` |
| `depends`, `calls` | make → make | a prerequisite; a recipe running `make` again |
| `produces` | build or make → image | builds and pushes the image |
| `consumes` | image → build or make | pulls or uses the image |
| `runsIn` | image → build or make | the work runs inside a container of the image |
| `derives` | image → image | `FROM`: the second is built on the first |

Third-party images (`alpine`, `golang`, …) don't become nodes. An image node exists
only if an analysed pipeline produces it. A reference that resolves to no analysed
repository becomes an `external` stub and a warning.

## 3. One run of the CLI

*Answers: what happens in one run, and what flows between stages?*

```mermaid
flowchart LR
    cfg["config.Load<br/>pipeline-graph.yaml"] -- "Config" --> fetch["fetch.All<br/>N in parallel · cache<br/>or offline from cache"]
    fetch -- "checkout + commit" --> sel{"ci: which<br/>providers?"}
    sel -- "ci.Repo" --> load["Provider.Load"]
    load -- "ci.Pipelines" --> an["analyze.Run<br/>steps → shell · makefile<br/>cross-repo ids"]
    an -- "*model.Graph" --> views["render.BuildViews<br/>overview · full · per-repo<br/>(internal/layout)"]
    an -- "overview" --> text[("mmd · dot")]
    an -- "[]warning" --> warn[("warnings.txt")]
    an -- "*model.Graph" --> json[("graph.json<br/>history/ if changed")]
    views --> files[("drawio · vsdx · html")]

    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    classDef output fill:#e5e7eb,stroke:#4b5563,color:#111827
    class cfg,fetch,sel,load,an,views go
    class json data
    class warn,files,text output
```

| Package | Responsibility |
|---|---|
| `internal/config` | Loads the YAML; folds deprecated keys into provider blocks and reports them |
| `internal/fetch` | Shallow `git clone` with the system git and BatchMode ssh. Local directories are cloned too, so every repo has a commit |
| `internal/ci` | The provider seam (§4) and the neutral job model |
| `internal/kts` | Tolerant Kotlin tokenizer and parser for the TeamCity DSL |
| `internal/shell`, `internal/makefile` | Find `make`, `docker` and script calls in step text; parse Makefiles |
| `internal/analyze` | Turns every provider's jobs into one graph; resolves references across repositories |
| `internal/model` | The graph types and `graph.json` (`SchemaVersion`) |
| `internal/layout`, `internal/render` | Layered left-to-right layout with repository lanes; every output format |

## 4. The provider seam

*Answers: what does adding a CI system cost?*

```mermaid
classDiagram
    direction LR
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
    class Pipelines {
        Jobs []Job
        CrossDeps []CrossDep
        Warnings []string
    }
    class Job {
        Key, QualID, Aliases
        Steps, Deps, Triggers
    }
    class Ref {
        Local or Global or Alias
    }
    class teamcity {
        .teamcity Kotlin DSL via kts
        settings: dir, project_id
    }
    class github {
        .github/workflows YAML
    }
    Provider <|.. teamcity
    Provider <|.. github
    Configurable <|.. teamcity
    Provider ..> Pipelines : Load returns
    Pipelines *-- Job
    Job --> Ref : Deps and Triggers name

    style Provider fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    style Configurable fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    style Pipelines fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    style Job fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    style Ref fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    style teamcity fill:#fde2e1,stroke:#c0392b,color:#3b0d0a
    style github fill:#fde2e1,stroke:#c0392b,color:#3b0d0a
```

Blue is shared by every system. Red reads one system's files from an untrusted
checkout. A new system is one package that returns neutral `ci.Job`s, plus one line
in `cmd/pipeline-graph/main.go`. Analysis, cross-repo resolution, the viewer and
every renderer stay as they are. See
[Adding a CI/CD system](development.md#adding-a-cicd-system).

## 5. The `graph.json` contract

*Answers: how can the Go and Rust halves never drift apart?*

```mermaid
flowchart LR
    model["internal/model<br/>SchemaVersion = 1"] -- "writes" --> golden[("test/integration/testdata/<br/>graph.json.golden")]
    integ["Go integration test<br/>repos over file://"] -- "must equal<br/>(-update rewrites)" --> golden
    golden -- "must parse" --> contract["viewer/tests/contract.rs"]
    contract -- "same loader" --> loader["core::graph<br/>refuses a newer<br/>schemaVersion"]

    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    classDef rust fill:#dcfce7,stroke:#15803d,color:#0a2e16
    class model,integ go
    class golden data
    class contract,loader rust
```

The golden file is the only interface between the two languages. A model change fails
the Go test until the golden file is regenerated, and the regenerated file then has to
pass the Rust test. Both run in the quality gate. The viewer refuses a file newer than
it understands instead of showing it half-understood.

## 6. The viewer at run time

*Answers: what is shared, what is mutable, and what runs off the UI thread?*

The app moves through three phases:

```mermaid
stateDiagram-v2
    direction LR
    [*] --> Welcome
    Welcome --> Loading : open · drop a file · start with a path
    Loading --> Ready : Model loaded
    Loading --> Welcome : error, shown on the welcome page
    Ready --> Loading : open another file
    Ready --> Welcome : File → Close
    Ready --> Ready : reload · compare (background job, state kept)
```

Inside `Ready`, ownership is simple: one immutable model, one mutable workspace,
and threads that only ever produce new immutable values.

```mermaid
flowchart TB
    subgraph bg["background threads (ui::job)"]
        jl["load / reload<br/>Model::load"]
        jy["layout<br/>build_scene"]
        jc["compare<br/>core::diff"]
    end
    model[("Arc&lt;Model&gt; · immutable<br/>Graph · Derived · SearchIndex")]
    ws["Workspace · mutable, UI thread only<br/>screen · selection · lineage · table"]
    frame["frame loop<br/>App::update"]
    screens["screens<br/>snapshot · table · map · prod<br/>images · compare · inspector"]

    jl -- "new Model" --> model
    model -- "read (Arc clone)" --> jy
    model -- "read (Arc clone)" --> jc
    model -- "read" --> ws
    jy -- "Arc&lt;SceneLayout&gt;" --> ws
    jc -- "Diff" --> ws
    frame -- "each frame" --> ws -- "draws" --> screens

    classDef rust fill:#dcfce7,stroke:#15803d,color:#0a2e16
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    class jl,jy,jc,ws,frame,screens rust
    class model data
    style bg fill:none,stroke:#15803d,stroke-dasharray:6 4
```

- `viewer/src/core` (graph, derive, search, layout, spatial, diff) doesn't depend on
  egui at all. That's what lets the unit tests, the contract test, the benchmarks and
  `ui::headless` run without a window.
- A reload builds a fresh `Workspace` around the new model, then carries over the
  selection, the screen, the table filter and the comparison.
- The budgets are checked on a synthetic graph with 48,000 nodes by `make viewer-bench`.
  It loads in about 0.3 s and stays at about 30 MB.

## 7. Defence in depth

*Answers: where is a mistake stopped before it reaches master?*

There is no CI/CD. Every check runs locally, in the same pinned Docker image
(`Dockerfile.pre-commit`), on the contributor's machine and again on the maintainer's.

```mermaid
flowchart LR
    subgraph contributor["contributor's machine · make hooks"]
        commit["git commit<br/>pre-commit hook<br/>staged files"]
        push["git push<br/>pre-push hook · all files<br/>+ govulncheck + history secrets"]
    end
    subgraph maintainer["maintainer's machine"]
        review["review the branch<br/>make pre-commit-pr-run<br/>FROM_REF=master TO_REF=branch"]
        manual["when the UI or speed changed<br/>viewer-shots-check · viewer-bench"]
        merge["merge to master"]
    end
    commit --> push -- "branch on origin" --> review --> merge
    manual -.-> merge

    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef output fill:#e5e7eb,stroke:#4b5563,color:#111827
    class commit,push,review,merge go
    class manual output
    style contributor fill:none,stroke:#1d4ed8,stroke-dasharray:6 4
    style maintainer fill:none,stroke:#6b7280,stroke-dasharray:6 4
```

The review step is what catches a branch from someone who never ran `make hooks`. It
runs the commit-stage hooks on exactly the branch's changes.

| Check | Catches | Runs at |
|---|---|---|
| gofmt, go vet, golangci-lint | Go style and bugs | commit, push, review |
| govulncheck | known vulnerabilities in Go dependencies | push only |
| `go test`: unit, integration (golden), `TestReadmeSampleOutput` | analyzer behaviour, a stale README sample | commit, push, review |
| cargo fmt, clippy, `cargo test`: unit, contract, headless smoke | Rust style, the `graph.json` contract, panics on any screen | commit, push, review |
| shellcheck, hadolint, Makefile parse | scripts, Dockerfiles, the Makefile | commit, push, review |
| gitleaks: staged / full history / working tree | committed secrets | commit / push / `make secrets` |
| `make viewer-bench` | load, search, layout and frame budgets | by hand |
| `make viewer-shots-check` | screenshots that no longer match their commit | by hand (needs a display) |

## 8. Reproducible screenshots

*Answers: how do we know the pictures in the docs are the real app at a known commit?*

The run is deterministic: no live mouse or keyboard input, the window always counts as
focused, a fixed 2000×1250 output, no animation, the time comes from the commit
(`SOURCE_DATE_EPOCH`), and the load time is left out. `SOURCE` records the commit.

```mermaid
sequenceDiagram
    actor dev as developer
    participant mk as make viewer-shots-check
    participant wt as temporary worktree at SOURCE
    participant v as pipeline-graph-viewer --shot
    dev->>mk: run
    mk->>wt: git worktree add --detach $(cat SOURCE)
    mk->>wt: make viewer-shots (own build cache)
    wt->>v: Acme graph.json + compare baseline
    v-->>wt: 11 PNGs
    mk->>mk: cmp each with the committed PNG
    mk->>wt: git worktree remove
    mk-->>dev: "reproduced", or the files that differ (exit 1)
```

## 9. Release

*Answers: how is a version cut?*

```mermaid
sequenceDiagram
    actor dev as maintainer
    participant mk as make new-version
    participant local as local repo (master)
    participant origin as origin
    dev->>mk: BUMP=major, minor or patch
    mk->>mk: refuse unless on a clean master
    mk->>local: VERSION, viewer/Cargo.toml, Cargo.lock
    mk->>local: commit "chore(release): vX.Y.Z" + annotated tag
    dev->>origin: git push origin HEAD vX.Y.Z
```

`VERSION` is the only place the version lives. `pipeline-graph -version`, the Docker
image tag and the viewer's About dialog derive from it. Until the push, `make
undo-version` takes the release back. A release is the commit and its tag. Nothing is
built or published: whoever needs a version checks out the tag and runs `make build`
and `make viewer`.

## Decisions

| Context | Decision | Consequence |
|---|---|---|
| The tool reads repositories it doesn't own | Static analysis only; nothing from them is executed | Safe on any repository and needs no CI server credentials. Anything decided at run time is invisible and shows up in `warnings.txt` |
| Several CI systems, one picture | One neutral job model behind `ci.Provider` | A new system doesn't touch analysis or rendering; system-specific detail has to fit `ci.Job` |
| Two languages, Go and Rust | `graph.json` with a golden file tested on both sides | Every shape change touches both sides and the golden file, on purpose |
| Graphs with tens of thousands of nodes | A native egui viewer next to the HTML one | Interactive at that size, at the cost of a second UI and builds per platform |
| The docs must show the real UI | The apps take their own screenshots | They can be rebuilt and checked; that needs a display (native) or a Chromium image (web) |
| The same results on every machine | The quality gate runs in a pinned Docker image | Docker is required |
| An internal tool; nothing is published | No CI/CD: git hooks on every machine plus a review run before merging | Nothing hosted to maintain or pay for, and no secrets in a CI system. The gate holds only if everyone runs `make hooks` and reviews really run `pre-commit-pr-run` |

## Known limits

- **Viewer screenshots are byte-identical only at the same display scaling.** At another
  scaling the map and commit → prod pictures differ in the anti-aliasing of curved
  links. The camera is identical, so the cause is in rasterization, and it isn't found yet.
- **The gate is only as good as its habits.** Nothing enforces `make hooks`. A branch
  from someone without hooks is checked only when the maintainer reviews it, and that
  review runs the commit stage: `govulncheck` and the history secret scan run only on
  push, on machines that have the hooks.
- **`make viewer-shots-check` and `make viewer-bench` are manual**, so neither is part
  of the gate.
- **Chromium isn't version-pinned** in `Dockerfile.screenshots`, because Debian removes
  superseded builds. The web screenshots can shift after a security update.
- **`make new-version` and `make undo-version` have no automated test.** Only their
  guards have been checked.

## Open question

**The `schemaVersion` policy.** Today the viewer refuses a newer file and accepts any
older one. Still open: when a change counts as a bump, and whether the viewer promises
to read older files (and for how long) or requires the current version.
