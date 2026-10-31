# The CLI

What the Go CLI (`cmd/pipeline-graph`) understands, what one run does, and how
a CI system plugs in.

Diagrams use the [legend](../architecture.md#legend); each answers the question
stated under its heading.

## Domain model

*Answers: what does the tool understand?*

```mermaid
%% view: domain
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

## One run

*Answers: what happens in one run, and what flows between stages?*

```mermaid
%% view: one-run
flowchart LR
    cfg["config.Load<br/>pipeline-graph.yaml"] -- "Config" --> fetch["fetch.All<br/>N in parallel · cache<br/>or offline from cache"]
    fetch -- "checkout + commit" --> sel{"ci: which<br/>providers?"}
    sel -- "ci.Repo" --> load["Provider.Load"]
    load -- "ci.Pipelines" --> an["analyze.Run<br/>steps → shell · makefile<br/>cross-repo ids"]
    an -- "*model.Graph" --> views["render.BuildViews<br/>overview · full · per-repo<br/>(internal/layout)"]
    an -- "overview" --> text[("mmd · dot")]
    an -- "[]warning" --> warn[("warnings.txt")]
    an -- "*model.Graph" --> json[("graph.json<br/>history/ if changed")]
    views --> files[("drawio · vsdx")]

    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    classDef output fill:#e5e7eb,stroke:#4b5563,color:#111827
    class cfg,fetch,sel,load,an,views go
    class json data
    class warn,files,text output
```

## Packages

*Answers: which Go package imports which?*

```mermaid
%% view: cli-packages
flowchart LR
    cmd["cmd/pipeline-graph"]
    config["config"]
    fetch["fetch"]
    ci["ci<br/>+ ci/teamcity · ci/github"]
    kts["kts"]
    analyze["analyze"]
    shell["shell"]
    makefile["makefile"]
    repofs["repofs"]
    render["render"]
    layout["layout"]
    model["model<br/>graph.json"]

    cmd --> config
    cmd --> fetch
    cmd --> ci
    cmd --> analyze
    cmd --> render
    cmd --> model
    ci --> kts
    ci --> model
    ci --> repofs
    kts --> model
    kts --> repofs
    analyze --> ci
    analyze --> shell
    analyze --> makefile
    analyze --> repofs
    analyze --> model
    makefile --> repofs
    render --> layout
    render --> model
    layout --> model

    classDef go fill:#dbeafe,stroke:#1d4ed8,color:#0b1f4d
    classDef data fill:#fef3c7,stroke:#b45309,color:#3a2402
    class cmd,config,fetch,ci,kts,analyze,shell,makefile,repofs,render,layout go
    class model data
```

An arrow is an import. Only `cmd/pipeline-graph` wires the stages together; `config`,
`fetch`, `shell` and `repofs` import nothing of ours, and everything ends in `model` or `repofs`.
`test/docs` fails when this picture and the real imports differ.

| Package | Responsibility |
|---|---|
| `internal/config` | Loads the YAML; folds deprecated keys into provider blocks and reports them |
| `internal/fetch` | Shallow `git clone` with the system git and BatchMode ssh, limited to http, https, ssh, git and file URLs. Each request has its own cache directory (readable name plus a hash of URL and branch), and only a directory this run created is ever deleted. Local directories are used in place |
| `internal/ci` | The provider seam ([below](#the-provider-seam)) and the neutral job model |
| `internal/kts` | Tolerant Kotlin tokenizer and parser for the TeamCity DSL |
| `internal/shell`, `internal/makefile` | Find `make`, `docker` and script calls in step text; parse Makefiles |
| `internal/repofs` | The only way repository files are read: confined to the repo root (no `..`, no symlink out), regular files only, size-capped |
| `internal/analyze` | Turns every provider's jobs into one graph; resolves references across repositories |
| `internal/model` | The graph types and `graph.json` (`SchemaVersion`) |
| `internal/layout`, `internal/render` | Layered left-to-right layout with repository lanes; every output format |

## The provider seam

*Answers: what does adding a CI system cost?*

```mermaid
%% view: provider-seam
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
[Adding a CI/CD system](../development.md#adding-a-cicd-system).
