package kts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDirReadsKotlinFilesOnly(t *testing.T) {
	root := t.TempDir()
	put := func(p, c string) {
		full := filepath.Join(root, p)
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put(".teamcity/settings.kts", `project { buildType(A) }`)
	put(".teamcity/A.kt", `object A : BuildType({ name = "A" })`)
	put(".teamcity/build/Generated.kt", `object Gen : BuildType({ name = "Gen" })`)
	put(".teamcity/target/Out.kt", `object Out : BuildType({ name = "Out" })`)
	put(".teamcity/.idea/X.kt", `object Idea : BuildType({ name = "Idea" })`)
	put(".teamcity/notes.txt", `object Txt : BuildType({ name = "Txt" })`)
	s, err := ParseDir(filepath.Join(root, ".teamcity"), root)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Builds) != 1 || s.Builds[0].Key != "A" {
		t.Errorf("only source files outside build output are read: %+v", s.Builds)
	}
	if s.Builds[0].File != ".teamcity/A.kt" {
		t.Errorf("paths are relative to the repo root: %q", s.Builds[0].File)
	}
	if _, err := ParseDir(filepath.Join(root, "missing"), root); err == nil {
		t.Error("a missing directory is an error")
	}
}

func TestRefStringAndLookup(t *testing.T) {
	for r, want := range map[Ref]string{
		{Kind: RefObject, Value: "A"}:   "object(A)",
		{Kind: RefRelative, Value: "A"}: "RelativeId(A)",
		{Kind: RefAbsolute, Value: "A"}: "AbsoluteId(A)",
		{Kind: RefString, Value: "A"}:   "id(A)",
	} {
		if r.String() != want {
			t.Errorf("%v", r.String())
		}
	}
	s := parse(t, map[string]string{"s.kts": `object Obj : BuildType({ id("Key"); name = "N" })`})
	for _, r := range []Ref{{Kind: RefObject, Value: "Obj"}, {Kind: RefObject, Value: "Key"}, {Kind: RefRelative, Value: "Key"}, {Kind: RefRelative, Value: "Obj"}} {
		if s.Lookup(r) == nil {
			t.Errorf("Lookup(%v) failed", r)
		}
	}
	if s.Lookup(Ref{Kind: RefAbsolute, Value: "Key"}) != nil || s.Lookup(Ref{Kind: RefObject, Value: "None"}) != nil {
		t.Error("absolute ids are not resolved locally; unknown names resolve to nil")
	}
}

func TestDockerCommandSteps(t *testing.T) {
	s := parse(t, map[string]string{"s.kts": `
object D : BuildType({
    name = "D"
    steps {
        dockerCommand {
            commandType = build { source = file { path = "docker/App.Dockerfile" }; contextDir = "app"; namesAndTags = "reg.io/app:1 reg.io/app:latest" }
        }
        dockerCommand { commandType = push { namesAndTags = "reg.io/app:1" } }
        dockerCommand { commandType = other { subCommand = "image"; commandArgs = "prune -f" } }
    }
})`})
	b := s.Builds[0]
	if len(b.Steps) != 3 {
		t.Fatalf("steps: %+v", b.Steps)
	}
	if got := b.Steps[0].Script; !strings.Contains(got, "docker build") || !strings.Contains(got, "-t reg.io/app:1") || !strings.Contains(got, "-t reg.io/app:latest") {
		t.Errorf("build: %q", got)
	}
	if got := b.Steps[1].Script; got != "docker push reg.io/app:1" {
		t.Errorf("push: %q", got)
	}
	if got := b.Steps[2].Script; got != "docker image prune -f" {
		t.Errorf("other: %q", got)
	}
}

func TestProjectNamesAndSubprojects(t *testing.T) {
	s := parse(t, map[string]string{"s.kts": `
object Backend : Project({
    name = "Backend services"
    buildType(Api)
})
object Api : BuildType({ name = "Api" })
`})
	if len(s.Builds) != 1 || s.Builds[0].Project != "Backend services" {
		t.Errorf("builds: %+v", s.Builds)
	}
}

func TestSyntaxEdgeCases(t *testing.T) {
	// none of these may panic or loop; the parser is deliberately tolerant
	for name, src := range map[string]string{
		"char literal":  `val c = 'x'; val q = '\''; object A : BuildType({ name = "A" })`,
		"generics":      `val m = mapOf<String, List<Int>>("a" to listOf(1)); object A : BuildType({ name = "A" })`,
		"typed val":     `val x: Map<String, String> = emptyMap(); object A : BuildType({ name = "A" })`,
		"annotations":   `@file:Suppress("x")` + "\n" + `object A : BuildType({ name = "A" })`,
		"unterminated":  `object A : BuildType({ name = "A`,
		"unbalanced":    `object A : BuildType({ steps { `,
		"stray tokens":  `) } ] ; , object A : BuildType({ name = "A" })`,
		"empty":         ``,
		"only comments": "// a\n/* b */",
		"lambda arrows": `val f = { x: Int -> x + 1 }; object A : BuildType({ name = "A" })`,
		"safe calls":    `val v = foo?.bar ?: "d"; object A : BuildType({ name = "A" })`,
		"when and if":   `val v = if (a) "x" else "y"; object A : BuildType({ name = "A" })`,
		"deep nesting":  strings.Repeat("a(", 200) + strings.Repeat(")", 200),
	} {
		t.Run(name, func(t *testing.T) {
			s := ParseSources(map[string]string{"s.kts": src})
			if strings.Contains(src, `object A : BuildType({ name = "A" })`) && (len(s.Builds) != 1 || s.Builds[0].Name != "A") {
				t.Errorf("the build after the odd syntax must still be found: %+v", s.Builds)
			}
		})
	}
}

func TestStringTemplates(t *testing.T) {
	s := parse(t, map[string]string{"s.kts": `
const val REG = "reg.io"
object A : BuildType({
    name = "A"
    params { param("img", "$REG/svc:${'$'}TAG") }
    steps { script { scriptContent = "echo ${REG}/${'$'}HOME" } }
})`})
	b := s.Builds[0]
	if b.Params["img"] != "reg.io/svc:$TAG" {
		t.Errorf("const templates: %q", b.Params["img"])
	}
	if b.Steps[0].Script != "echo reg.io/$HOME" {
		t.Errorf("${'$'} is a literal dollar: %q", b.Steps[0].Script)
	}
}
