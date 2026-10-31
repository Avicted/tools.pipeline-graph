package teamcity

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
)

func checkout(t *testing.T, files map[string]string) ci.Repo {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return ci.Repo{Name: "service-a", Root: root}
}

func TestDetect(t *testing.T) {
	p := New()
	if p.System().Name != "teamcity" {
		t.Errorf("name %q", p.System().Name)
	}
	if !p.Detect(checkout(t, map[string]string{".teamcity/settings.kts": ""})) {
		t.Error(".teamcity must be detected")
	}
	r := checkout(t, map[string]string{"ci/settings.kts": "", "README.md": ""})
	if p.Detect(r) {
		t.Error("the default directory is absent")
	}
	r.Settings = &Settings{Dir: "ci"}
	if !p.Detect(r) {
		t.Error("a configured directory must be detected")
	}
	// a file named like the directory is not a settings directory
	if p.Detect(checkout(t, map[string]string{".teamcity": "x"})) {
		t.Error("a plain file is not a settings directory")
	}
}

func TestLoadErrors(t *testing.T) {
	_, err := New().Load(checkout(t, map[string]string{"README.md": ""}))
	if err == nil || !strings.Contains(err.Error(), "no TeamCity settings directory .teamcity") {
		t.Errorf("got %v", err)
	}
}

const settings = `
project { buildType(Build); buildType(Deploy) }
object Build : BuildType({
    id("Build")
    name = "Build service"
    params { param("tag", "v1") }
    artifactRules = """
        +:out/** => dist
        -:out/tmp
        report.xml
    """.trimIndent()
    steps { script { scriptContent = "make all TAG=%tag%" } }
    triggers { vcs {} }
})
object Deploy : BuildType({
    name = "Deploy"
    dependencies {
        snapshot(Build) {}
        artifacts(Build) { artifactRules = "dist/**" }
        snapshot(AbsoluteId("Platform_Base")) {}
        snapshot(AbsoluteId("ServiceA_Build")) {}
        snapshot(Missing) {}
    }
    triggers { finishBuildTrigger { buildType = "Platform_Release" } }
})
`

func TestLoad(t *testing.T) {
	r := checkout(t, map[string]string{".teamcity/settings.kts": settings})
	p, err := New().Load(r)
	if err != nil {
		t.Fatal(err)
	}
	if p.IDSep != "_" {
		t.Errorf("IDSep %q", p.IDSep)
	}
	jobs := map[string]*ci.Job{}
	for _, j := range p.Jobs {
		jobs[j.Key] = j
	}
	b := jobs["Build"]
	if b == nil || jobs["Deploy"] == nil || len(p.Jobs) != 2 {
		t.Fatalf("jobs: %v", p.Jobs)
	}
	if b.QualID != "ServiceA_Build" || b.Name != "Build service" {
		t.Errorf("build: %+v", b)
	}
	if got := strings.Join(b.Artifacts, "|"); got != "out/** => dist|report.xml" {
		t.Errorf("artifact rules (excluded paths dropped, + stripped): %q", got)
	}
	if b.Params["tag"] != "v1" {
		t.Errorf("params: %v", b.Params)
	}
	if b.Expand == nil || b.Expand("make TAG=%tag% %unknown%") != "make TAG=v1 %unknown%" {
		t.Error("Expand must substitute the job's own params and leave others")
	}
	if len(b.Triggers) != 1 || b.Triggers[0].From != nil || b.Triggers[0].Type != "vcs" {
		t.Errorf("build triggers: %+v", b.Triggers)
	}

	d := jobs["Deploy"]
	var deps []string
	for _, x := range d.Deps {
		deps = append(deps, depString(x))
	}
	// the dependency on the unknown object Missing is dropped (and warned about)
	want := "local=Build|local=Build artifacts|global=Platform_Base|global=ServiceA_Build"
	if got := strings.Join(deps, "|"); got != want {
		t.Errorf("deps:\n%s\nwant:\n%s", got, want)
	}
	if len(d.Triggers) != 1 || d.Triggers[0].From == nil || d.Triggers[0].From.Global != "Platform_Release" {
		t.Errorf("deploy triggers: %+v", d.Triggers)
	}
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "reference to unknown build configuration Missing") {
		t.Errorf("warnings: %v", p.Warnings)
	}
}

func depString(d ci.Dep) string {
	s := "local=" + d.Ref.Local
	if d.Ref.Local == "" {
		s = "global=" + d.Ref.Global
	}
	if d.Artifacts {
		s += " artifacts"
	}
	return s
}

