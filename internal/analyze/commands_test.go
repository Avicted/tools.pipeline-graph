package analyze

import (
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci/teamcity"
)

func run1(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	g, w := Run([]Repo{repo(t, "app", files)}, Options{Providers: tc})
	return edges(g), strings.Join(w, "\n")
}

func build(script string) string {
	return `object B : BuildType({ name = "B"
    steps { script { scriptContent = """` + script + `""" } } })`
}

func TestMakeInSubdirectoryAndWorkDir(t *testing.T) {
	got, _ := run1(t, map[string]string{
		".teamcity/s.kts": `object B : BuildType({ name = "B"
    steps {
        script { scriptContent = "make -C svc build" }
        script { scriptContent = "make test"; workingDir = "other" }
        script { scriptContent = "cd svc && make lint" }
        script { scriptContent = "make -f ci.mk all" }
    } })`,
		"svc/Makefile":   "build:\n\t@true\nlint:\n\t@true\n",
		"other/Makefile": "test:\n\t@true\n",
		"ci.mk":          "all:\n\t@true\n",
	})
	want := []string{
		"bt:app/B>mk:app/ci.mk:all:runs",
		"bt:app/B>mk:app/other/Makefile:test:runs",
		"bt:app/B>mk:app/svc/Makefile:build:runs",
		"bt:app/B>mk:app/svc/Makefile:lint:runs",
	}
	if got != strings.Join(want, "\n") {
		t.Errorf("edges:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
}

func TestDefaultGoal(t *testing.T) {
	got, _ := run1(t, map[string]string{
		".teamcity/s.kts": build("make"),
		"Makefile":        ".DEFAULT_GOAL := ship\nbuild:\n\t@true\nship: build\n\t@true\n",
	})
	if !strings.Contains(got, "bt:app/B>mk:app/Makefile:ship:runs") || !strings.Contains(got, "mk:app/Makefile:ship>mk:app/Makefile:build:depends") {
		t.Errorf("bare `make` runs the default goal and its prerequisites:\n%s", got)
	}
}

func TestMakeProblemsAreWarnings(t *testing.T) {
	got, warn := run1(t, map[string]string{
		".teamcity/s.kts": `object B : BuildType({ name = "B"
    steps {
        script { scriptContent = "make -C nowhere all" }
        script { scriptContent = "make -C ../outside all" }
        script { scriptContent = "make -C ${'$'}DIR all" }
        script { scriptContent = "make ghost" }
    } })`,
		"Makefile": "real:\n\t@true\n",
	})
	for _, w := range []string{
		`B runs make in "nowhere" but no makefile was found there`,
		`make -C ../outside (from B) cannot be resolved statically`,
		`make target "ghost" is invoked but not defined`,
	} {
		if !strings.Contains(warn, w) {
			t.Errorf("missing warning %q in:\n%s", w, warn)
		}
	}
	if !strings.Contains(got, "mk:app/Makefile:ghost") {
		t.Errorf("an undefined target still shows up, marked as having no rule:\n%s", got)
	}
}

func TestMakeCallsMake(t *testing.T) {
	got, _ := run1(t, map[string]string{
		".teamcity/s.kts": build("make all"),
		"Makefile":        "all: prep\n\t$(MAKE) -C sub deploy\nprep:\n\t@true\n",
		"sub/Makefile":    "deploy:\n\t@true\n",
	})
	for _, e := range []string{
		"bt:app/B>mk:app/Makefile:all:runs",
		"mk:app/Makefile:all>mk:app/Makefile:prep:depends",
		"mk:app/Makefile:all>mk:app/sub/Makefile:deploy:calls",
	} {
		if !strings.Contains(got, e) {
			t.Errorf("missing edge %s in:\n%s", e, got)
		}
	}
}

func TestRecursiveMakeTerminates(t *testing.T) {
	got, _ := run1(t, map[string]string{
		".teamcity/s.kts": build("make a"),
		"Makefile":        "a: b\n\t@true\nb: a\n\t$(MAKE) a\n",
	})
	if !strings.Contains(got, "mk:app/Makefile:a>mk:app/Makefile:b:depends") {
		t.Errorf("cycle:\n%s", got)
	}
}

func TestScriptsAreFollowed(t *testing.T) {
	files := map[string]string{
		".teamcity/s.kts": build("./ci/build.sh"),
		"ci/build.sh":     "#!/bin/sh\n./ci/inner.sh\n",
		"ci/inner.sh":     "make pkg\n",
		"Makefile":        "pkg:\n\t@true\n",
	}
	if got, _ := run1(t, files); got != "bt:app/B>mk:app/Makefile:pkg:runs" {
		t.Errorf("make called from a script called from a script:\n%s", got)
	}
	// scripts that call each other forever must not hang the analysis
	files["ci/inner.sh"] = "./ci/build.sh\nmake pkg\n"
	if got, _ := run1(t, files); got != "bt:app/B>mk:app/Makefile:pkg:runs" {
		t.Errorf("recursion:\n%s", got)
	}
}

func TestDockerImageFlows(t *testing.T) {
	got, warn := run1(t, map[string]string{
		".teamcity/s.kts": `object Base : BuildType({ name = "Base"
    steps { script { scriptContent = "docker build -t reg.io/base:1 . && docker push reg.io/base:1" } } })
object App : BuildType({ name = "App"
    steps {
        script { scriptContent = "docker pull reg.io/base:1" }
        script { scriptContent = "docker run --rm reg.io/base:1 true" }
        script { scriptContent = "docker pull alpine:3" }
        script { scriptContent = "docker push ${'$'}{REGISTRY}/dyn:1" }
    } })`,
		"Dockerfile": "FROM alpine:3\n",
	})
	if !strings.Contains(got, "bt:app/Base>img:reg.io/base:produces") || !strings.Contains(got, "img:reg.io/base>bt:app/App:consumes") {
		t.Errorf("edges:\n%s", got)
	}
	if strings.Contains(got, "alpine") {
		t.Errorf("third-party images are not nodes:\n%s", got)
	}
	if !strings.Contains(warn, "dynamic name; not linked") {
		t.Errorf("a pushed image with a variable name is reported:\n%s", warn)
	}
}

func TestParamsReachScripts(t *testing.T) {
	got, _ := run1(t, map[string]string{
		".teamcity/s.kts": `object B : BuildType({ name = "B"
    params { param("target", "pkg"); param("dir", "svc") }
    steps { script { scriptContent = "make %target%"; workingDir = "%dir%" } } })`,
		"svc/Makefile": "pkg:\n\t@true\n",
	})
	if got != "bt:app/B>mk:app/svc/Makefile:pkg:runs" {
		t.Errorf("%%param%% references are substituted before scanning:\n%s", got)
	}
}

func TestExternalIDsOverride(t *testing.T) {
	a := repo(t, "a", map[string]string{".teamcity/s.kts": `object B : BuildType({ name = "B"
    dependencies { snapshot(AbsoluteId("Legacy_Thing")) {} } })`})
	b := repo(t, "b", map[string]string{".teamcity/s.kts": `object Thing : BuildType({ name = "Thing" })`})
	g, w := Run([]Repo{a, b}, Options{Providers: tc, ExternalIDs: map[string]string{"Legacy_Thing": "b/Thing"}})
	if got := edges(g); got != "bt:b/Thing>bt:a/B:snapshot" {
		t.Errorf("edges: %s (warnings %v)", got, w)
	}
}

func TestProjectIDFromConfigMakesIDsExact(t *testing.T) {
	a := repo(t, "lib", map[string]string{".teamcity/s.kts": `object Pub : BuildType({ name = "Pub" })`})
	a.Settings = map[string]any{"teamcity": &teamcity.Settings{ProjectID: "Acme_Libs_Core"}}
	b := repo(t, "app", map[string]string{".teamcity/s.kts": `object B : BuildType({ name = "B"
    dependencies { snapshot(AbsoluteId("Acme_Libs_Core_Pub")) {} } })`})
	g, w := Run([]Repo{a, b}, Options{Providers: tc})
	if got := edges(g); got != "bt:lib/Pub>bt:app/B:snapshot" || len(w) != 0 {
		t.Errorf("edges %s, warnings %v", got, w)
	}
}
