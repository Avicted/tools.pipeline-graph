package teamcity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/kts"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
	"github.com/Avicted/tools.pipeline-graph/internal/repofs"
	"github.com/Avicted/tools.pipeline-graph/internal/shell"
)

type RefKind int

const (
	RefObject   RefKind = iota // Kotlin object / val name in the same settings
	RefRelative                // RelativeId("X") or id("X"): relative to the settings root project
	RefAbsolute                // AbsoluteId("X"): full TeamCity external id
	RefString                  // bare string: usually an absolute external id
)

type Ref struct {
	Kind  RefKind
	Value string
	Line  int
}

func (r Ref) String() string {
	return [...]string{"object", "RelativeId", "AbsoluteId", "id"}[r.Kind] + "(" + r.Value + ")"
}

type Dep struct {
	Ref       Ref
	Artifacts bool
	Rules     string
}

type TriggerDef struct {
	model.Trigger
	Ref *Ref // finishBuildTrigger source
}

// Build is a TeamCity build configuration (or template) as declared in the DSL.
type Build struct {
	Key           string // unique within the settings: relative id
	ObjName       string // Kotlin object / val name, if any
	Name          string
	Project       string
	Composite     bool
	Template      bool
	Templates     []Ref
	Steps         []model.Step
	Deps          []Dep
	Triggers      []TriggerDef
	ArtifactRules string
	Params        map[string]string
	File          string
	Line          int
}

// CrossDep is a dependency whose dependent build is not declared in these settings.
type CrossDep struct {
	Target Ref
	Dep    Dep
	File   string
	Line   int
}

type Declared struct {
	Builds    []*Build // excludes templates
	CrossDeps []CrossDep
	Warnings  []string
	byKey     map[string]*Build
	byObj     map[string]*Build
	templates map[string]*Build
}

// Lookup resolves a Kotlin object/val name or relative id within these settings.
func (s *Declared) Lookup(r Ref) *Build {
	switch r.Kind {
	case RefObject:
		if b := s.byObj[r.Value]; b != nil {
			return b
		}
		return s.byKey[r.Value]
	case RefRelative:
		if b := s.byKey[r.Value]; b != nil {
			return b
		}
		return s.byObj[r.Value]
	}
	return nil
}

// parseDir reads every .kts/.kt file below dir. root is used to make file paths relative.
func parseDir(dir, root string) (*Declared, error) {
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() && (d.Name() == "target" || d.Name() == "build" || d.Name() == ".idea") {
			return filepath.SkipDir
		}
		if !d.IsDir() && (strings.HasSuffix(p, ".kts") || strings.HasSuffix(p, ".kt")) {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	srcs := map[string]string{}
	var warnings []string
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			return nil, err
		}
		rel = filepath.ToSlash(rel)
		b, err := repofs.ReadFile(root, rel)
		if err != nil { // one hostile or unreadable file must not hide the rest of the repository
			warnings = append(warnings, fmt.Sprintf("%s: not read: %s", rel, repofs.Reason(err)))
			continue
		}
		srcs[rel] = string(b)
	}
	s := parseSources(srcs)
	s.Warnings = append(warnings, s.Warnings...)
	return s, nil
}

// parseSources analyses a set of DSL files (name -> content) as one settings package.
func parseSources(srcs map[string]string) *Declared {
	e := &extractor{
		s:      &Declared{byKey: map[string]*Build{}, byObj: map[string]*Build{}, templates: map[string]*Build{}},
		objs:   map[string]objDef{},
		funs:   map[string]*kts.Node{},
		consts: map[string]string{},
		vals:   map[string]*kts.Node{},
		reg:    map[string]string{},
	}
	names := make([]string, 0, len(srcs))
	for n := range srcs {
		names = append(names, n)
	}
	sort.Strings(names)
	trees := map[string][]*kts.Node{}
	for _, n := range names {
		var truncated bool
		trees[n], truncated = kts.Parse(srcs[n])
		if truncated {
			e.s.Warnings = append(e.s.Warnings, fmt.Sprintf("%s:1: nesting is deeper than %d levels; part of this file is not analysed", n, kts.MaxDepth))
		}
		e.collect(trees[n], n, true)
	}
	for _, n := range names {
		e.file = n
		e.walk(trees[n], "")
	}
	e.finish()
	return e.s
}

type objDef struct {
	node *kts.Node
	file string
}

type extractor struct {
	s       *Declared
	file    string
	objs    map[string]objDef // object name -> declaration
	funs    map[string]*kts.Node
	consts  map[string]string
	vals    map[string]*kts.Node // top-level val name -> value expression
	reg     map[string]string
	subst   []map[string]string // function-argument substitutions (innermost last)
	pending []pendingDep
	refs    []map[string]*kts.Node // inlined function arguments as expressions (innermost last)
	depth   int
}

func (e *extractor) warn(line int, format string, a ...any) {
	e.s.Warnings = append(e.s.Warnings, fmt.Sprintf("%s:%d: %s", e.file, line, fmt.Sprintf(format, a...)))
}