func TestProjectIDOverrideAndAlias(t *testing.T) {
	r := checkout(t, map[string]string{".teamcity/settings.kts": `
project { buildType(Build); buildType(Test) }
object Build : BuildType({ id("Build") })
object Test : BuildType({ dependencies { snapshot(AbsoluteId("Build")) {} } })
`})
	r.Settings = &Settings{ProjectID: "Acme_Service"}
	p, err := New().Load(r)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range p.Jobs {
		if !strings.HasPrefix(j.QualID, "Acme_Service_") {
			t.Errorf("QualID %q ignores the configured project id", j.QualID)
		}
		for _, d := range j.Deps {
			if d.Ref.Alias != "Build" || d.Ref.Global != "Build" {
				t.Errorf("an id that names a job of this repo needs an alias: %+v", d.Ref)
			}
		}
	}
}

func TestCrossDeps(t *testing.T) {
	r := checkout(t, map[string]string{".teamcity/settings.kts": `
project { buildType(Build) }
object Build : BuildType({ name = "B" })
Build.dependsOn(AbsoluteId("Other_Lib"))
AbsoluteId("Other_App").dependsOn(Build)
`})
	p, err := New().Load(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.CrossDeps) != 1 {
		t.Fatalf("cross deps: %+v (warnings %v)", p.CrossDeps, p.Warnings)
	}
	c := p.CrossDeps[0]
	if c.Target.Global != "Other_App" || c.Dep.Ref.Local != "Build" {
		t.Errorf("cross dep: %+v", c)
	}
}

func TestDeriveProjectID(t *testing.T) {
	for in, want := range map[string]string{
		"service-a_api": "ServiceAApi",
		"service-a":     "ServiceA",
		"Platform":      "Platform",
		"--x--y":        "XY",
		"":              "",
	} {
		if got := deriveProjectID(in); got != want {
			t.Errorf("deriveProjectID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSubstParams(t *testing.T) {
	params := map[string]string{"a": "%b%", "b": "%c%", "c": "deep", "dir": "x"}
	for in, want := range map[string]string{
		"no params":       "no params",
		"%dir%/y":         "x/y",
		"%a%":             "deep", // nested references resolve
		"%missing% %dir%": "%missing% x",
		"100%":            "100%",
	} {
		if got := substParams(in, params); got != want {
			t.Errorf("substParams(%q) = %q, want %q", in, got, want)
		}
	}
	if got := substParams("%x%", map[string]string{"x": "%x%"}); got != "%x%" {
		t.Errorf("a self-reference must terminate, got %q", got)
	}
}

func TestSettingsSymlinkedOutsideTheRepositoryIsSkipped(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	r := checkout(t, map[string]string{".teamcity/settings.kts": "project { buildType(Build) }\nobject Build : BuildType({ name = \"Build\" })\n"})
	outside := filepath.Join(t.TempDir(), "evil.kts")
	if err := os.WriteFile(outside, []byte("object Leaked : BuildType({ name = \"Leaked\" })\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(r.Root, ".teamcity", "evil.kts")); err != nil {
		t.Fatal(err)
	}
	p, err := New().Load(r)
	if err != nil {
		t.Fatalf("one hostile file must not fail the whole repository: %v", err)
	}
	var keys []string
	for _, j := range p.Jobs {
		keys = append(keys, j.Key)
	}
	if got := strings.Join(keys, ","); strings.Contains(got, "Leaked") || !strings.Contains(got, "Build") {
		t.Errorf("jobs %q: want Build and not Leaked", got)
	}
	if got := strings.Join(p.Warnings, "\n"); !strings.Contains(got, "evil.kts") || !strings.Contains(got, "escapes") {
		t.Errorf("the refusal must be reported, got %q", got)
	}
}

func TestPasswordNeverReachesTheJobs(t *testing.T) {
	r := checkout(t, map[string]string{".teamcity/settings.kts": `
project { buildType(Deploy) }
object Deploy : BuildType({
    params { password("env.TOKEN", "hunter2") }
    steps { script { scriptContent = "deploy --token=%env.TOKEN%" } }
})
`})
	p, err := New().Load(r)
	if err != nil {
		t.Fatal(err)
	}
	j := p.Jobs[0]
	if got := j.Params["env.TOKEN"]; got != "******" {
		t.Errorf("param %q", got)
	}
	for _, st := range j.Steps {
		if strings.Contains(st.Script, "hunter2") {
			t.Errorf("the secret reached a step script: %q", st.Script)
		}
	}
}
