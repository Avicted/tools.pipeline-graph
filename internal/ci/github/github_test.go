package github

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
)

func checkout(t *testing.T, workflows map[string]string) ci.Repo {
	t.Helper()
	root := t.TempDir()
	for name, content := range workflows {
		p := filepath.Join(root, ".github", "workflows", name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ci.Repo{Name: "svc", Root: root}
}

func load(t *testing.T, workflows map[string]string) (*ci.Pipelines, map[string]*ci.Job) {
	t.Helper()
	p, err := New().Load(checkout(t, workflows))
	if err != nil {
		t.Fatal(err)
	}
	jobs := map[string]*ci.Job{}
	for _, j := range p.Jobs {
		jobs[j.Key] = j
	}
	return p, jobs
}

func TestDetect(t *testing.T) {
	g := New()
	if g.System().Name != "github" {
		t.Errorf("name %q", g.System().Name)
	}
	if !g.Detect(checkout(t, map[string]string{"ci.yml": "jobs: {}"})) || !g.Detect(checkout(t, map[string]string{"ci.yaml": "jobs: {}"})) {
		t.Error(".yml and .yaml workflows are detected")
	}
	if g.Detect(checkout(t, map[string]string{"notes.txt": ""})) {
		t.Error("only yaml files count")
	}
	if g.Detect(ci.Repo{Root: t.TempDir()}) {
		t.Error("no .github directory")
	}
	if _, err := g.Load(ci.Repo{Root: t.TempDir()}); err == nil {
		t.Error("loading a repo without workflows is an error")
	}
}

const ci1 = `
name: CI
on:
  push:
    branches: [main, release]
  pull_request:
  schedule:
    - cron: "0 3 * * 1"
  workflow_dispatch:
env:
  REG: ghcr.io/acme
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - name: Lint it
        run: |
          # comment first
          make lint
  build:
    name: Build it
    needs: lint
    env:
      TARGET: pkg
    steps:
      - run: make ${{ env.TARGET }}
        working-directory: svc
      - uses: actions/upload-artifact@v4
        with:
          name: bin
          path: |
            out/app
            out/app.sig
  package:
    needs: [build]
    steps:
      - uses: actions/download-artifact@v4
        with:
          name: bin
`

func TestJobsAndKeys(t *testing.T) {
	p, jobs := load(t, map[string]string{"ci.yml": ci1})
	if len(p.Warnings) != 0 {
		t.Errorf("warnings: %v", p.Warnings)
	}
	var keys []string
	for _, j := range p.Jobs {
		keys = append(keys, j.Key)
	}
	if got := strings.Join(keys, ","); got != "ci/lint,ci/build,ci/package" {
		t.Errorf("jobs keep the order of the file: %s", got)
	}
	b := jobs["ci/build"]
	if b.QualID != "svc/.github/workflows/ci.yml#build" || b.Name != "Build it" || b.Project != "CI" || b.File != ".github/workflows/ci.yml" || b.Line == 0 {
		t.Errorf("build: %+v", b)
	}
	if jobs["ci/lint"].Name != "lint" {
		t.Errorf("a job without name: is called by its id, got %q", jobs["ci/lint"].Name)
	}
}

func TestStepsParamsAndExpand(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": ci1})
	l := jobs["ci/lint"]
	if len(l.Steps) != 2 || l.Steps[0].Type != "uses" || l.Steps[0].Summary != "actions/checkout@v4" {
		t.Fatalf("lint steps: %+v", l.Steps)
	}
	if s := l.Steps[1]; s.Type != "run" || s.Name != "Lint it" || s.Summary != "make lint" || !strings.Contains(s.Script, "# comment first") {
		t.Errorf("run step (summary skips comments): %+v", s)
	}
	b := jobs["ci/build"]
	if b.Params["REG"] != "ghcr.io/acme" || b.Params["TARGET"] != "pkg" {
		t.Errorf("workflow and job env: %v", b.Params)
	}
	if l.Params["TARGET"] != "" {
		t.Error("job env must not leak into other jobs")
	}
	if got := b.Expand("make ${{ env.TARGET }} V=${{ github.sha }} ${{ env.NOPE }}"); got != "make pkg V=${EXPR} ${EXPR}" {
		t.Errorf("Expand: %q", got)
	}
	if b.Steps[0].WorkDir != "svc" {
		t.Errorf("working-directory: %q", b.Steps[0].WorkDir)
	}
}

func TestDefaultWorkingDirectory(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": `
on: push
defaults:
  run:
    working-directory: wf
jobs:
  a:
    steps: [{run: make a}]
  b:
    defaults: {run: {working-directory: job}}
    steps: [{run: make b}, {run: make c, working-directory: step}]
`})
	if jobs["ci/a"].Steps[0].WorkDir != "wf" || jobs["ci/b"].Steps[0].WorkDir != "job" || jobs["ci/b"].Steps[1].WorkDir != "step" {
		t.Errorf("most specific wins: %+v %+v", jobs["ci/a"].Steps, jobs["ci/b"].Steps)
	}
}

func TestNeedsAndArtifacts(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": ci1})
	if d := jobs["ci/build"].Deps; len(d) != 1 || d[0].Ref.Local != "ci/lint" || d[0].Artifacts {
		t.Errorf("needs is a plain dependency: %+v", d)
	}
	d := jobs["ci/package"].Deps
	if len(d) != 1 || !d[0].Artifacts || d[0].Rules != "bin" || d[0].Ref.Local != "ci/build" {
		t.Errorf("a download of an upload is an artifact dependency: %+v", d)
	}
	if got := strings.Join(jobs["ci/build"].Artifacts, ","); got != "out/app,out/app.sig" {
		t.Errorf("upload paths: %s", got)
	}
}

func TestTriggersLandOnTheFirstJobs(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": ci1})
	var got []string
	for _, tr := range jobs["ci/lint"].Triggers {
		got = append(got, tr.Type+":"+tr.Detail)
	}
	if want := "vcs:main,release|pull_request:|schedule:0 3 * * 1|manual:"; strings.Join(got, "|") != want {
		t.Errorf("triggers:\n%s\nwant:\n%s", strings.Join(got, "|"), want)
	}
	if len(jobs["ci/build"].Triggers) != 0 {
		t.Error("jobs that wait for others are not started by the event")
	}
}

func TestOnAsScalarAndList(t *testing.T) {
	_, jobs := load(t, map[string]string{
		"a.yml": "on: push\njobs: {x: {steps: [{run: a}]}}",
		"b.yml": "on: [push, release]\njobs: {x: {steps: [{run: a}]}}",
	})
	if tr := jobs["a/x"].Triggers; len(tr) != 1 || tr[0].Type != "vcs" {
		t.Errorf("scalar: %+v", tr)
	}
	if tr := jobs["b/x"].Triggers; len(tr) != 2 || tr[1].Type != "release" {
		t.Errorf("list; other events keep their name: %+v", tr)
	}
}

func TestWorkflowRun(t *testing.T) {
	p, jobs := load(t, map[string]string{
		"ci.yml": "name: CI\non: push\njobs:\n  a: {steps: [{run: x}]}\n  b: {needs: a, steps: [{run: x}]}\n",
		"deploy.yml": `
name: Deploy
on:
  workflow_run:
    workflows: [CI, ci, Ghost]
    types: [completed]
jobs:
  go: {steps: [{run: x}]}
`,
	})
	tr := jobs["deploy/go"].Triggers
	if len(tr) != 2 {
		t.Fatalf("triggers: %+v", tr)
	}
	for _, x := range tr {
		if x.From == nil || x.From.Local != "ci/b" || x.Type != "finishBuild" || x.Detail != "completed" {
			t.Errorf("a workflow_run starts after the named workflow's last job: %+v", x)
		}
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), `deploy.yml:5: workflow_run names "Ghost", which is not a workflow of this repo`) {
		t.Errorf("warnings: %v", p.Warnings)
	}
}

func TestReusableWorkflows(t *testing.T) {
	p, jobs := load(t, map[string]string{
		"ci.yml": `
on: push
jobs:
  remote:
    uses: acme/shared/.github/workflows/go.yml@v1
  local:
    uses: ./.github/workflows/lib.yml
  action:
    uses: acme/shared/.github/workflows/go.txt@v1
`,
		"lib.yml": "on: workflow_call\njobs:\n  a: {steps: [{run: x}]}\n  b: {needs: a, steps: [{run: x}]}\n",
	})
	r := jobs["ci/remote"]
	if !r.Composite || len(r.Deps) != 1 || r.Deps[0].Ref.Global != "shared/.github/workflows/go.yml" {
		t.Errorf("a call to another repo's workflow: %+v", r.Deps)
	}
	if l := jobs["ci/local"]; len(l.Deps) != 1 || l.Deps[0].Ref.Global != "svc/.github/workflows/lib.yml" {
		t.Errorf("a call to this repo's workflow: %+v", l.Deps)
	}
	if a := jobs["lib/b"].Aliases; len(a) != 1 || a[0] != "svc/.github/workflows/lib.yml" {
		t.Errorf("the last job answers for the workflow: %v", a)
	}
	if len(jobs["lib/a"].Aliases) != 0 {
		t.Error("only the last job is the workflow")
	}
	if len(jobs["lib/a"].Triggers) != 0 {
		t.Error("workflow_call is not an event")
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "which is not a reusable workflow") {
		t.Errorf("warnings: %v", p.Warnings)
	}
}

func TestDockerSteps(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": `
on: push
env:
  REG: ghcr.io/acme
jobs:
  image:
    container:
      image: ghcr.io/acme/builder:1
    services:
      db:
        image: postgres:16
    steps:
      - uses: docker/build-push-action@v6
        with:
          context: app
          file: app/Dockerfile
          push: true
          build-args: |
            BASE=${{ env.REG }}/base:1
          tags: |
            ${{ env.REG }}/app:${{ github.sha }}
            ${{ env.REG }}/app:latest
      - uses: docker/build-push-action@v6
        with:
          tags: ${{ env.REG }}/dry:1
`})
	steps := jobs["ci/image"].Steps
	if steps[0].Script != "docker pull ghcr.io/acme/builder:1" || steps[1].Script != "docker pull postgres:16" {
		t.Errorf("container and services are pulled: %+v", steps[:2])
	}
	b := steps[2]
	for _, want := range []string{
		"docker build -f app/Dockerfile -t ${{ env.REG }}/app:${{ github.sha }} -t ${{ env.REG }}/app:latest --build-arg BASE=${{ env.REG }}/base:1 app",
		"docker push ${{ env.REG }}/app:latest",
	} {
		if !strings.Contains(b.Script, want) {
			t.Errorf("script lacks %q:\n%s", want, b.Script)
		}
	}
	if got := jobs["ci/image"].Expand(b.Script); !strings.Contains(got, "-t ghcr.io/acme/app:${EXPR} ") {
		t.Errorf("after Expand the tags are single words: %s", got)
	}
	if dry := steps[3].Script; strings.Contains(dry, "docker push") || !strings.HasSuffix(dry, "docker build -t ${{ env.REG }}/dry:1 .") {
		t.Errorf("without push: there is no push, and the context defaults to .: %q", dry)
	}
}

func TestBrokenWorkflowsAreWarnings(t *testing.T) {
	p, jobs := load(t, map[string]string{
		"good.yml":    "on: push\njobs: {a: {steps: [{run: x}]}}",
		"syntax.yml":  "jobs: [unclosed",
		"scalar.yml":  "just text",
		"nojobs.yml":  "on: push",
		"badneed.yml": "on: push\njobs:\n  a: {needs: ghost, steps: [{run: x}]}\n",
	})
	if jobs["good/a"] == nil {
		t.Error("one broken file must not hide the others")
	}
	joined := strings.Join(p.Warnings, "\n")
	for _, w := range []string{
		".github/workflows/syntax.yml:1: cannot parse workflow",
		".github/workflows/scalar.yml:1: not a workflow",
		".github/workflows/nojobs.yml:1: workflow has no jobs",
		"job a needs ghost, which is not a job of this workflow",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing warning %q in:\n%s", w, joined)
		}
	}
	if _, ok := jobs["badneed/a"]; !ok || len(jobs["badneed/a"].Deps) != 0 {
		t.Error("the job stays, minus the dependency that cannot exist")
	}
}

func TestStepWithoutRunOrUses(t *testing.T) {
	_, jobs := load(t, map[string]string{"ci.yml": "on: push\njobs:\n  a:\n    steps:\n      - name: nothing\n      - run: make\n"})
	if s := jobs["ci/a"].Steps; len(s) != 1 || s[0].Summary != "make" {
		t.Errorf("steps: %+v", s)
	}
}

func TestWorkflowSymlinkedOutsideTheRepositoryIsNotRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	r := checkout(t, map[string]string{"ok.yml": "on: push\njobs:\n  build:\n    runs-on: x\n    steps:\n      - run: make\n"})
	outside := filepath.Join(t.TempDir(), "evil.yml")
	if err := os.WriteFile(outside, []byte("on: push\njobs:\n  leaked:\n    runs-on: x\n    steps:\n      - run: make\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Root, ".github", "workflows", "evil.yml")); err != nil {
		t.Fatal(err)
	}
	p, err := New().Load(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p.Jobs {
		if strings.Contains(j.Key, "leaked") {
			t.Errorf("job %q comes from a workflow outside the repository", j.Key)
		}
	}
	if got := strings.Join(p.Warnings, "\n"); !strings.Contains(got, "evil.yml") || !strings.Contains(got, "escapes") {
		t.Errorf("the refusal must be reported, got %q", got)
	}
}
