// Package github is the ci.Provider for GitHub Actions workflows.
//
// Every job of every .github/workflows/*.yml becomes a ci.Job. Its Key is
// "<workflow file>/<job id>" and its QualID "<repo>/.github/workflows/<file>#<job>".
// A workflow as a whole is addressed as "<repo>/.github/workflows/<file>" and
// stands for its last job, the one no other job needs.
package github

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/repofs"
	"github.com/Avicted/tools.pipeline-graph/internal/shell"
)

const workflowsDir = ".github/workflows"

type Provider struct{}

func New() Provider { return Provider{} }

func (Provider) System() ci.System {
	return ci.System{Name: "github", Title: "GitHub Actions", Job: "workflow job", ID: "job path"}
}

func workflowFiles(root string) []string {
	var out []string
	for _, ext := range []string{"yml", "yaml"} {
		m, _ := filepath.Glob(filepath.Join(root, workflowsDir, "*."+ext))
		out = append(out, m...)
	}
	sort.Strings(out)
	return out
}

func (Provider) Detect(r ci.Repo) bool { return len(workflowFiles(r.Root)) > 0 }

// workflow is one parsed file.
type workflow struct {
	file  string // base name, e.g. ci.yml
	stem  string
	name  string
	final string // key of the job that stands for the whole workflow
	jobs  []*ci.Job
	// workflow_run triggers wait until every workflow is known
	runs []pendingRun
}

type pendingRun struct {
	names []string
	types string
	line  int
	roots []*ci.Job
}

func (Provider) Load(r ci.Repo) (*ci.Pipelines, error) {
	files := workflowFiles(r.Root)
	if len(files) == 0 {
		return nil, fmt.Errorf("no workflows in %s", workflowsDir)
	}
	p := &ci.Pipelines{}
	var wfs []*workflow
	for _, f := range files {
		wf, warns := parseFile(r, f)
		p.Warnings = append(p.Warnings, warns...)
		if wf != nil {
			wfs = append(wfs, wf)
		}
	}
	byName := map[string]*workflow{}
	for _, wf := range wfs {
		byName[wf.name] = wf
		byName[wf.stem] = wf
	}
	for _, wf := range wfs {
		for _, pr := range wf.runs {
			var refs []ci.Ref
			for _, n := range pr.names {
				target := byName[n]
				if target == nil || target.final == "" {
					p.Warnings = append(p.Warnings, fmt.Sprintf("%s/%s:%d: workflow_run names %q, which is not a workflow of this repo", workflowsDir, wf.file, pr.line, n))
					continue
				}
				refs = append(refs, ci.Ref{Local: target.final, Line: pr.line})
			}
			for _, j := range pr.roots {
				for i := range refs {
					j.Triggers = append(j.Triggers, ci.Trigger{
						Trigger: model.Trigger{Type: "finishBuild", Detail: pr.types}, From: &refs[i],
					})
				}
			}
		}
		p.Jobs = append(p.Jobs, wf.jobs...)
	}
	return p, nil
}

