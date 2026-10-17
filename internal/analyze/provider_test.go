package analyze

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
	"github.com/Avicted/tools.pipeline-graph/internal/ci/teamcity"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

// stub is a CI system that is not TeamCity, with each repo's jobs declared in Go.
type stub struct {
	name   string
	marker string                   // a repo uses this system when the file exists
	pipes  map[string]*ci.Pipelines // by repo name
	err    error
}

func (s stub) Name() string { return s.name }
func (s stub) Detect(r ci.Repo) bool {
	_, err := os.Stat(filepath.Join(r.Root, s.marker))
	return err == nil
}
func (s stub) Load(r ci.Repo) (*ci.Pipelines, error) { return s.pipes[r.Name], s.err }

func job(key, qual string) *ci.Job {
	return &ci.Job{Key: key, QualID: qual, Name: key, File: "wf.yml", Line: 3}
}

func TestOtherProviderBuildsTheSameGraph(t *testing.T) {
	release := job("release", "acme/lib#release")

	build := job("build", "acme/app#build")
	build.Steps = []model.Step{{Name: "image", Type: "run", Script: "make image"}}
	test := job("test", "acme/app#test")
	test.Deps = []ci.Dep{
		{Ref: ci.Ref{Local: "build"}},
		{Ref: ci.Ref{Global: "acme/lib#release", Line: 7}, Artifacts: true, Rules: "dist/**"},
		{Ref: ci.Ref{Global: "acme/lib-typo#release", Line: 9}},
	}
	test.Triggers = []ci.Trigger{
		{Trigger: model.Trigger{Type: "push"}},
		{Trigger: model.Trigger{Type: "workflow_run", Detail: "on release"}, From: &ci.Ref{Global: "acme/lib#release"}},
	}
	test.Artifacts = []string{"coverage/**"}
	test.Params = map[string]string{"GOFLAGS": "-race"}

	gh := stub{name: "github", marker: "wf.yml", pipes: map[string]*ci.Pipelines{
		"lib": {Jobs: []*ci.Job{release}},
		"app": {Jobs: []*ci.Job{build, test}, Warnings: []string{"wf.yml:1: odd thing"}},
	}}
	files := map[string]string{"wf.yml": ""}
	lib := repo(t, "lib", files)
	app := repo(t, "app", map[string]string{"wf.yml": "", "Makefile": "image:\n\tdocker build -t reg.io/app .\n"})

	g, warnings := Run([]Repo{lib, app}, Options{Providers: []ci.Provider{gh}})

	want := strings.Join([]string{
		"bt:app/build>bt:app/test:snapshot",
		"bt:app/build>mk:app/Makefile:image:runs",
		"bt:lib/release>bt:app/test:artifact",
		"bt:lib/release>bt:app/test:trigger",
		"ext:acme/lib-typo#release>bt:app/test:snapshot",
		"mk:app/Makefile:image>img:reg.io/app:produces",
	}, "\n")
	if got := edges(g); got != want {
		t.Errorf("edges:\n%s\nwant:\n%s", got, want)
	}
	n := g.Node("bt:app/test")
	if n.Sub != "acme/app#test" || n.Source != "wf.yml:3" || n.Params["GOFLAGS"] != "-race" ||
		strings.Join(n.Artifacts, ",") != "coverage/**" {
		t.Errorf("job details were not carried over: %+v", n)
	}
	var trig []string
	for _, tr := range n.Triggers {
		trig = append(trig, tr.Type+":"+tr.Detail)
	}
	if got := strings.Join(trig, "|"); got != "push:|finishBuild:release" {
		t.Errorf("triggers: %s", got)
	}
	joined := strings.Join(warnings, "\n")
	for _, w := range []string{
		"app: wf.yml:1: odd thing", // provider warnings get the repo
		"app: wf.yml:9: build configuration acme/lib-typo#release not found",
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing warning %q in:\n%s", w, joined)
		}
	}
	if strings.Contains(joined, "by id suffix") {
		t.Error("without IDSep there is no fuzzy matching")
	}
}

func TestExpandAppliesToScripts(t *testing.T) {
	j := job("build", "x#build")
	j.Steps = []model.Step{{Type: "run", Script: "make ${{TARGET}}", WorkDir: "${{DIR}}"}}
	j.Expand = func(s string) string {
		return strings.NewReplacer("${{TARGET}}", "image", "${{DIR}}", "svc").Replace(s)
	}
	plain := job("plain", "x#plain")
	plain.Steps = []model.Step{{Type: "run", Script: "make image"}} // nil Expand: text as written
	p := stub{name: "gh", marker: "wf.yml", pipes: map[string]*ci.Pipelines{"app": {Jobs: []*ci.Job{j, plain}}}}
	r := repo(t, "app", map[string]string{"wf.yml": "", "svc/Makefile": "image:\n\t@true\n", "Makefile": "image:\n\t@true\n"})
	g, _ := Run([]Repo{r}, Options{Providers: []ci.Provider{p}})
	want := "bt:app/build>mk:app/svc/Makefile:image:runs\nbt:app/plain>mk:app/Makefile:image:runs"
	if got := edges(g); got != want {
		t.Errorf("edges:\n%s\nwant:\n%s", got, want)
	}
}

