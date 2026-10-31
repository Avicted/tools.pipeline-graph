// Package teamcity is the ci.Provider for TeamCity Kotlin DSL settings.
package teamcity

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
)

const defaultDir = ".teamcity"

type Provider struct{}

func New() Provider { return Provider{} }

func (Provider) System() ci.System {
	return ci.System{Name: "teamcity", Title: "TeamCity", Job: "build configuration", ID: "TeamCity id"}
}

// Settings are the `teamcity:` block of the config, globally and per repo.
type Settings struct {
	Dir       string `yaml:"dir"`        // where the Kotlin DSL lives, relative to the repo; default .teamcity
	ProjectID string `yaml:"project_id"` // TeamCity project the settings are imported into; default derived from the repo name
}

func (Provider) NewSettings() any { return &Settings{} }

func settingsOf(r ci.Repo) Settings {
	if s, ok := r.Settings.(*Settings); ok && s != nil {
		return *s
	}
	return Settings{}
}

func dir(r ci.Repo) string {
	if d := settingsOf(r).Dir; d != "" {
		return d
	}
	return defaultDir
}

func (Provider) Detect(r ci.Repo) bool {
	st, err := os.Stat(filepath.Join(r.Root, dir(r)))
	return err == nil && st.IsDir()
}

func (Provider) Load(r ci.Repo) (*ci.Pipelines, error) {
	d := dir(r)
	if _, err := os.Stat(filepath.Join(r.Root, d)); err != nil {
		return nil, fmt.Errorf("no TeamCity settings directory %s", d)
	}
	s, err := ParseDir(filepath.Join(r.Root, d), r.Root)
	if err != nil {
		return nil, fmt.Errorf("reading TeamCity settings: %w", err)
	}
	projectID := settingsOf(r).ProjectID
	if projectID == "" {
		projectID = DeriveProjectID(r.Name)
	}
	p := &ci.Pipelines{Warnings: s.Warnings, IDSep: "_"}
	warn := func(file string, line int, format string, args ...any) {
		p.Warnings = append(p.Warnings, fmt.Sprintf("%s:%d: %s", file, line, fmt.Sprintf(format, args...)))
	}
	// ref converts a DSL reference; ok is false when it can never resolve.
	ref := func(x Ref, file string) (ci.Ref, bool) {
		out := ci.Ref{Global: x.Value, Line: x.Line}
		switch x.Kind {
		case RefObject, RefRelative:
			if b := s.Lookup(x); b != nil {
				return ci.Ref{Local: b.Key, Line: x.Line}, true
			}
			if x.Kind == RefObject {
				warn(file, x.Line, "reference to unknown build configuration %s", x.Value)
				return out, false
			}
		default:
			// "Build" inside a string may be a relative id, not an absolute one
			if b := s.Lookup(Ref{Kind: RefRelative, Value: x.Value}); b != nil {
				out.Alias = b.Key
			}
		}
		return out, true
	}
	dep := func(d Dep, file string) (ci.Dep, bool) {
		r, ok := ref(d.Ref, file)
		return ci.Dep{Ref: r, Artifacts: d.Artifacts, Rules: d.Rules}, ok
	}
	for _, b := range s.Builds {
		j := &ci.Job{
			Key: b.Key, QualID: projectID + "_" + b.Key, Name: b.Name, Project: b.Project,
			Composite: b.Composite, Steps: b.Steps, File: b.File, Line: b.Line,
			Expand: func(t string) string { return substParams(t, b.Params) },
		}
		if len(b.Params) > 0 {
			j.Params = b.Params
		}
		for _, l := range strings.Split(b.ArtifactRules, "\n") {
			if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "-:") {
				j.Artifacts = append(j.Artifacts, strings.TrimPrefix(l, "+:"))
			}
		}
		for _, d := range b.Deps {
			if cd, ok := dep(d, b.File); ok {
				j.Deps = append(j.Deps, cd)
			}
		}
		for _, t := range b.Triggers {
			ct := ci.Trigger{Trigger: t.Trigger}
			if t.Ref != nil {
				rf, ok := ref(*t.Ref, b.File)
				if !ok {
					continue
				}
				ct.From = &rf
			}
			j.Triggers = append(j.Triggers, ct)
		}
		p.Jobs = append(p.Jobs, j)
	}
	for _, c := range s.CrossDeps {
		t, ok1 := ref(c.Target, c.File)
		d, ok2 := dep(c.Dep, c.File)
		if ok1 && ok2 {
			p.CrossDeps = append(p.CrossDeps, ci.CrossDep{Target: t, Dep: d, File: c.File})
		}
	}
	return p, nil
}

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

var reParam = regexp.MustCompile(`%([A-Za-z0-9_.\-]+)%`)

// substParams expands %param% references from the build's params.
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