// collect indexes declarations: objects, functions, constant vals.
func (e *extractor) collect(nodes []*kts.Node, file string, top bool) {
	for _, n := range nodes {
		switch n.Kind {
		case kts.NObject:
			if n.Name != "" {
				e.objs[n.Name] = objDef{n, file}
			}
			e.collect(n.Body, file, false)
		case kts.NFun:
			if n.Name != "" {
				e.funs[n.Name] = n
			}
		case kts.NVal:
			if n.Value != nil && n.Name != "" {
				if n.Value.Kind == kts.NStr {
					e.consts[n.Name] = n.Value.Str
				}
				if top {
					e.vals[n.Name] = n.Value
				}
			}
		case kts.NBlock:
			e.collect(n.Body, file, false)
		}
	}
}

var (
	reDollarEsc = regexp.MustCompile(`\$\{\s*'\$'\s*\}`)
	reTmplBrace = regexp.MustCompile(`\$\{\s*([A-Za-z_][\w.]*)\s*\}`)
	reTmplBare  = regexp.MustCompile(`\$([A-Za-z_]\w*)`)
	reIdTmpl    = regexp.MustCompile(`^\$\{\s*([A-Za-z_][\w.]*?)\.id\b[^}]*\}$`)
	reRuntime   = regexp.MustCompile(`\$\{[^}]*\}|\$[A-Za-z_]\w*`)
)

func (e *extractor) lookupVar(name string) (string, bool) {
	for i := len(e.subst) - 1; i >= 0; i-- {
		if v, ok := e.subst[i][name]; ok {
			return v, true
		}
	}
	v, ok := e.consts[kts.LastName(name)]
	return v, ok
}

// Expand resolves Kotlin string templates using known constants and function arguments.
// Unknown templates are kept verbatim. Kotlin-escaped dollars become plain '$'.
func (e *extractor) expand(s string) string {
	const dollar = "\x00"
	s = reDollarEsc.ReplaceAllString(s, dollar)
	s = strings.ReplaceAll(s, `\$`, dollar)
	s = reTmplBrace.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := e.lookupVar(reTmplBrace.FindStringSubmatch(m)[1]); ok {
			return v
		}
		return m
	})
	s = reTmplBare.ReplaceAllStringFunc(s, func(m string) string {
		if v, ok := e.lookupVar(m[1:]); ok {
			return v
		}
		return m
	})
	return strings.ReplaceAll(s, dollar, "$")
}

// str evaluates an expression to a string when it is (a concatenation of) literals/constants.
func (e *extractor) str(n *kts.Node) string {
	if n == nil {
		return ""
	}
	switch n.Kind {
	case kts.NStr:
		return e.expand(n.Str)
	case kts.NNum:
		return n.Str
	case kts.NRef, kts.NMember:
		if v, ok := e.lookupVar(n.Path()); ok {
			return v
		}
		if n.Kind == kts.NMember && (n.Name == "trimIndent" || n.Name == "trimMargin") {
			return e.str(n.Recv)
		}
		return n.Path()
	case kts.NBinary:
		if n.Op == "+" {
			return e.str(n.Value) + e.str(n.Right)
		}
		if n.Op == "?:" {
			return e.str(n.Value)
		}
	case kts.NCall:
		switch kts.LastName(n.CallName()) {
		case "trimIndent", "trimMargin", "trim", "toString":
			if n.Recv.Kind == kts.NMember {
				s := e.str(n.Recv.Recv)
				if n.Recv.Name == "trimIndent" {
					s = trimIndent(s)
				}
				return s
			}
		case "RelativeId", "AbsoluteId", "id":
			if len(n.Args) > 0 {
				return e.str(n.Args[0])
			}
		}
	}
	return ""
}

func trimIndent(s string) string {
	lines := strings.Split(s, "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	min := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		ind := len(l) - len(strings.TrimLeft(l, " \t"))
		if min < 0 || ind < min {
			min = ind
		}
	}
	for i, l := range lines {
		if len(l) >= min && min > 0 {
			lines[i] = l[min:]
		}
	}
	return strings.Join(lines, "\n")
}

