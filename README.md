# pipeline-graph

Maps how TeamCity pipelines across many git repositories link together:
build configurations, snapshot/artifact dependencies and finish-build triggers
(also across repos), the `make` targets each step runs, and the docker images
flowing between pipelines.

It is plain static analysis: a Kotlin DSL parser, a Makefile parser and a
shell-command scanner. No TeamCity server access, no AI, and nothing from the
repositories is executed.

```
$ ./pipeline-graph -config pipeline-graph.yaml
pipeline-graph: fetching 3 repositories into ~/.cache/pipeline-graph
out/pipelines.drawio
out/pipelines.vsdx
out/pipelines.html
out/pipelines.mmd
out/pipelines.dot
out/graph.json
out/warnings.txt
pipeline-graph: 3 repos, 8 build configurations, 11 make targets, 2 images, 1 external, 30 links, 3 warnings (see out/warnings.txt)
```

![Every pipeline across seven repos on one page](docs/screenshots/overview.png)

### What it answers

| | |
|---|---|
| **DevSecOps:** *a CVE lands in a base image. What rebuilds, and what gets redeployed?* Click the image or its build: the upstream turns green, the downstream red, and `I` hides everything else. The URL points at exactly this view. | ![Blast radius of a base image change](docs/screenshots/blast-radius.png) |
| **DevSecOps:** *where are images pushed and pulled?* `/` searches names, TeamCity ids, step scripts and parameters across every view. | ![Supply chain search](docs/screenshots/search-supply-chain.png) |
| **QA:** *what does this test job actually run, what triggers it, and what waits for it?* | ![Details of a test job](docs/screenshots/qa-test-job.png) |
| **Everyone:** *walk the team through the path to prod.* `P` is a presentation mode for screen sharing. | ![Presentation mode](docs/screenshots/present.png) |

More in the [screenshot gallery](docs/screenshots/README.md), including the
make drill-down, edge tooltips and analysis warnings.

## Quick start

```sh
make build   # or: go install github.com/Avicted/tools.pipeline-graph/cmd/pipeline-graph@latest
cp pipeline-graph.example.yaml pipeline-graph.yaml   # list your repos
./pipeline-graph -config pipeline-graph.yaml

# or without a config file
./pipeline-graph git@github.com:acme/service-a.git git@github.com:acme/platform-base.git

# or in Docker, using the host ssh-agent
make docker-run
```

`make example` runs it on seven linked fictional repos in
[`examples/acme`](examples/acme) and writes `out/acme/`.

Repositories are shallow-cloned with the system `git`, so your SSH agent and
keys are used. Clones are cached in `cache_dir` and refreshed on each run. A
local directory instead of a URL is analysed as it is.

## Outputs

