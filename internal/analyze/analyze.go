// Package analyze turns checked-out repositories into the pipeline graph:
// TeamCity build configurations, the make targets they run, and the docker
// images that flow between them.
package analyze

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/kts"
	"github.com/Avicted/tools.pipeline-graph/internal/makefile"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/shell"
)

type Repo struct {
	Name        string // short name, used as swimlane title
	URL         string
	Commit      string
	Root        string // local checkout
	TeamCityDir string // relative to Root, default ".teamcity"
	ProjectID   string // TeamCity external id of the project the settings are imported into
}

type Options struct {
	ExternalIDs map[string]string // absolute TeamCity id -> "repo/RelativeId"
}

type analyzer struct {
	opts      Options
	g         *model.Graph
	warnings  []string
	byAbs     map[string]string   // absolute TeamCity id -> node id
	byRel     map[string][]string // relative id -> node ids (all repos)
	makefiles map[string]*makefile.Makefile
	expanded  map[string]bool
	images    map[string]*imageUse
}

type imageUse struct {
	producers map[string]bool
	consumers map[string]bool
	refs      map[string]bool
}

// Run analyses all repos and returns the graph plus human-readable warnings.
func Run(repos []Repo, opts Options) (*model.Graph, []string) {
	a := &analyzer{
		opts:      opts,
		g:         model.New(),
		byAbs:     map[string]string{},
		byRel:     map[string][]string{},
		makefiles: map[string]*makefile.Makefile{},
		expanded:  map[string]bool{},
		images:    map[string]*imageUse{},
	}
	type parsed struct {
		repo Repo
		s    *kts.Settings
	}
	var all []parsed
	for _, r := range repos {
		a.g.Repos = append(a.g.Repos, model.Repo{Name: r.Name, URL: r.URL, Commit: r.Commit})
		dir := r.TeamCityDir
		if dir == "" {
			dir = ".teamcity"
		}
		if _, err := os.Stat(filepath.Join(r.Root, dir)); err != nil {
			a.warn(r.Name, "", 0, "no TeamCity settings directory %s", dir)
			continue
		}
		s, err := kts.ParseDir(filepath.Join(r.Root, dir), r.Root)
		if err != nil {
			a.warn(r.Name, "", 0, "reading TeamCity settings: %v", err)
			continue
		}
		for _, w := range s.Warnings {
			a.warnings = append(a.warnings, r.Name+": "+w)
		}
		if r.ProjectID == "" {
			r.ProjectID = DeriveProjectID(r.Name)
		}
		all = append(all, parsed{r, s})
		for _, b := range s.Builds {
			id := buildNodeID(r.Name, b.Key)
			abs := r.ProjectID + "_" + b.Key
			a.byAbs[abs] = id
			a.byRel[b.Key] = append(a.byRel[b.Key], id)
			n := &model.Node{
				ID: id, Kind: model.KindBuild, Repo: r.Name, Label: b.Name, Sub: abs,
				Project: b.Project, Composite: b.Composite, Steps: b.Steps,
				Source: fmt.Sprintf("%s:%d", b.File, b.Line),
			}
			if len(b.Params) > 0 {
				n.Params = b.Params
			}
			for _, t := range b.Triggers {
				if t.Ref == nil {
					n.Triggers = append(n.Triggers, t.Trigger)
				}
			}
			for _, l := range strings.Split(b.ArtifactRules, "\n") {
				if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "-:") {
					n.Artifacts = append(n.Artifacts, strings.TrimPrefix(l, "+:"))
				}
			}
			a.g.AddNode(n)
		}
	}
	for abs, target := range opts.ExternalIDs {
		repo, rel, _ := strings.Cut(target, "/")
		a.byAbs[abs] = buildNodeID(repo, rel)
	}

	for _, p := range all {
		for _, b := range p.s.Builds {
			to := buildNodeID(p.repo.Name, b.Key)
			for _, d := range b.Deps {
				a.depEdge(p.repo, p.s, d, to, b.File)
			}
			for _, t := range b.Triggers {
				if t.Ref == nil {
					continue
				}
				if from := a.resolve(p.repo, p.s, *t.Ref, b.File); from != "" {
					a.g.AddEdge(model.Edge{From: from, To: to, Kind: model.EdgeTrigger, Label: t.Detail})
					a.g.Node(to).Triggers = append(a.g.Node(to).Triggers, model.Trigger{Type: "finishBuild", Detail: a.g.Node(from).Label})
				}
			}
			a.steps(p.repo, b, to)
		}
		for _, cd := range p.s.CrossDeps {
			if to := a.resolve(p.repo, p.s, cd.Target, cd.File); to != "" {
				a.depEdge(p.repo, p.s, cd.Dep, to, cd.File)
			}
		}
	}
	a.imageNodes()
	a.g.Sort()
	sort.Strings(a.warnings)
	return a.g, slices.Compact(a.warnings)
}

