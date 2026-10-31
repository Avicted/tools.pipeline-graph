# Working on pipeline-graph

These rules apply to everyone who changes this repository, people and AI agents alike.
Commands are in [docs/development.md](docs/development.md); the design is in
[docs/architecture.md](docs/architecture.md).

## Principles

1. **Everything is tested.** A change comes with the tests that prove it, in the same commit.
   Nothing is shipped on the strength of "it should work".
2. **Architecture first.** Most of the effort goes into the shape of the system: seams,
   contracts, data flow. Implementation details come second.
3. **Explore the UI/UX.** New ideas are welcome when they serve the goal: an expert opens
   their own pipelines, is never confused, and comes away with insight into problems.
4. **Git hygiene.** [Conventional commits](https://www.conventionalcommits.org/), one logical
   change per commit, each commit green on its own.
5. **The architect decides; reviewers push back.** Raise the risks for CI/CD and
   maintainability (cost, flakiness, coupling, upkeep) before building, not after.
6. **Designs are documented.** Every architecture design has a clear `.md` in `docs/` with
   diagrams (Mermaid, which GitHub renders). A new subsystem or a cross-cutting change
   updates [docs/architecture.md](docs/architecture.md).
7. **Don't trust, verify.** Claims are proven by running them: tests, byte-for-byte
   comparisons, checks made to fail on purpose. State plainly what was not run.

## Before a commit

| Change | Run |
|---|---|
| anything | `make pre-commit-run` (the gate also runs as a git hook) |
| viewer code | `make viewer-test` |
| viewer UI | `make viewer-shots`, then `make viewer-shots-check` |
| the Acme demo or the summary line | `go test ./cmd/pipeline-graph -run Readme` |

## Git

- Stage with `git add <paths>`, check `git diff --cached --name-only`, then run a plain
  `git commit`. Don't use `git commit -- <paths>`: git holds the index lock for its temporary
  index while the hook runs pre-commit in Docker, and the hook then fails.
- Releases come only from `master`, through `make new-version BUMP=<major|minor|patch>`
  (see [docs/development.md](docs/development.md#releasing)).