| File | What it's for |
|------|---------------|
| `pipelines.drawio` | The main diagram. Pages: *Overview* (pipelines and images), *Pipelines + make*, and one per repo with its cross-repo neighbours. Repos are swimlanes, flow runs left to right, and each page has a legend and the commit SHAs it was built from. |
| `pipelines.vsdx` | The same pages as Visio, for importing into Miro. |
| `pipelines.html` | Self-contained interactive viewer (no CDN). See [The HTML viewer](#the-html-viewer). |
| `pipelines.mmd` | Mermaid flowchart of the overview; renders in GitHub/GitLab markdown and Miro's Mermaid app. |
| `pipelines.dot` / `.svg` | Graphviz source of the overview. The `.svg` is written only if `dot` is installed (the Docker image has it). |
| `graph.json` | The full graph model, for scripting or diffing runs. |
| `warnings.txt` | Everything the analysis could not resolve. Nothing is dropped silently. |

### The HTML viewer

Open `pipelines.html` in any browser; it needs no server and no network.

- **Shareable URLs.** The address bar always holds the view and the selected
  node (`pipelines.html#overview/bt%3Aorders-service%2FImage`), so you can paste
  exactly what you are looking at. Browser Back/Forward step through views and
  selections. `L` copies the link.
- **Lineage.** Click a node to colour everything upstream and downstream of it.
  `I` hides everything else. The side panel lists neighbours by link type,
  including ones outside the current view, plus steps, scripts, triggers, params
  and artifact rules, each with a copy button. When the repo is on GitHub,
  GitLab, Bitbucket Cloud or Gitea, `file:line` links to the analysed commit.
- **Search everything** with `/`. It matches names, TeamCity ids, scripts and
  param names in every view. `↑`/`↓` and `Enter` jump to a node (switching views
  if needed), and `N`/`⇧N` steps through the hits on the map.
- **Canvas.** Scroll or drag to pan; pinch or `Ctrl`+scroll to zoom. `F` fits,
  `0` is 100%, and double-click zooms in. When zoomed out, cards show only
  their titles, enlarged so they stay readable.
- **Sharing.** `P` is a presentation mode for screen sharing (no chrome, bigger
  text). **SVG** downloads the current view, with its filters and highlights,
  for slides and docs. `T` switches light/dark.
- `?` lists every shortcut.

### Importing into Miro

Use **Upload → Visio (.vsdx)** with `pipelines.vsdx` and pick the pages to
import. Colours and formatted text are kept, and connectors stay glued to
their shapes. `pipelines.drawio` is the editable version for diagrams.net and
Confluence.

## What is recognised

**TeamCity Kotlin DSL** (`.teamcity/**/*.kts|*.kt`):

- `object X : BuildType({...})`, `Template`, `Project`, inline `BuildType { }`,
  `project { buildType { } }`, `val x = buildType { }`, and user subclasses
  (`object X : MyBase(arg = ...)`, `class C(..) : BuildType({...})`), with
  constructor arguments substituted.
- Helper functions that build configs or add dependencies/steps
  (`fun deploy(env: String) = BuildType {...}`, `fun Dependencies.dependsOnX(b: BuildType)`),
  inlined with their arguments.
- `id(...)`, `name`, `type = COMPOSITE`, `templates(...)` (steps, dependencies,
  triggers and params merged in), `params`, `artifactRules`.
- `dependencies { snapshot(X); artifacts(X) { artifactRules }; dependency(X) { snapshot{}; artifacts{} } }`
  and `X.dependsOn(Y)`.
- `sequential { buildType(A); parallel { ... } }` chains.
- `triggers { vcs; schedule; finishBuildTrigger { buildType = "${X.id}" } }`.
- Steps `script`, `exec`, `dockerCommand`, `gradle`, `maven`, `nodeJS`,
  `powerShell`, `python`, `kotlinScript`, and generic runners.
- String templates with constants and function/constructor arguments, and
  TeamCity `%param%` references from the build's params.

**Commands in steps** and in scripts they call (e.g. `./ci/build.sh`): `make` /
`$(MAKE)` with `-C`, `-f`, `VAR=value` and targets; `cd dir && ...`;
`docker build/buildx/push/pull/run/tag` with `--build-arg` image values and the
`FROM` lines of the Dockerfile being built.

**Makefiles**: variables (`= := ?= += !=`), command-line overrides, `include`,
rules and prerequisites, static pattern rules, `.PHONY`, `.DEFAULT_GOAL`,
recipes with automatic variables, and recursive `$(MAKE) -C dir target`.
Conditionals are not evaluated (every branch is taken) and `$(shell ...)` is
never run.

**Images**: an image becomes a node when an analysed pipeline produces it.
Every pipeline or target that builds `FROM` it, runs it or passes it as a build
arg is linked as a consumer. Third-party base images such as `alpine` are left
out.

## Cross-repo references

A string id like `AbsoluteId("ServiceA_Deploy_staging")` is matched against
`<project id>_<relative id>` of every analysed build. The project id is
`teamcity_project_id` from the config, or derived from the repo name the way
TeamCity does it (`service-a` → `ServiceA`). Without an exact match, a unique
build whose id ends in `_<relative id>` and whose prefix contains the repo name
is accepted, with a warning. Anything still unresolved goes in the
**External** lane; `external_ids` in the config pins a mapping by hand.

## Limits

Values only known when TeamCity runs the DSL stay as written, e.g. names built
from data-class fields (`"${config.name}"`) or loops over lists of configs.
Such a build is shown once and listed in `warnings.txt` with its `file:line`.
UI patches in `.teamcity/patches` (`changeBuildType`) are reported but not
applied.

## Development

```sh
make          # menu of all targets
make test     # unit tests + end-to-end test against the fixture repos (golden files)
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
defined in `scripts/screenshots.mjs`, one per PNG, and rewrites the
gallery in `docs/screenshots/README.md`. It runs in Docker
(`Dockerfile.screenshots`), so the browser, the fonts, and therefore the
pixels, are the same on every machine, and the browser clock is frozen. A
second run gives byte-identical files, so `git status` shows only what really
changed. Each scene also checks the page reached the expected state and fails
on any JavaScript error, so a broken viewer fails the run instead of producing
a bad picture. To add a screenshot, add a scene to `SCENES` in the script.
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
internal/kts         Kotlin tokenizer, tolerant parser, TeamCity DSL extraction
internal/makefile    Makefile parser
internal/shell       make/docker/script invocations in shell text
internal/analyze     builds the graph and resolves cross-repo references
internal/model       graph types (nodes, edges, steps, triggers)
internal/layout      layered left-to-right layout with repo swimlanes
internal/render      draw.io, Visio, HTML viewer, Mermaid, DOT
test/fixtures/repos  sample repos covering the recognised constructs
examples/acme        larger fictional example (`make example`)
scripts              screenshots.mjs: regenerates docs/screenshots (`make screenshots`)
```

## License

GNU General Public License v3.0. See [LICENSE](LICENSE).