// resolveSuper follows a supertype call (BuildType({...}), or a user class that
// eventually extends BuildType/Template/Project) and returns the DSL kind, the
// statements making up the body, and constructor-argument substitutions.
func (e *extractor) resolveSuper(sup *kts.Node, depth int) (string, []*kts.Node, map[string]string) {
	name := kts.LastName(sup.CallName())
	if name == "" {
		name = kts.LastName(sup.Path())
	}
	var lambdas []*kts.Node
	for _, a := range sup.Args {
		if a.Kind == kts.NLambda {
			lambdas = append(lambdas, a.Body...)
		} else if a.Kind == kts.NAssign && a.Value != nil && a.Value.Kind == kts.NLambda {
			lambdas = append(lambdas, a.Value.Body...) // init = { ... }
		}
	}
	if sup.Value != nil {
		lambdas = append(lambdas, sup.Value.Body...)
	}
	switch name {
	case "BuildType", "Template", "Project":
		return name, lambdas, map[string]string{}
	}
	cls, ok := e.objs[name]
	if !ok || cls.node.Op != "class" || depth > 6 {
		return "", nil, nil
	}
	for _, s := range cls.node.Supers {
		kind, body, sub := e.resolveSuper(s, depth+1)
		if kind == "" {
			continue
		}
		for i, p := range cls.node.Params {
			if i < len(sup.Args) && sup.Args[i].Kind != kts.NLambda && sup.Args[i].Kind != kts.NAssign {
				if v := e.str(sup.Args[i]); v != "" {
					sub[p] = v
				}
			}
		}
		for _, a := range sup.Args {
			if a.Kind == kts.NAssign && a.Value != nil && a.Value.Kind != kts.NLambda {
				if v := e.str(a.Value); v != "" {
					sub[a.Name] = v
				}
			}
		}
		body = append(append(body, initBodies(cls.node.Body)...), lambdas...)
		return kind, body, sub
	}
	return "", nil, nil
}

func (e *extractor) superOf(obj *kts.Node) (string, []*kts.Node, map[string]string) {
	for _, s := range obj.Supers {
		if kind, body, sub := e.resolveSuper(s, 0); kind != "" {
			return kind, append(body, initBodies(obj.Body)...), sub
		}
	}
	return "", nil, nil
}

// walk visits statements looking for DSL constructs. project is the enclosing project name.
func (e *extractor) walk(nodes []*kts.Node, project string) {
	for _, n := range nodes {
		e.visit(n, project)
	}
}

func (e *extractor) visit(n *kts.Node, project string) {
	if n == nil {
		return
	}
	switch n.Kind {
	case kts.NObject:
		kind, body, sub := e.superOf(n)
		if n.Op != "object" {
			if kind == "" {
				// a helper class that may hold builds as properties: analyse once, as declared
				e.walk(n.Body, project)
			}
			return // BuildType/Project subclasses are analysed when instantiated
		}
		e.subst = append(e.subst, sub)
		defer func() { e.subst = e.subst[:len(e.subst)-1] }()
		switch kind {
		case "BuildType", "Template":
			e.defineBuild(n.Name, body, kind == "Template", project, n.Line)
		case "Project":
			e.defineProject(n.Name, body)
		default:
			e.walk(n.Body, project)
		}
	case kts.NFun:
		// function bodies are only analysed when called (see call), with arguments substituted
	case kts.NVal:
		if b := e.buildExpr(n.Value, project, n.Name); b == nil {
			e.visit(n.Value, project)
		}
	case kts.NAssign:
		e.visit(n.Value, project)
	case kts.NBinary:
		e.visit(n.Value, project)
		e.visit(n.Right, project)
	case kts.NBlock:
		e.walk(n.Args, project)
		e.walk(n.Body, project)
		e.visit(n.Right, project)
	case kts.NLambda, kts.NRaw:
		e.walk(n.Body, project)
	case kts.NCall:
		e.call(n, project)
	}
}

func initBodies(body []*kts.Node) []*kts.Node {
	var out []*kts.Node
	for _, s := range body {
		if s.Kind == kts.NBlock && s.Name == "init" {
			out = append(out, s.Body...)
		}
	}
	return out
}

func (e *extractor) call(n *kts.Node, project string) {
	full := n.CallName()
	name := kts.LastName(full)
	lam := n.Lambda()
	switch {
	case full == "project" && lam != nil:
		e.projectBody(lam.Body, "")
		return
	case (name == "BuildType" || name == "Template" || name == "Project" || e.funs[name] != nil || e.isClass(name)) && full == name:
		if e.buildExpr(n, project, "") != nil {
			return
		}
	case name == "dependsOn" && n.Recv.Kind == kts.NMember && len(n.Args) > 0:
		// X.dependsOn(Y)
		if target := e.refOf(n.Recv.Recv); target != nil {
			for _, a := range n.Args {
				if r := e.refOf(a); r != nil {
					e.pending = append(e.pending, pendingDep{target: *target, dep: Dep{Ref: *r}, file: e.file, line: n.Line})
				}
			}
			return
		}
	case name == "sequential" && lam != nil:
		e.sequence(lam.Body, nil, project, false)
		return
	case full == "changeBuildType" || full == "changeProject" || full == "changeTemplate":
		e.warn(n.Line, "patch %s(%s) not applied: commit the patch into the DSL for accurate results", full, e.str(n.Args0()))
		return
	}
	e.visit(n.Recv, project)
	e.walk(n.Args, project)
	if n.Value != nil {
		e.visit(n.Value, project)
	}
}

