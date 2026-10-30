// Package ci is the seam between pipeline-graph and a CI/CD system.
//
// A Provider reads one system's pipeline definitions (TeamCity Kotlin DSL,
// GitHub Actions workflows, ...) from a checkout and describes them as
// neutral Jobs. The analyzer owns everything that is the same for every
// system: cross-repo id resolution, make/docker/script scanning and the graph.
//
// To add a system, implement Provider in a sub-package, and add it to the
// list in cmd/pipeline-graph. Nothing else changes.
package ci

import (
	"fmt"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

// Repo is a checkout handed to a provider.
type Repo struct {
	Name     string // short repo name, the swimlane title
	Root     string // local checkout
	Settings any    // the provider's own settings for this repo, decoded into its NewSettings(); nil without any
}

// Ref points at another job.
type Ref struct {
	Local  string // Key of a job in the same repo; set when the provider already knows it
	Global string // Job.QualID, looked up across all analysed repos
	Alias  string // Key of a job in the same repo that Global also names; used when no QualID matches
	Line   int
}

// Dep is "this job needs that job".
type Dep struct {
	Ref       Ref
	Artifacts bool   // consumes the other job's artifacts, not just ordering
	Rules     string // which artifacts
}

// Trigger is a way a job starts. From is set when it starts on another job finishing.
type Trigger struct {
	model.Trigger
	From *Ref
}

// Job is one unit of a pipeline: a TeamCity build configuration, a GitHub Actions job.
type Job struct {
	Key       string   // unique within the repo
	QualID    string   // unique across repos, what other repos refer to it by
	Aliases   []string // more ids that also name this job, e.g. a whole workflow that is called by its last job; the first claimant wins
	Name      string
	Project   string
	Composite bool // only orchestrates other jobs
	Steps     []model.Step
	Params    map[string]string
	Artifacts []string // published artifact rules
	Triggers  []Trigger
	Deps      []Dep
	File      string
	Line      int
	// Expand substitutes the system's variables (TeamCity %param%, Actions ${{ env.X }})
	// in step scripts and work dirs before they are scanned. nil leaves text as written.
	Expand func(string) string
}

// CrossDep is a dependency declared where its dependent job is not in this repo's own definitions.
type CrossDep struct {
	Target Ref
	Dep    Dep
	File   string
}

// Pipelines is everything a provider found in one repo.
type Pipelines struct {
	Jobs      []*Job
	CrossDeps []CrossDep
	Warnings  []string // "file:line: message"; the analyzer adds the repo
	// IDSep, when set, enables a fuzzy match of unknown Global ids: the id ends
	// in IDSep+Key of a job whose repo name appears in the prefix. "" disables it.
	IDSep string
}

// System is how a provider names itself and its jobs; see model.System.
type System = model.System

// Provider reads the pipelines of one CI/CD system.
type Provider interface {
	System() System
	// Detect reports whether the repo has this system's definitions.
	Detect(r Repo) bool
	// Load parses them. An error is reported as a warning and the repo is skipped.
	Load(r Repo) (*Pipelines, error)
}

// Configurable is a Provider with settings of its own. The config's top-level
// `<name>:` block and then a repo's `<name>:` block are decoded, in that order,
// into the value NewSettings returns; the result is Repo.Settings.
type Configurable interface {
	NewSettings() any
}

// Pick returns the providers to run on a repo: the one called name, or, when
// name is empty, every provider that detects its definitions there.
func Pick(all []Provider, name string, r Repo) ([]Provider, error) {
	var out []Provider
	var names []string
	for _, p := range all {
		names = append(names, p.System().Name)
		switch {
		case name == p.System().Name:
			return []Provider{p}, nil
		case name == "" && p.Detect(r):
			out = append(out, p)
		}
	}
	if name != "" {
		return nil, fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(names, ", "))
	}
	return out, nil
}
