# pipeline-graph

Maps how CI/CD pipelines across many git repositories link together.

- **CI systems:** TeamCity (Kotlin DSL) and GitHub Actions. Others plug in
  through [one interface](docs/development.md#adding-a-cicd-system).
- **What is read:** build configurations and workflow jobs, snapshot/artifact
  dependencies, finish-build triggers (also across repos), the `make` targets
  each step runs, and the docker images built, pulled and run in.
- **How:** static analysis only: a Kotlin DSL parser, a workflow YAML reader,
  a Makefile parser and a shell-command scanner. No CI server access, no AI, nothing from the
  repositories is executed.

```
$ make run
pipeline-graph: fetching 7 repositories into ~/.cache/pipeline-graph
out/pipelines.drawio
out/pipelines.vsdx
out/pipelines.html
out/pipelines.mmd
out/pipelines.dot
out/graph.json
out/history/20261030T2320Z-15fc7ecd.json
out/warnings.txt
pipeline-graph: 7 repos, 30 jobs, 38 make targets, 5 images, 1 external, 133 links, 1 warnings (see out/warnings.txt)
```

![Every pipeline across seven repos on one page](docs/screenshots/overview.png)

**[Try the live demo](https://avicted.github.io/tools.pipeline-graph/)**: the
HTML viewer for the [Acme example](examples/acme), rebuilt on every push.

### What it answers

| | |
|---|---|
| **DevSecOps:** *a CVE lands in a base image. What rebuilds, and what gets redeployed?* Click the image or its build: what it needs is ringed violet, what depends on it magenta. `B` keeps only the blast radius, `D` only the dependencies, `I` both. The URL points at exactly this view. | ![Blast radius of a base image change](docs/screenshots/blast-radius.png) |
| **DevSecOps:** *where are images pushed and pulled?* `/` searches names, TeamCity ids, step scripts and parameters across every view. | ![Supply chain search](docs/screenshots/search-supply-chain.png) |
| **Platform:** *how does a base image reach prod?* Select a job, then `Shift+click` another: only the routes between them stay, listed in order. The URL carries both ends. | ![Path between two jobs](docs/screenshots/path-finder.png) |
| **QA:** *what does this test job actually run, what triggers it, and what waits for it?* | ![Details of a test job](docs/screenshots/qa-test-job.png) |
| **Everyone:** *walk the team through the path to prod.* `P` is a presentation mode for screen sharing. | ![Presentation mode](docs/screenshots/present.png) |

More in the [screenshot gallery](docs/screenshots/README.md), including the
make drill-down, edge tooltips and analysis warnings.

## Quick start

Requires Docker and make. The native viewer additionally needs a Rust
toolchain (`cargo`).

```sh
make run                      # first run: creates pipeline-graph.yaml with a demo and renders it
$EDITOR pipeline-graph.yaml   # put your repo URLs under `repos:`
make run                      # serves and opens http://localhost:8080/pipelines.html
```

### Your repos

Everything you edit is the `repos:` list in `pipeline-graph.yaml`:

```yaml
repos:
  - git@github.com:your-org/service-a.git     # a bare URL
  - url: git@github.com:your-org/base.git     # or a mapping: branch, name, provider, ...
    branch: main
  - ./checkout                                # a local directory, analysed as it is
```

A CI system with settings of its own gets a block named after it, for every
repo or for one (`teamcity: {dir: ci/tc, project_id: Base}`).

Repos are shallow-cloned with git using your ssh-agent and `known_hosts`, and the
clones are cached in `~/.cache/pipeline-graph`. Local directories must be inside
this project directory, since only it is visible to the container.
[`pipeline-graph.example.yaml`](pipeline-graph.example.yaml) lists every option.

`make example` and `make example-github` render the larger fictional
[TeamCity](examples/acme) and [GitHub Actions](examples/github) examples.

## Outputs

| File | What it's for |
|------|---------------|
| `pipelines.drawio` | The main diagram. Pages: *Overview* (pipelines and images), *Pipelines + make*, and one per repo with its cross-repo neighbours. Repos are swimlanes, flow runs left to right, and each page has a legend and the commit SHAs it was built from. |
| `pipelines.vsdx` | The same pages as Visio, for importing into Miro. |
| `pipelines.html` | Self-contained interactive viewer (no CDN). See [The HTML viewer](docs/viewer.md). |
| `pipelines.mmd` | Mermaid flowchart of the overview; renders in GitHub/GitLab markdown and Miro's Mermaid app. |
| `pipelines.dot` / `.svg` | Graphviz source of the overview. The `.svg` is written only if `dot` is installed (the Docker image has it). |
| `graph.json` | The full graph model (`schemaVersion` 1), for scripting, diffing runs and the native viewer. A dated copy of every changed graph is kept in `history/`. |
| `warnings.txt` | Every reference or file the analysis could not resolve. |

## The native viewer

`pipeline-graph-viewer` is a Rust/egui desktop app that opens `graph.json`.
No server, browser or network. On a synthetic 48,000-node graph (3,000 jobs,
45,000 make targets) it loads in about 0.3 s and uses about 30 MB; the budgets
are checked by `make viewer-bench`.

![pipeline-graph viewer: Snapshot](docs/screenshots/pipeline-graph-viewer/snapshot.png)

| Screen (key) | Shows |
|---|---|
| Snapshot (`1`) | Counts of jobs, images, make targets, images jobs run in, longest commit → prod chain and analysis warnings; **Worth a look** (load-bearing jobs, deploys with no test upstream, undefined builds, cycles, unused images, stand-alone jobs, make targets with no rule); how work starts; a card per repository. Every number opens the table filtered to what it counts. |
| Jobs & images (`2`) | Every job, image and make target with stage, triggers, images, **Needs** (all upstream) and **Breaks** (all downstream). Sortable, filterable (`repo:`, `kind:`, `class:`, `trig:`, `is:`). |
| Map (`3`) | Repositories, one repository, all jobs, or one job's lineage. Needs in violet, breaks in pink; `Shift`+click keeps the route between two jobs. |
| Commit → prod (`4`) | Per repository, every job a commit starts and everything downstream, in dependency order. |
| Images & layers (`5`) | Base → application image tree, who builds and uses each image, which jobs run inside it. |
| Compare (`6`) | Jobs added, removed and changed (with the field) against an earlier `graph.json` from `out/history/`. |

Selecting a job opens the inspector: CI system and id, source file and line,
triggers, container, direct needs and dependents, make targets and steps.
Jobs are named in the terms of their CI system. `Ctrl`/`⌘`+`K` searches and
runs commands, and `?` shows shortcuts and a legend. The File menu opens,
closes, reloads and saves a copy of a `graph.json`; the viewer never modifies it.

![Table with the inspector](docs/screenshots/pipeline-graph-viewer/inspector.png)

```sh
make run && make view
make view GRAPH=out/acme/graph.json COMPARE=viewer/testdata/acme-previous.json
```

See [the pipeline-graph viewer](docs/pipeline-graph-viewer.md).

## Docs

- [The pipeline-graph viewer](docs/pipeline-graph-viewer.md): screens, search, comparing runs, budgets
- [The HTML viewer](docs/viewer.md): shortcuts, lineage, sharing, Miro import
- [What is analysed](docs/analysis.md): recognised constructs, cross-repo references, limits
- [Development](docs/development.md): devcontainer, adding a CI/CD system, tests, quality gate, layout

## License

GNU General Public License v3.0. See [LICENSE](LICENSE).