func buildNodeID(repo, key string) string { return "bt:" + repo + "/" + key }

func normID(s string) string { return strings.ToLower(reWordSplit.ReplaceAllString(s, "")) }

var reWordSplit = regexp.MustCompile(`[^A-Za-z0-9]+`)

// DeriveProjectID mimics how TeamCity derives a project id from a name: "service-a" -> "ServiceA".
func DeriveProjectID(name string) string {
	var b strings.Builder
	for _, w := range reWordSplit.Split(name, -1) {
		if w != "" {
			b.WriteString(strings.ToUpper(w[:1]) + w[1:])
		}
	}
	return b.String()
}

func (a *analyzer) warn(repo, file string, line int, format string, args ...any) {
	loc := repo
	if file != "" {
		loc += ": " + file
		if line > 0 {
			loc += fmt.Sprintf(":%d", line)
		}
	}
	a.warnings = append(a.warnings, loc+": "+fmt.Sprintf(format, args...))
}

func (a *analyzer) depEdge(r Repo, s *kts.Settings, d kts.Dep, to, file string) {
	from := a.resolve(r, s, d.Ref, file)
	if from == "" {
		return
	}
	kind := model.EdgeSnapshot
	if d.Artifacts {
		kind = model.EdgeArtifact
	}
	a.g.AddEdge(model.Edge{From: from, To: to, Kind: kind, Label: oneLine(d.Rules)})
}

// resolve maps a DSL reference to a node id, creating an external stub if needed.
func (a *analyzer) resolve(r Repo, s *kts.Settings, ref kts.Ref, file string) string {
	switch ref.Kind {
	case kts.RefObject, kts.RefRelative:
		if b := s.Lookup(ref); b != nil {
			return buildNodeID(r.Name, b.Key)
		}
		if ref.Kind == kts.RefObject {
			a.warn(r.Name, file, ref.Line, "reference to unknown build configuration %s", ref.Value)
			return ""
		}
	}
	if id, ok := a.byAbs[ref.Value]; ok && a.g.Node(id) != nil {
		return id
	}
	// RelativeId inside a string, or an id without the root project prefix
	if b := s.Lookup(kts.Ref{Kind: kts.RefRelative, Value: ref.Value}); b != nil {
		return buildNodeID(r.Name, b.Key)
	}
	// heuristic: unique build whose relative id is a suffix of the absolute id
	// and whose repo name appears in the prefix (e.g. "Acme_ServiceA_Build" for repo service-a)
	var cands []string
	for rel, ids := range a.byRel {
		prefix, ok := strings.CutSuffix(ref.Value, "_"+rel)
		if !ok {
			continue
		}
		for _, id := range ids {
			if strings.HasSuffix(normID(prefix), normID(a.g.Node(id).Repo)) {
				cands = append(cands, id)
			}
		}
	}
	if len(cands) == 1 {
		a.warn(r.Name, file, ref.Line, "%s matched to %s by id suffix (set teamcity_project_id or external_ids to make this exact)", ref.Value, cands[0])
		return cands[0]
	}
	a.warn(r.Name, file, ref.Line, "build configuration %s not found in any analysed repo; shown as external", ref.Value)
	id := "ext:" + ref.Value
	a.g.AddNode(&model.Node{ID: id, Kind: model.KindExternal, Label: ref.Value, Sub: "not in analysed repos"})
	return id
}

var reParam = regexp.MustCompile(`%([A-Za-z0-9_.\-]+)%`)