// buildExpr handles expressions that create a build: BuildType{...}, BuildType({...}),
// calls to helper functions returning one. Returns the created build (or nil).
func (e *extractor) buildExpr(n *kts.Node, project, valName string) *Build {
	if n == nil || n.Kind != kts.NCall {
		return nil
	}
	full := n.CallName()
	name := kts.LastName(full)
	if full != name {
		return nil
	}
	switch name {
	case "BuildType", "Template", "buildType", "template":
		if lam := n.Lambda(); lam != nil && (len(n.Args) == 0 || name[0] == 'B' || name[0] == 'T') {
			return e.defineBuild(valName, lam.Body, name == "Template" || name == "template", project, n.Line)
		}
	case "Project":
		if lam := n.Lambda(); lam != nil {
			e.defineProject(valName, lam.Body)
		}
	default:
		if e.isClass(name) && e.depth <= 8 {
			kind, body, sub := e.resolveSuper(n, 0)
			e.subst = append(e.subst, sub)
			e.depth++
			defer func() { e.subst = e.subst[:len(e.subst)-1]; e.depth-- }()
			switch kind {
			case "BuildType", "Template":
				return e.defineBuild("", body, kind == "Template", project, e.objs[name].node.Line)
			case "Project":
				e.defineProject(name, body)
			}
			return nil
		}
		fn := e.funs[name]
		if fn == nil || e.depth > 8 {
			return nil
		}
		sub := map[string]string{}
		for i, p := range fn.Params {
			if i < len(n.Args) {
				a := n.Args[i]
				if a.Kind == kts.NAssign {
					sub[a.Name] = e.str(a.Value)
				} else {
					sub[p] = e.str(a)
				}
			}
		}
		e.subst = append(e.subst, sub)
		e.depth++
		defer func() { e.subst = e.subst[:len(e.subst)-1]; e.depth-- }()
		var b *Build
		if fn.Value != nil {
			b = e.buildExpr(fn.Value, project, valName)
			if b == nil {
				e.visit(fn.Value, project)
			}
		}
		for _, s := range fn.Body {
			if s.Kind == kts.NRef && s.Name == "return" {
				continue
			}
			if c := e.buildExpr(s, project, valName); c != nil {
				b = c
				continue
			}
			e.visit(s, project)
		}
		if b == nil && fn.Value == nil && len(fn.Body) > 0 {
			return nil
		}
		return b
	}
	return nil
}

func (e *extractor) defineProject(objName string, body []*kts.Node) {
	name := objName
	for _, s := range body {
		if s.Kind == kts.NAssign && s.Name == "name" {
			name = e.str(s.Value)
		}
	}
	e.projectBody(body, name)
}

func (e *extractor) projectBody(body []*kts.Node, project string) {
	for _, s := range body {
		if s.Kind != kts.NCall {
			e.visit(s, project)
			continue
		}
		name := s.CallName()
		switch name {
		case "buildType", "template":
			if lam := s.Lambda(); lam != nil && len(s.Args) == 0 {
				e.defineBuild("", lam.Body, name == "template", project, s.Line)
				continue
			}
			for _, a := range s.Args {
				e.register(a, project)
			}
		case "subProject":
			for _, a := range s.Args {
				if a.Kind == kts.NCall && kts.LastName(a.CallName()) == "Project" {
					e.buildExpr(a, project, "")
				} else if a.Kind == kts.NRef || a.Kind == kts.NMember {
					// object Project definitions are visited on their own
				} else {
					e.visit(a, project)
				}
			}
			if lam := s.Lambda(); lam != nil && len(s.Args) == 0 {
				e.defineProject("", lam.Body)
			}
		case "sequential":
			if lam := s.Lambda(); lam != nil {
				e.sequence(lam.Body, nil, project, true)
			}
		default:
			e.visit(s, project)
		}
	}
}

// register records that the build referenced by expression a belongs to project.
func (e *extractor) register(a *kts.Node, project string) {
	if b := e.buildExpr(a, project, ""); b != nil {
		return
	}
	if r := e.refOf(a); r != nil && r.Kind == RefObject {
		if _, ok := e.reg[r.Value]; !ok {
			e.reg[r.Value] = project
		}
	}
}

