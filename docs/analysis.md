# What is analysed

## What is recognised

**GitHub Actions** (`.github/workflows/*.yml|*.yaml`): every job is a node.

- `needs:` becomes a dependency, and a `download-artifact` of what a needed job
  `upload-artifact`s makes it an artifact dependency.
- `uses: owner/repo/.github/workflows/x.yml@ref` (and `./.github/workflows/x.yml`)
  links to the reusable workflow, which runs before the calling job. The called
  workflow is represented by its last job.
- `on:` `push`, `pull_request`, `schedule` and `workflow_dispatch` are shown as
  triggers on the first jobs. `workflow_run` links to the workflow it follows.
- `run:` steps are scanned like any script (`make`, `docker`, called scripts),
  with `working-directory` and `${{ env.X }}` resolved. `docker/build-push-action`
  is read as the `docker build`/`push` it runs, and `container:` and `services:`
  images count as used.

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