func substParams(s string, params map[string]string) string {
	for i := 0; i < 3 && strings.Contains(s, "%"); i++ {
		s = reParam.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := params[m[1:len(m)-1]]; ok {
				return v
			}
			return m
		})
	}
	return s
}

// steps analyses the commands each build step runs.
func (a *analyzer) steps(r Repo, b *kts.Build, owner string) {
	for _, st := range b.Steps {
		script := substParams(st.Script, b.Params)
		label := st.Name
		a.commands(r, script, cleanDir(substParams(st.WorkDir, b.Params)), owner, label, 0)
	}
}

func cleanDir(d string) string {
	if d == "" || strings.HasPrefix(d, "/") || strings.Contains(d, "%") {
		return "."
	}
	return path.Clean(d)
}

// commands extracts make/docker/script invocations from script text run in dir.
// owner is the node (build or make target) the work is attributed to.
func (a *analyzer) commands(r Repo, script, dir, owner, label string, depth int) {
	res := shell.Extract(script)
	for _, mk := range res.Makes {
		mdir := path.Clean(path.Join(dir, mk.Dir))
		kind := model.EdgeRuns
		if a.g.Node(owner).Kind == model.KindMake {
			kind = model.EdgeCalls
		}
		a.makeCall(r, mdir, mk.File, mk.Targets, mk.Vars, owner, kind, label, depth)
	}
	for _, d := range res.Dockers {
		a.docker(r, d, dir, owner)
	}
	if depth >= 3 {
		return
	}
	for _, sc := range res.Scripts {
		p := path.Clean(path.Join(dir, sc.Path))
		b, err := os.ReadFile(filepath.Join(r.Root, p))
		if err != nil {
			continue // not a repo file (or generated at build time)
		}
		a.commands(r, string(b), dir, owner, label, depth+1)
	}
}

func (a *analyzer) loadMakefile(r Repo, dir, file string, vars map[string]string) *makefile.Makefile {
	key := r.Name + "\x00" + dir + "\x00" + file + "\x00" + fmt.Sprint(sortedKV(vars))
	if m, ok := a.makefiles[key]; ok {
		return m
	}
	m, err := makefile.Load(r.Root, dir, file, vars)
	if err != nil {
		m = nil
	} else {
		for _, w := range m.Warnings {
			a.warn(r.Name, "", 0, "%s", w)
		}
	}
	a.makefiles[key] = m
	return m
}