func TestRepoWithTwoCISystems(t *testing.T) {
	tcRepo := repo(t, "svc", map[string]string{
		".teamcity/settings.kts": `object Build : BuildType({ name = "TC build" })`,
		"wf.yml":                 "",
	})
	gh := stub{name: "github", marker: "wf.yml", pipes: map[string]*ci.Pipelines{
		"svc": {Jobs: []*ci.Job{job("wf/lint", "acme/svc#lint")}},
	}}
	g, w := Run([]Repo{tcRepo}, Options{Providers: []ci.Provider{teamcity.New(), gh}})
	if g.Node("bt:svc/Build") == nil || g.Node("bt:svc/wf/lint") == nil || len(g.Nodes) != 2 {
		t.Errorf("both systems' jobs must be in the graph: %v warnings %v", ids(g), w)
	}
	// naming one system reads only that one
	g, _ = Run([]Repo{{Name: "svc", Root: tcRepo.Root, Provider: "github"}}, Options{Providers: []ci.Provider{teamcity.New(), gh}})
	if g.Node("bt:svc/Build") != nil || g.Node("bt:svc/wf/lint") == nil {
		t.Errorf("provider: github must skip TeamCity: %v", ids(g))
	}
}

func ids(g *model.Graph) []string {
	var out []string
	for _, n := range g.Nodes {
		out = append(out, n.ID)
	}
	return out
}

func TestProviderProblemsAreWarningsNotFailures(t *testing.T) {
	empty := repo(t, "empty", map[string]string{"README.md": ""})
	ok := repo(t, "ok", map[string]string{".teamcity/settings.kts": `object B : BuildType({ name = "B" })`})
	broken := repo(t, "broken", map[string]string{"wf.yml": ""})
	bad := stub{name: "github", marker: "wf.yml", err: errors.New("cannot parse workflow")}
	unknown := Repo{Name: "weird", Root: ok.Root, Provider: "jenkins"}

	g, warnings := Run([]Repo{empty, ok, broken, unknown}, Options{Providers: []ci.Provider{teamcity.New(), bad}})
	joined := strings.Join(warnings, "\n")
	for _, w := range []string{
		"empty: no pipeline definitions found (looked for: teamcity, github)",
		`broken: github: cannot parse workflow`,
		`weird: unknown provider "jenkins" (known: teamcity, github)`,
	} {
		if !strings.Contains(joined, w) {
			t.Errorf("missing warning %q in:\n%s", w, joined)
		}
	}
	if g.Node("bt:ok/B") == nil {
		t.Error("healthy repos are still analysed")
	}
	if len(g.Repos) != 4 {
		t.Errorf("every repo keeps its lane: %v", g.Repos)
	}
}

func TestExplicitTeamCityDirAndMissingDir(t *testing.T) {
	r := repo(t, "svc", map[string]string{"ci/settings.kts": `object B : BuildType({ name = "B" })`})
	g, _ := Run([]Repo{{Name: "svc", Root: r.Root, Provider: "teamcity", ConfigDir: "ci"}}, Options{Providers: tc})
	if g.Node("bt:svc/B") == nil {
		t.Errorf("ConfigDir must reach the provider: %v", ids(g))
	}
	_, w := Run([]Repo{{Name: "svc", Root: r.Root, Provider: "teamcity"}}, Options{Providers: tc})
	if !strings.Contains(strings.Join(w, "\n"), "svc: teamcity: no TeamCity settings directory .teamcity") {
		t.Errorf("an explicit provider with nothing to read must say so: %v", w)
	}
}

func TestAliasesAnswerForAWholeWorkflow(t *testing.T) {
	last := job("go/test", "lib/.github/workflows/go.yml#test")
	last.Aliases = []string{"lib/.github/workflows/go.yml"}
	steal := job("other/x", "lib/.github/workflows/other.yml#x")
	steal.Aliases = []string{"lib/.github/workflows/go.yml"} // already taken: the first claimant keeps it
	call := job("ci/checks", "app/.github/workflows/ci.yml#checks")
	call.Deps = []ci.Dep{{Ref: ci.Ref{Global: "lib/.github/workflows/go.yml", Line: 4}}}
	gh := stub{name: "github", marker: "wf.yml", pipes: map[string]*ci.Pipelines{
		"lib": {Jobs: []*ci.Job{last, steal}},
		"app": {Jobs: []*ci.Job{call}},
	}}
	g, w := Run([]Repo{repo(t, "lib", map[string]string{"wf.yml": ""}), repo(t, "app", map[string]string{"wf.yml": ""})}, Options{Providers: []ci.Provider{gh}})
	if got := edges(g); got != "bt:lib/go/test>bt:app/ci/checks:snapshot" {
		t.Errorf("edges: %s (warnings %v)", got, w)
	}
}