// sequence handles sequential { buildType(A); parallel { ... } } blocks: each item
// gets snapshot dependencies on the previous stage. Returns the last stage.
func (e *extractor) sequence(body []*kts.Node, prev []Ref, project string, register bool) []Ref {
	for _, s := range body {
		if s.Kind != kts.NCall {
			continue
		}
		switch s.CallName() {
		case "buildType":
			var cur []Ref
			for _, a := range s.Args {
				if a.Kind == kts.NAssign || a.Kind == kts.NLambda {
					continue
				}
				var r *Ref
				if b := e.buildExpr(a, project, ""); b != nil {
					r = &Ref{Kind: RefRelative, Value: b.Key}
				} else {
					r = e.refOf(a)
				}
				if r == nil {
					continue
				}
				if register && r.Kind == RefObject {
					e.reg[r.Value] = project
				}
				e.addSeqDeps(*r, prev, s.Line)
				cur = append(cur, *r)
			}
			if lam := s.Lambda(); lam != nil && len(s.Args) == 0 {
				b := e.defineBuild("", lam.Body, false, project, s.Line)
				r := Ref{Kind: RefRelative, Value: b.Key}
				e.addSeqDeps(r, prev, s.Line)
				cur = append(cur, r)
			}
			if cur != nil {
				prev = cur
			}
		case "parallel":
			if lam := s.Lambda(); lam != nil {
				var tails []Ref
				for _, item := range lam.Body {
					if item.Kind == kts.NCall && item.CallName() == "sequential" && item.Lambda() != nil {
						tails = append(tails, e.sequence(item.Lambda().Body, prev, project, register)...)
					} else {
						tails = append(tails, e.sequence([]*kts.Node{item}, prev, project, register)...)
					}
				}
				if tails != nil {
					prev = tails
				}
			}
		case "sequential":
			if lam := s.Lambda(); lam != nil {
				prev = e.sequence(lam.Body, prev, project, register)
			}
		}
	}
	return prev
}

// addSeqDeps defers the dependency until all builds are known.
func (e *extractor) addSeqDeps(r Ref, prev []Ref, line int) {
	for _, p := range prev {
		e.pending = append(e.pending, pendingDep{target: r, dep: Dep{Ref: p}, file: e.file, line: line})
	}
}

type pendingDep struct {
	target Ref
	dep    Dep
	file   string
	line   int
}

func (e *extractor) isClass(name string) bool {
	o, ok := e.objs[name]
	return ok && o.node.Op == "class"
}

// refOf converts an expression to a reference to a build configuration.
func (e *extractor) refOf(n *kts.Node) *Ref {
	if n == nil || e.depth > 16 {
		return nil
	}
	switch n.Kind {
	case kts.NRef:
		for i := len(e.refs) - 1; i >= 0; i-- {
			if arg, ok := e.refs[i][n.Name]; ok {
				saved := e.refs
				e.refs = e.refs[:i] // evaluate the argument in its caller's scope
				e.depth++
				r := e.refOf(arg)
				e.depth--
				e.refs = saved
				return r
			}
		}
		if v, ok := e.vals[n.Name]; ok && e.s.byObj[n.Name] == nil {
			switch {
			case v.Kind == kts.NStr:
				return &Ref{Kind: RefString, Value: e.str(v), Line: n.Line}
			case v.Kind == kts.NCall && (kts.LastName(v.CallName()) == "AbsoluteId" || kts.LastName(v.CallName()) == "RelativeId"):
				return e.refOf(v) // val id = AbsoluteId("...")
			}
		}
		return &Ref{Kind: RefObject, Value: n.Name, Line: n.Line}
	case kts.NBinary:
		if n.Op == "?:" { // X.id?.value ?: error("...")
			return e.refOf(n.Value)
		}
		return nil
	case kts.NMember:
		switch n.Name {
		case "id", "externalId", "value", "absoluteId", "relativeId":
			return e.refOf(n.Recv)
		}
		return &Ref{Kind: RefObject, Value: n.Name, Line: n.Line}
	case kts.NStr:
		if m := reIdTmpl.FindStringSubmatch(strings.TrimSpace(n.Str)); m != nil {
			return &Ref{Kind: RefObject, Value: kts.LastName(m[1]), Line: n.Line}
		}
		s := e.expand(n.Str)
		if s == "" {
			return nil
		}
		return &Ref{Kind: RefString, Value: s, Line: n.Line}
	case kts.NCall:
		switch kts.LastName(n.CallName()) {
		case "RelativeId", "AbsoluteId":
			v := e.str(n.Args0())
			if v == "" || n.Args0() == nil || n.Args0().Kind != kts.NStr && n.Args0().Kind != kts.NRef {
				return nil // computed id, e.g. RelativeId(Foo.idFor(config))
			}
			if kts.LastName(n.CallName()) == "RelativeId" {
				return &Ref{Kind: RefRelative, Value: v, Line: n.Line}
			}
			return &Ref{Kind: RefAbsolute, Value: v, Line: n.Line}
		case "toString":
			if n.Recv.Kind == kts.NMember {
				return e.refOf(n.Recv.Recv)
			}
		}
		if b := e.buildExpr(n, "", ""); b != nil {
			return &Ref{Kind: RefRelative, Value: b.Key, Line: n.Line}
		}
	}
	return nil
}