func sortedKV(m map[string]string) []string {
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

func makeNodeID(repo, makefilePath, target string) string {
	return "mk:" + repo + "/" + makefilePath + ":" + target
}

func (a *analyzer) makeCall(r Repo, dir, file string, targets []string, vars map[string]string, owner string, kind model.EdgeKind, label string, depth int) {
	if strings.ContainsAny(dir, "$%") || strings.HasPrefix(dir, "..") {
		a.warn(r.Name, "", 0, "make -C %s (from %s) cannot be resolved statically", dir, a.g.Node(owner).Label)
		return
	}
	m := a.loadMakefile(r, dir, file, vars)
	if m == nil {
		a.warn(r.Name, "", 0, "%s runs make in %q but no makefile was found there", a.g.Node(owner).Label, dir)
		return
	}
	if len(targets) == 0 {
		if m.DefaultGoal == "" {
			return
		}
		targets = []string{m.DefaultGoal}
	}
	for _, t := range targets {
		t = m.Expand(t)
		if strings.ContainsAny(t, "$%") || t == "" {
			continue
		}
		id := a.makeTarget(r, m, t, depth)
		a.g.AddEdge(model.Edge{From: owner, To: id, Kind: kind, Label: label})
	}
}

// makeTarget adds a make target node and, once, everything it needs and runs.
func (a *analyzer) makeTarget(r Repo, m *makefile.Makefile, target string, depth int) string {
	id := makeNodeID(r.Name, m.Path, target)
	rule := m.Rules[target]
	n := a.g.AddNode(&model.Node{ID: id, Kind: model.KindMake, Repo: r.Name, Label: target, Sub: m.Path})
	if rule == nil {
		if n.Source == "" {
			n.Sub = m.Path + " (no rule)"
			a.warn(r.Name, m.Path, 0, "make target %q is invoked but not defined", target)
		}
		return id
	}
	n.Source = fmt.Sprintf("%s:%d", rule.File, rule.Line)
	if a.expanded[id] || depth > 12 {
		return id
	}
	a.expanded[id] = true
	var recipe []string
	for _, l := range rule.Recipe {
		recipe = append(recipe, m.ExpandRecipe(rule, l))
	}
	if len(recipe) > 0 {
		n.Steps = []model.Step{{Type: "recipe", Script: strings.Join(recipe, "\n"), Summary: firstLine(recipe)}}
	}
	for _, p := range rule.Prereqs {
		if m.Rules[p] == nil {
			continue // plain file prerequisite
		}
		pid := a.makeTarget(r, m, p, depth+1)
		a.g.AddEdge(model.Edge{From: id, To: pid, Kind: model.EdgeDepends})
	}
	a.commands(r, strings.Join(recipe, "\n"), m.Dir, id, "", depth+1)
	return id
}

func firstLine(lines []string) string {
	for _, l := range lines {
		l = strings.TrimLeft(strings.TrimSpace(l), "@-+")
		if l != "" {
			if len(l) > 60 {
				l = l[:57] + "..."
			}
			return l
		}
	}
	return ""
}

func (a *analyzer) docker(r Repo, d shell.Docker, dir, owner string) {
	for _, img := range d.Images {
		if d.Action == "pull" {
			a.useImage(img, owner, false)
		} else {
			a.useImage(img, owner, true)
		}
	}
	for _, img := range d.Consumed {
		a.useImage(img, owner, false)
	}
	if !strings.HasPrefix(d.Action, "build") {
		return
	}
	ctx := d.Context
	if ctx == "" || strings.ContainsAny(ctx, "$%") {
		ctx = "."
	}
	df := d.Dockerfile
	if df == "" {
		df = path.Join(ctx, "Dockerfile")
	}
	df = path.Join(dir, df)
	b, err := os.ReadFile(filepath.Join(r.Root, df))
	if err != nil {
		return
	}
	for _, img := range shell.DockerfileFrom(string(b), nil) {
		a.useImage(img, owner, false)
	}
}

var reVarRef = regexp.MustCompile(`%[^%]*%|\$\([^)]*\)|\$\{[^}]*\}|\$[A-Za-z_]\w*`)

// ImageName normalises an image reference to registry/name without tag or digest.
// Returns "" when the name itself is not static.
func ImageName(ref string) string {
	ref = strings.Trim(ref, `"'`)
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		ref = ref[:i]
	}
	if reVarRef.MatchString(ref) || ref == "" {
		return ""
	}
	return ref
}

func (a *analyzer) useImage(ref, owner string, produce bool) {
	name := ImageName(ref)
	if name == "" {
		if produce {
			a.warn(a.g.Node(owner).Repo, "", 0, "image %q (from %s) has a dynamic name; not linked", ref, a.g.Node(owner).Label)
		}
		return
	}
	u := a.images[name]
	if u == nil {
		u = &imageUse{producers: map[string]bool{}, consumers: map[string]bool{}, refs: map[string]bool{}}
		a.images[name] = u
	}
	u.refs[ref] = true
	if produce {
		u.producers[owner] = true
	} else {
		u.consumers[owner] = true
	}
}

// imageNodes materialises images produced by an analysed pipeline, and links their consumers.
func (a *analyzer) imageNodes() {
	names := make([]string, 0, len(a.images))
	for n := range a.images {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		u := a.images[name]
		if len(u.producers) == 0 {
			continue // third-party image (alpine, golang, ...)
		}
		producers := keys(u.producers)
		repo := a.g.Node(producers[0]).Repo
		id := "img:" + name
		a.g.AddNode(&model.Node{ID: id, Kind: model.KindImage, Repo: repo, Label: path.Base(name), Sub: name})
		for _, p := range producers {
			a.g.AddEdge(model.Edge{From: p, To: id, Kind: model.EdgeProduces})
		}
		for _, c := range keys(u.consumers) {
			if !u.producers[c] {
				a.g.AddEdge(model.Edge{From: id, To: c, Kind: model.EdgeConsumes})
			}
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\n", "; ")), " ")
}