func parseFile(r ci.Repo, file string) (*workflow, []string) {
	rel := workflowsDir + "/" + filepath.Base(file)
	fail := func(line int, format string, a ...any) (*workflow, []string) {
		return nil, []string{fmt.Sprintf("%s:%d: %s", rel, line, fmt.Sprintf(format, a...))}
	}
	b, err := repofs.ReadFile(r.Root, rel)
	if err != nil {
		return fail(1, "%s", repofs.Reason(err))
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return fail(1, "cannot parse workflow: %v", err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return fail(1, "not a workflow (expected a mapping at the top)")
	}
	root := doc.Content[0]
	jobs := get(root, "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return fail(root.Line, "workflow has no jobs")
	}

	base := filepath.Base(file)
	wf := &workflow{file: base, stem: strings.TrimSuffix(base, filepath.Ext(base))}
	wf.name = scalar(get(root, "name"))
	if wf.name == "" {
		wf.name = wf.stem
	}
	wfEnv := strMap(get(root, "env"))
	wfDir := scalar(get(get(get(root, "defaults"), "run"), "working-directory"))
	var warns []string
	warn := func(line int, format string, a ...any) {
		warns = append(warns, fmt.Sprintf("%s:%d: %s", rel, line, fmt.Sprintf(format, a...)))
	}

	uploads := map[string][]string{} // job id -> artifact names
	needs := map[string][]string{}   // job id -> job ids
	ids := map[string]bool{}
	for i := 0; i < len(jobs.Content); i += 2 {
		ids[jobs.Content[i].Value] = true
	}
	needed := map[string]bool{}
	byID := map[string]*ci.Job{}
	downloads := map[string][]string{}
	var order []string

	for i := 0; i+1 < len(jobs.Content); i += 2 {
		id, jn := jobs.Content[i].Value, jobs.Content[i+1]
		j := &ci.Job{
			Key: wf.stem + "/" + id, QualID: r.Name + "/" + workflowsDir + "/" + base + "#" + id,
			Name: id, Project: wf.name, File: rel, Line: jobs.Content[i].Line,
		}
		if n := scalar(get(jn, "name")); n != "" && !strings.Contains(n, "${{") {
			j.Name = n
		}
		env := map[string]string{}
		for k, v := range wfEnv {
			env[k] = v
		}
		for k, v := range strMap(get(jn, "env")) {
			env[k] = v
		}
		if len(env) > 0 {
			j.Params = env
		}
		j.Expand = func(s string) string { return expandExpr(s, env) }
		dir := wfDir
		if d := scalar(get(get(get(jn, "defaults"), "run"), "working-directory")); d != "" {
			dir = d
		}

		for _, n := range list(get(jn, "needs")) {
			if !ids[n] {
				warn(jn.Line, "job %s needs %s, which is not a job of this workflow", id, n)
				continue
			}
			needs[id] = append(needs[id], n)
			needed[n] = true
		}

		if u := scalar(get(jn, "uses")); u != "" { // a reusable workflow called as a whole job
			j.Composite = true
			j.Steps = []model.Step{{Type: "uses", Summary: u}}
			if g := reusableID(r.Name, u); g != "" {
				j.Deps = append(j.Deps, ci.Dep{Ref: ci.Ref{Global: g, Line: jn.Line}})
			} else {
				warn(jn.Line, "job %s calls %q, which is not a reusable workflow in .github/workflows", id, u)
			}
		}
		if c := get(jn, "container"); c != nil {
			img := scalar(c)
			if c.Kind == yaml.MappingNode {
				img = scalar(get(c, "image"))
			}
			j.Steps = append(j.Steps, pullStep("container", img)...)
		}
		if sv := get(jn, "services"); sv != nil && sv.Kind == yaml.MappingNode {
			for k := 0; k+1 < len(sv.Content); k += 2 {
				j.Steps = append(j.Steps, pullStep("service "+sv.Content[k].Value, scalar(get(sv.Content[k+1], "image")))...)
			}
		}
		for _, sn := range seq(get(jn, "steps")) {
			st, up, down := step(sn, dir)
			j.Steps = append(j.Steps, st...)
			uploads[id] = append(uploads[id], up...)
			downloads[id] = append(downloads[id], down...)
			if w := get(get(sn, "with"), "path"); w != nil && strings.HasPrefix(scalar(get(sn, "uses")), "actions/upload-artifact") {
				for _, l := range strings.Split(scalar(w), "\n") {
					if l = strings.TrimSpace(l); l != "" {
						j.Artifacts = append(j.Artifacts, l)
					}
				}
			}
		}
		byID[id] = j
		order = append(order, id)
		wf.jobs = append(wf.jobs, j)
	}

	for _, id := range order {
		j := byID[id]
		for _, n := range needs[id] {
			d := ci.Dep{Ref: ci.Ref{Local: wf.stem + "/" + n, Line: j.Line}}
			if names := matching(uploads[n], downloads[id]); len(names) > 0 {
				d.Artifacts, d.Rules = true, strings.Join(names, ", ")
			}
			j.Deps = append(j.Deps, d)
		}
		if !needed[id] {
			wf.final = j.Key // the last job nobody needs stands for the workflow
		}
	}
	if wf.final != "" {
		// shortcut: a workflow with several final jobs is addressed by the last one, split it if that matters
		byID[strings.TrimPrefix(wf.final, wf.stem+"/")].Aliases = []string{r.Name + "/" + workflowsDir + "/" + base}
	}

	var roots []*ci.Job
	for _, id := range order {
		if len(needs[id]) == 0 {
			roots = append(roots, byID[id])
		}
	}
	triggers(get(root, "on"), roots, wf, warn)
	return wf, warns
}

// triggers attaches the events that start the workflow to its first jobs.
func triggers(on *yaml.Node, roots []*ci.Job, wf *workflow, warn func(int, string, ...any)) {
	if on == nil {
		return
	}
	events := map[string]*yaml.Node{}
	var names []string
	switch on.Kind {
	case yaml.ScalarNode:
		names = []string{on.Value}
	case yaml.SequenceNode:
		names = list(on)
	case yaml.MappingNode:
		for i := 0; i+1 < len(on.Content); i += 2 {
			names = append(names, on.Content[i].Value)
			events[on.Content[i].Value] = on.Content[i+1]
		}
	}
	add := func(t model.Trigger) {
		for _, j := range roots {
			j.Triggers = append(j.Triggers, ci.Trigger{Trigger: t})
		}
	}
	for _, n := range names {
		cfg := events[n]
		switch n {
		case "push":
			add(model.Trigger{Type: "vcs", Detail: strings.Join(list(get(cfg, "branches")), ",")})
		case "schedule":
			for _, s := range seq(cfg) {
				add(model.Trigger{Type: "schedule", Detail: scalar(get(s, "cron"))})
			}
		case "workflow_dispatch":
			add(model.Trigger{Type: "manual"})
		case "workflow_call":
			// a reusable workflow: started by its caller, which is drawn as a dependency
		case "workflow_run":
			types := strings.Join(list(get(cfg, "types")), ",")
			if types == "" {
				types = "completed"
			}
			line := 0
			if cfg != nil {
				line = cfg.Line
			}
			wf.runs = append(wf.runs, pendingRun{names: list(get(cfg, "workflows")), types: types, line: line, roots: roots})
		default:
			add(model.Trigger{Type: n})
		}
	}
}

var reUses = regexp.MustCompile(`^(?:([^/@\s]+)/([^/@\s]+)/)?(\.github/workflows/[^@\s]+\.ya?ml)(?:@\S+)?$`)

// reusableID turns `owner/repo/.github/workflows/x.yml@ref` or `./.github/workflows/x.yml`
// into the id the called workflow answers to. "" if uses is not a reusable workflow.
func reusableID(self, uses string) string {
	uses = strings.TrimPrefix(uses, "./")
	m := reUses.FindStringSubmatch(uses)
	if m == nil {
		return ""
	}
	repo := self
	if m[2] != "" {
		repo = m[2]
	}
	return repo + "/" + m[3]
}

func pullStep(name, image string) []model.Step {
	if image == "" {
		return nil
	}
	return []model.Step{{Name: name, Type: "container", Summary: name + ": " + image, Script: "docker pull " + image}}
}

var reExpr = regexp.MustCompile(`\$\{\{\s*(.*?)\s*\}\}`)

// expandExpr resolves ${{ env.NAME }}. Any other expression is only known to
// the runner, so it becomes one opaque variable: a word the shell scanner keeps
// whole, and an image name it knows is dynamic.
func expandExpr(s string, env map[string]string) string {
	return reExpr.ReplaceAllStringFunc(s, func(m string) string {
		e := strings.TrimSpace(reExpr.FindStringSubmatch(m)[1])
		if v, ok := strings.CutPrefix(e, "env."); ok {
			if val, ok := env[v]; ok {
				return val
			}
		}
		return "${EXPR}"
	})
}

// step converts one workflow step; up and down are the artifact names it uploads or downloads.
func step(n *yaml.Node, defaultDir string) (steps []model.Step, up, down []string) {
	name := scalar(get(n, "name"))
	dir := scalar(get(n, "working-directory"))
	if dir == "" {
		dir = defaultDir
	}
	if run := scalar(get(n, "run")); run != "" {
		return []model.Step{{Name: name, Type: "run", Script: run, WorkDir: dir, Summary: shell.Summary(run)}}, nil, nil
	}
	uses := scalar(get(n, "uses"))
	if uses == "" {
		return nil, nil, nil
	}
	with := strMap(get(n, "with"))
	st := model.Step{Name: name, Type: "uses", Summary: uses}
	action, _, _ := strings.Cut(uses, "@")
	switch {
	case strings.HasPrefix(action, "docker/build-push-action"):
		st.Script = buildScript(with)
		st.Summary = "docker build " + strings.Join(tags(with), " ")
	case strings.HasPrefix(action, "actions/upload-artifact"):
		up = []string{artifactName(with)}
	case strings.HasPrefix(action, "actions/download-artifact"):
		down = []string{with["name"]} // "" downloads everything
	}
	return []model.Step{st}, up, down
}

func artifactName(with map[string]string) string {
	if with["name"] != "" {
		return with["name"]
	}
	return "artifact"
}

// matching returns the uploaded names a job downloads; an unnamed download takes them all.
func matching(uploaded, downloaded []string) []string {
	var out []string
	for _, u := range uploaded {
		for _, d := range downloaded {
			if d == "" || d == u {
				out = append(out, u)
				break
			}
		}
	}
	return out
}

func tags(with map[string]string) []string {
	var out []string
	for _, f := range strings.FieldsFunc(with["tags"], func(r rune) bool { return r == '\n' || r == ',' }) {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// buildScript spells docker/build-push-action as the shell it runs, so the
// analyzer finds images, Dockerfiles and base images the same way as elsewhere.
func buildScript(with map[string]string) string {
	ts := tags(with)
	var args []string
	for _, a := range strings.Split(with["build-args"], "\n") {
		if a = strings.TrimSpace(a); a != "" {
			args = append(args, a)
		}
	}
	lines := []string{shell.BuildCommand(with["file"], ts, args, with["context"])}
	// push is off unless asked for; an expression is assumed to be on
	if p := with["push"]; p != "" && p != "false" {
		lines = append(lines, shell.PushCommands(ts)...)
	}
	return strings.Join(lines, "\n")
}

// --- yaml.Node helpers; all are nil-safe so lookups chain

func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode {
		return ""
	}
	return n.Value
}

func seq(n *yaml.Node) []*yaml.Node {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	return n.Content
}

// list reads "a" or [a, b] as a list of strings.
func list(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		return []string{n.Value}
	}
	var out []string
	for _, c := range seq(n) {
		if c.Kind == yaml.ScalarNode {
			out = append(out, c.Value)
		}
	}
	return out
}

// strMap reads a mapping's scalar values; nested values are skipped.
func strMap(n *yaml.Node) map[string]string {
	out := map[string]string{}
	if n == nil || n.Kind != yaml.MappingNode {
		return out
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if v := n.Content[i+1]; v.Kind == yaml.ScalarNode {
			out[n.Content[i].Value] = v.Value
		}
	}
	return out
}