// inline expands a call to a user-defined helper function (e.g. an extension
// "fun Dependencies.dependsOnTemplate(b: BuildType) { dependency(b) {...} }")
// by handing its body to handle with the arguments bound.
func (e *extractor) inline(s *kts.Node, handle func([]*kts.Node)) bool {
	if s.Kind != kts.NCall || e.depth > 8 {
		return false
	}
	fn := e.funs[kts.LastName(s.CallName())]
	if fn == nil {
		return false
	}
	sub, refs := map[string]string{}, map[string]*kts.Node{}
	pos := 0
	for _, a := range s.Args {
		name, val := "", a
		if a.Kind == kts.NAssign {
			name, val = a.Name, a.Value
		} else if pos < len(fn.Params) {
			name = fn.Params[pos]
			pos++
		}
		if name == "" || val == nil {
			continue
		}
		refs[name] = val
		if v := e.str(val); v != "" && (val.Kind == kts.NStr || val.Kind == kts.NBinary || val.Kind == kts.NNum) {
			sub[name] = v
		}
	}
	body := fn.Body
	if fn.Value != nil {
		body = []*kts.Node{fn.Value}
	}
	e.subst = append(e.subst, sub)
	e.refs = append(e.refs, refs)
	e.depth++
	handle(body)
	e.depth--
	e.subst = e.subst[:len(e.subst)-1]
	e.refs = e.refs[:len(e.refs)-1]
	return true
}

var reNonID = regexp.MustCompile(`[^A-Za-z0-9_]+`)

// defineBuild extracts a build configuration body.
func (e *extractor) defineBuild(objName string, body []*kts.Node, template bool, project string, line int) *Build {
	b := &Build{ObjName: objName, Project: project, Template: template, File: e.file, Line: line, Params: map[string]string{}}
	for _, s := range body {
		e.buildStmt(b, s)
	}
	switch {
	case b.Key != "":
	case objName != "":
		b.Key = objName
	case b.Name != "":
		b.Key = reNonID.ReplaceAllString(b.Name, "_")
	default:
		b.Key = fmt.Sprintf("anonymous_%s_%d", reNonID.ReplaceAllString(e.file, "_"), line)
	}
	if b.Name == "" {
		b.Name = b.Key
	}
	if m := reRuntime.FindString(b.Key + " " + b.Name); m != "" && !template {
		e.warn(line, "build %q: id/name depends on runtime value %s; shown once, as written", b.Name, m)
	}
	if template {
		e.s.templates[b.Key] = b
		if objName != "" {
			e.s.templates[objName] = b
		}
		return b
	}
	if old := e.s.byKey[b.Key]; old != nil {
		if old.File == b.File && old.Line == b.Line {
			return old // same declaration reached twice (e.g. registered and used in sequential)
		}
		e.warn(line, "duplicate build configuration id %q (also %s:%d); renamed", b.Key, old.File, old.Line)
		for i := 2; ; i++ {
			k := fmt.Sprintf("%s_%d", b.Key, i)
			if e.s.byKey[k] == nil {
				b.Key = k
				break
			}
		}
	}
	e.s.byKey[b.Key] = b
	if objName != "" {
		e.s.byObj[objName] = b
	}
	e.s.Builds = append(e.s.Builds, b)
	return b
}

func (e *extractor) buildStmt(b *Build, s *kts.Node) {
	switch s.Kind {
	case kts.NAssign:
		v := e.str(s.Value)
		switch s.Name {
		case "id":
			if r := e.refOf(s.Value); r != nil {
				b.Key = r.Value
			}
		case "name":
			b.Name = v
		case "artifactRules":
			b.ArtifactRules = v
		case "type":
			if strings.Contains(s.Value.Path(), "COMPOSITE") {
				b.Composite = true
			}
		}
	case kts.NCall:
		lam := s.Lambda()
		switch s.CallName() {
		case "id":
			if a := s.Args0(); a != nil {
				if r := e.refOf(a); r != nil {
					b.Key = r.Value
				}
			}
		case "templates":
			for _, a := range s.Args {
				if r := e.refOf(a); r != nil {
					b.Templates = append(b.Templates, *r)
				}
			}
		case "params":
			if lam != nil {
				e.params(b.Params, lam.Body)
			}
		case "steps":
			if lam != nil {
				e.steps(b, lam.Body)
			}
		case "dependencies":
			if lam != nil {
				e.deps(b, lam.Body)
			}
		case "dependsOn":
			for _, a := range s.Args {
				if r := e.refOf(a); r != nil {
					b.Deps = append(b.Deps, Dep{Ref: *r})
				}
			}
		case "triggers":
			if lam != nil {
				e.triggers(b, lam.Body)
			}
		case "type":
			if strings.Contains(e.str(s.Args0()), "COMPOSITE") {
				b.Composite = true
			}
		default:
			// helper functions (fun BuildType.common() {...}); vcs, features, requirements... are not part of the graph
			e.inline(s, func(body []*kts.Node) {
				for _, x := range body {
					e.buildStmt(b, x)
				}
			})
		}
	case kts.NBlock: // if (...) { steps {...} }
		for _, x := range s.Body {
			e.buildStmt(b, x)
		}
		if s.Right != nil {
			e.buildStmt(b, s.Right)
		}
	}
}

// redacted stands in for the value of a password parameter.
const redacted = "******"

func (e *extractor) params(m map[string]string, body []*kts.Node) {
	for _, s := range body {
		if s.Kind != kts.NCall {
			continue
		}
		switch s.CallName() {
		case "param", "text", "select", "checkbox":
			if len(s.Args) >= 2 {
				m[e.str(s.Args[0])] = e.str(s.Args[1])
			}
		case "password":
			// The value is a secret, or a reference to one. It never goes into the graph, and
			// so never into the scripts it is substituted into either.
			if len(s.Args) >= 2 {
				m[e.str(s.Args[0])] = redacted
			}
		}
	}
}

// stepFields flattens all assignments (and param("k","v") calls) within a step.
func (e *extractor) stepFields(body []*kts.Node, f map[string]string, calls *[]string) {
	for _, s := range body {
		switch s.Kind {
		case kts.NAssign:
			if v := e.str(s.Value); v != "" {
				f[kts.LastName(s.Name)] = v
			}
			if lam := s.Value.Lambda(); lam != nil {
				*calls = append(*calls, kts.LastName(s.Value.CallName()))
				e.stepFields(lam.Body, f, calls)
			}
		case kts.NCall:
			if s.CallName() == "param" && len(s.Args) >= 2 {
				f[e.str(s.Args[0])] = e.str(s.Args[1])
			}
			if lam := s.Lambda(); lam != nil {
				*calls = append(*calls, kts.LastName(s.CallName()))
				e.stepFields(lam.Body, f, calls)
			}
		}
	}
}

func (e *extractor) steps(b *Build, body []*kts.Node) {
	for _, s := range body {
		if s.Kind == kts.NBlock {
			e.steps(b, append(append([]*kts.Node{}, s.Body...), nilSafe(s.Right)...))
			continue
		}
		if s.Kind != kts.NCall {
			continue
		}
		if e.inline(s, func(body []*kts.Node) { e.steps(b, body) }) {
			continue
		}
		typ := kts.LastName(s.CallName())
		if s.Recv != nil && s.Recv.Kind == kts.NMember && s.Recv.Recv.Kind == kts.NCall {
			// helper()?.also { ... } / script { ... }.apply { ... }: the step is the receiver
			e.steps(b, []*kts.Node{s.Recv.Recv})
			continue
		}
		if typ == "insert" || typ == "items" || typ == "add" { // patches: insert(0) { script {...} }
			if lam := s.Lambda(); lam != nil {
				e.steps(b, lam.Body)
			}
			continue
		}
		if scopeFuncs[typ] || s.Recv == nil || s.Recv.Kind != kts.NRef {
			continue
		}
		if t, ok := strings.CutSuffix(typ, "BuildStep"); ok && t != "" { // ScriptBuildStep { ... }
			typ = strings.ToLower(t[:1]) + t[1:]
		}
		lam := s.Lambda()
		if lam == nil {
			continue
		}
		f := map[string]string{}
		var calls []string
		e.stepFields(lam.Body, f, &calls)
		st := model.Step{Name: f["name"], Type: typ, WorkDir: f["workingDir"]}
		switch typ {
		case "script", "nodeJS", "powerShell", "python", "kotlinScript", "step":
			st.Script = first(f, "scriptContent", "content", "shellScript", "script.content", "scriptBody", "command")
		case "exec":
			st.Script = strings.TrimSpace(f["path"] + " " + f["arguments"])
		case "dockerCommand":
			st.Script = dockerScript(f, calls)
		case "gradle":
			st.Script = strings.TrimSpace("gradle " + f["tasks"] + " " + f["gradleParams"])
		case "maven":
			st.Script = strings.TrimSpace("mvn " + f["goals"] + " " + f["runnerArgs"])
		case "dockerCompose":
			st.Script = "docker compose -f " + first(f, "file", "path")
		default:
			st.Script = first(f, "scriptContent", "content", "command", "arguments")
		}
		st.Summary = summarize(st)
		b.Steps = append(b.Steps, st)
	}
}

// Kotlin standard functions that take a lambda but are never build runners.
var scopeFuncs = map[string]bool{
	"also": true, "apply": true, "let": true, "run": true, "with": true, "takeIf": true, "takeUnless": true,
	"forEach": true, "forEachIndexed": true, "map": true, "filter": true, "repeat": true, "onEach": true,
	"listOf": true, "setOf": true, "mapOf": true, "require": true, "check": true, "error": true, "println": true,
}

func nilSafe(n *kts.Node) []*kts.Node {
	if n == nil {
		return nil
	}
	return []*kts.Node{n}
}

func first(f map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := f[k]; v != "" {
			return v
		}
	}
	return ""
}

// dockerScript turns a dockerCommand runner into equivalent shell so images are discovered uniformly.
func dockerScript(f map[string]string, calls []string) string {
	tags := strings.Fields(f["namesAndTags"])
	kind := "build"
	for _, c := range calls {
		if c == "push" || c == "build" || c == "other" {
			kind = c
		}
	}
	switch kind {
	case "build":
		return shell.BuildCommand(f["path"], tags, nil, f["contextDir"])
	case "push":
		return strings.Join(shell.PushCommands(tags), "\n")
	}
	return strings.TrimSpace("docker " + f["subCommand"] + " " + f["commandArgs"])
}

// summarize is a step's first meaningful line, or its type when there is none.
func summarize(st model.Step) string {
	if s := shell.Summary(st.Script); s != "" {
		return s
	}
	return st.Type
}

func (e *extractor) deps(b *Build, body []*kts.Node) {
	for _, s := range body {
		if s.Kind != kts.NCall {
			continue
		}
		name := s.CallName()
		if name != "snapshot" && name != "artifacts" && name != "dependency" {
			e.inline(s, func(body []*kts.Node) { e.deps(b, body) })
			continue
		}
		var r *Ref
		for _, a := range s.Args {
			if a.Kind != kts.NLambda && a.Kind != kts.NAssign {
				r = e.refOf(a)
				break
			}
		}
		if r == nil {
			e.warn(s.Line, "%s dependency with unrecognised target", name)
			continue
		}
		d := Dep{Ref: *r, Artifacts: name == "artifacts"}
		if lam := s.Lambda(); lam != nil {
			for _, x := range lam.Body {
				switch {
				case x.Kind == kts.NAssign && x.Name == "artifactRules":
					d.Rules = e.str(x.Value)
				case x.Kind == kts.NCall && x.CallName() == "artifacts":
					d.Artifacts = true
					if l := x.Lambda(); l != nil {
						for _, y := range l.Body {
							if y.Kind == kts.NAssign && y.Name == "artifactRules" {
								d.Rules = e.str(y.Value)
							}
						}
					}
				}
			}
		}
		b.Deps = append(b.Deps, d)
	}
}

func (e *extractor) triggers(b *Build, body []*kts.Node) {
	for _, s := range body {
		if s.Kind != kts.NCall {
			continue
		}
		if e.inline(s, func(body []*kts.Node) { e.triggers(b, body) }) {
			continue
		}
		typ := kts.LastName(s.CallName())
		lam := s.Lambda()
		if lam == nil {
			b.Triggers = append(b.Triggers, TriggerDef{Trigger: model.Trigger{Type: typ}})
			continue
		}
		t := TriggerDef{Trigger: model.Trigger{Type: typ}}
		f := map[string]string{}
		var calls []string
		e.stepFields(lam.Body, f, &calls)
		switch typ {
		case "vcs":
			t.Detail = joinLines(first(f, "branchFilter", "triggerRules"))
		case "schedule":
			var parts []string
			for _, c := range calls {
				if c == "daily" || c == "weekly" || c == "cron" {
					parts = append(parts, c)
				}
			}
			for _, k := range []string{"dayOfWeek", "hour", "minute", "hours", "minutes", "dayOfMonth", "month", "seconds"} {
				if v, ok := f[k]; ok {
					parts = append(parts, k+"="+kts.LastName(v)) // ScheduleTrigger.DAY.Tuesday -> Tuesday
				}
			}
			t.Detail = strings.Join(parts, " ")
		case "finishBuildTrigger":
			for _, x := range lam.Body {
				if x.Kind == kts.NAssign && x.Name == "buildType" {
					t.Ref = e.refOf(x.Value)
				}
			}
			if f["successfulOnly"] == "true" {
				t.Detail = "on success"
			}
		}
		b.Triggers = append(b.Triggers, t)
	}
}

// joinLines trims every line and joins the non-empty ones with a space.
func joinLines(s string) string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, " ")
}

// finish applies deferred dependencies, project registration and template inheritance.
func (e *extractor) finish() {
	for _, p := range e.pending {
		if b := e.s.Lookup(p.target); b != nil {
			b.Deps = append(b.Deps, p.dep)
		} else {
			// e.g. sequential { buildType(AbsoluteId("Other_Build")) }: resolved across repos later
			e.s.CrossDeps = append(e.s.CrossDeps, CrossDep{Target: p.target, Dep: p.dep, File: p.file, Line: p.line})
		}
	}
	for obj, project := range e.reg {
		if b := e.s.byObj[obj]; b != nil && b.Project == "" {
			b.Project = project
		}
	}
	for _, b := range e.s.Builds {
		for _, tr := range b.Templates {
			t := e.s.templates[tr.Value]
			if t == nil {
				e.s.Warnings = append(e.s.Warnings, fmt.Sprintf("%s:%d: unknown template %s", b.File, b.Line, tr.Value))
				continue
			}
			b.Steps = append(append([]model.Step{}, t.Steps...), b.Steps...)
			b.Deps = append(b.Deps, t.Deps...)
			b.Triggers = append(b.Triggers, t.Triggers...)
			if b.ArtifactRules == "" {
				b.ArtifactRules = t.ArtifactRules
			}
			for k, v := range t.Params {
				if _, ok := b.Params[k]; !ok {
					b.Params[k] = v
				}
			}
		}
	}
	sort.Slice(e.s.Builds, func(i, j int) bool { return e.s.Builds[i].Key < e.s.Builds[j].Key })
}
