package teamcity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func parse(t *testing.T, srcs map[string]string) *Declared {
	t.Helper()
	s := ParseSources(srcs)
	return s
}

func TestExtractBuild(t *testing.T) {
	s := parse(t, map[string]string{"settings.kts": `
const val REG = "reg.io"
project {
    buildType(B)
    buildType(C)
}
object T : Template({
    steps { script { scriptContent = "echo template" } }
    triggers { vcs { branchFilter = "+:main" } }
})
object B : BuildType({
    templates(T)
    id("Bee")
    name = "Build"
    artifactRules = "out/** => out.zip"
    params { param("env.IMG", "$REG/b") }
    steps {
        script { name = "s1"; scriptContent = "docker build -t %env.IMG%:1 ." }
        exec { path = "make"; arguments = "-C sub all" }
    }
})
object C : BuildType({
    name = "Consumer"
    type = BuildTypeSettings.Type.COMPOSITE
    dependencies {
        artifacts(B) { artifactRules = "out.zip!** => in" }
        snapshot(AbsoluteId("Other_Proj_Build")) {}
    }
    triggers {
        finishBuildTrigger { buildType = "${B.id}" }
        schedule { schedulingPolicy = cron { hours = "3" } }
    }
})
`})
	if len(s.Warnings) > 0 {
		t.Errorf("warnings: %v", s.Warnings)
	}
	if len(s.Builds) != 2 {
		t.Fatalf("builds: %d", len(s.Builds))
	}
	b := s.Lookup(Ref{Kind: RefObject, Value: "B"})
	if b == nil || b.Key != "Bee" || b.Name != "Build" || b.ArtifactRules != "out/** => out.zip" {
		t.Fatalf("B: %+v", b)
	}
	if b.Params["env.IMG"] != "reg.io/b" {
		t.Errorf("const template not expanded: %q", b.Params["env.IMG"])
	}
	if len(b.Steps) != 3 || b.Steps[0].Script != "echo template" || b.Steps[2].Script != "make -C sub all" {
		t.Errorf("steps (template first): %+v", b.Steps)
	}
	if len(b.Triggers) != 1 || b.Triggers[0].Detail != "+:main" {
		t.Errorf("template trigger: %+v", b.Triggers)
	}
	c := s.Lookup(Ref{Kind: RefObject, Value: "C"})
	if !c.Composite {
		t.Error("composite not detected")
	}
	if len(c.Deps) != 2 || !c.Deps[0].Artifacts || c.Deps[0].Rules != "out.zip!** => in" || c.Deps[1].Ref != (Ref{Kind: RefAbsolute, Value: "Other_Proj_Build", Line: c.Deps[1].Ref.Line}) {
		t.Errorf("deps: %+v", c.Deps)
	}
	if len(c.Triggers) != 2 || c.Triggers[0].Ref == nil || c.Triggers[0].Ref.Value != "B" || c.Triggers[1].Detail != "cron hours=3" {
		t.Errorf("triggers: %+v", c.Triggers)
	}
}

func TestExtractFunctionsAndSequential(t *testing.T) {
	s := parse(t, map[string]string{
		"settings.kts": `
project {
    sequential {
        buildType(Compile)
        parallel {
            buildType(deploy("eu"))
            sequential {
                buildType(deploy("us"))
                buildType(Verify)
            }
        }
        buildType(Done)
    }
}
object Compile : BuildType({ name = "Compile" })
object Verify : BuildType({ name = "Verify" })
object Done : BuildType({ name = "Done" })
`,
		"Deploy.kt": `
fun deploy(region: String): BuildType {
    return BuildType {
        id("Deploy_${region}")
        name = "Deploy $region"
    }
}
`})
	deps := map[string][]string{}
	for _, b := range s.Builds {
		for _, d := range b.Deps {
			deps[b.Key] = append(deps[b.Key], d.Ref.Value)
		}
	}
	want := map[string]string{
		"Deploy_eu": "Compile",
		"Deploy_us": "Compile",
		"Verify":    "Deploy_us",
		"Done":      "Deploy_eu,Verify",
	}
	for k, v := range want {
		if got := strings.Join(deps[k], ","); got != v {
			t.Errorf("%s deps: got %q want %q", k, got, v)
		}
	}
	if b := s.Lookup(Ref{Kind: RefRelative, Value: "Deploy_eu"}); b == nil || b.Name != "Deploy eu" {
		t.Errorf("function instantiation: %+v", b)
	}
}

func TestInlineAndDependsOn(t *testing.T) {
	s := parse(t, map[string]string{"settings.kts": `
val a = BuildType { id("A"); name = "A" }
project {
    buildType(a)
    buildType {
        id("Inline")
        name = "Inline"
    }
    if (true) {
        buildType(Cond)
    }
}
object Cond : BuildType({ name = "Cond" })
Cond.dependsOn(a)
changeBuildType(RelativeId("A")) { }
`})
	if len(s.Builds) != 3 {
		t.Fatalf("builds: %+v", s.Builds)
	}
	c := s.Lookup(Ref{Kind: RefObject, Value: "Cond"})
	if len(c.Deps) != 1 || c.Deps[0].Ref.Value != "a" || s.Lookup(c.Deps[0].Ref).Key != "A" {
		t.Errorf("dependsOn: %+v", c.Deps)
	}
	if len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "changeBuildType") {
		t.Errorf("patch warning: %v", s.Warnings)
	}
}

func TestTrimIndentAndEscapes(t *testing.T) {
	s := parse(t, map[string]string{"settings.kts": `
object B : BuildType({
    steps {
        script {
            scriptContent = """
                echo ${'$'}HOME
                  make all
            """.trimIndent()
        }
    }
})
`})
	if got := s.Builds[0].Steps[0].Script; got != "echo $HOME\n  make all" {
		t.Errorf("script: %q", got)
	}
}

// Patterns seen in real settings (JetBrains/kotlin-web-site, teamcity-rest-client).
// Patterns seen in real settings (JetBrains/kotlin-web-site, teamcity-rest-client).
func TestRealWorldPatterns(t *testing.T) {
	s := parse(t, map[string]string{
		"Base.kt": `
const val LIB_ID = "kotlinx.io"
abstract class ApiPages(
    apiId: String,
    stepBuild: BuildSteps.() -> BuildStep? = { scriptBuild() },
    init: BuildType.() -> Unit = {}
) : BuildType({
    name = "$apiId pages"
    steps {
        stepBuild()?.also { step -> step(step) }
        script { scriptContent = "make pages ID=$apiId" }
    }
    init(this)
})
fun Dependencies.dependsOnTemplate(build: BuildType) {
    dependency(build) {
        snapshot {}
        artifacts { artifactRules = "templates.zip" }
    }
}
private val external = AbsoluteId("Other_Collect")
`,
		"Io.kt": `
object Templates : BuildType({ name = "Templates" })
object IoPages : ApiPages(
    apiId = LIB_ID,
    init = {
        dependencies { dependsOnTemplate(Templates) }
        triggers { finishBuildTrigger { buildType = Templates.id?.value ?: error("no id") } }
    })
object Stats : BuildType({
    dependencies { artifacts(external) { artifactRules = "x.json" } }
})
`,
		"settings.kts": `
class Landing(val name: String) : BuildType({
    id("Landing_${name}")
    name = "Landing $name"
})
project {
    val check = buildType {
        id("Check")
        name = "Check"
    }
    buildType {
        id("Publish")
        dependencies { snapshot(check) {} }
    }
    buildType(Landing("home"))
}
`})
	io := s.Lookup(Ref{Kind: RefObject, Value: "IoPages"})
	if io == nil || io.Name != "kotlinx.io pages" {
		t.Fatalf("subclass with named args: %+v", io)
	}
	if len(io.Steps) != 1 || io.Steps[0].Script != "make pages ID=kotlinx.io" {
		t.Errorf("steps: %+v", io.Steps)
	}
	if len(io.Deps) != 1 || io.Deps[0].Ref.Value != "Templates" || !io.Deps[0].Artifacts || io.Deps[0].Rules != "templates.zip" {
		t.Errorf("inlined helper dependency: %+v", io.Deps)
	}
	if len(io.Triggers) != 1 || io.Triggers[0].Ref == nil || io.Triggers[0].Ref.Value != "Templates" {
		t.Errorf("elvis trigger ref: %+v", io.Triggers)
	}
	st := s.Lookup(Ref{Kind: RefObject, Value: "Stats"})
	if len(st.Deps) != 1 || st.Deps[0].Ref.Kind != RefAbsolute || st.Deps[0].Ref.Value != "Other_Collect" {
		t.Errorf("val holding AbsoluteId: %+v", st.Deps)
	}
	pub := s.Lookup(Ref{Kind: RefRelative, Value: "Publish"})
	if pub == nil || len(pub.Deps) != 1 || s.Lookup(pub.Deps[0].Ref) == nil || s.Lookup(pub.Deps[0].Ref).Key != "Check" {
		t.Errorf("val = buildType {} in project: %+v", pub)
	}
	if l := s.Lookup(Ref{Kind: RefRelative, Value: "Landing_home"}); l == nil || l.Name != "Landing home" {
		t.Errorf("class instantiation: %+v", s.Builds)
	}
	if len(s.Warnings) != 0 {
		t.Errorf("warnings: %v", s.Warnings)
	}
}

// graph.json and the viewer show parameters and step scripts, and both are shared.
// A password's value must not be among the parameters, and so cannot be substituted anywhere.
// graph.json and the viewer show parameters and step scripts, and both are shared.
// A password's value must not be among the parameters, and so cannot be substituted anywhere.
func TestPasswordParametersAreRedacted(t *testing.T) {
	s := parse(t, map[string]string{"settings.kts": `
project { buildType(B) }
object B : BuildType({
    id("Bee")
    params {
        param("env.TAG", "v1")
        password("env.TOKEN", "hunter2")
        password("env.REF", "credentialsJSON:6bb2f4d1-0000-0000-0000-000000000000")
    }
    steps { script { scriptContent = "deploy --tag=%env.TAG% --token=%env.TOKEN%" } }
})
`})
	b := s.Builds[0]
	if b.Params["env.TAG"] != "v1" {
		t.Errorf("a plain parameter keeps its value: %q", b.Params["env.TAG"])
	}
	for _, k := range []string{"env.TOKEN", "env.REF"} {
		if got := b.Params[k]; got != "******" {
			t.Errorf("%s = %q, want the value hidden", k, got)
		}
	}
}

// Go cannot recover from a stack overflow, so one hostile settings.kts would kill the whole
// run. Each case repeats a unit that nests the parser one level deeper per repetition.
func TestTooDeepFileIsAWarningNotACrash(t *testing.T) {
	s := ParseSources(map[string]string{
		"deep.kts": "object Deep : BuildType({ name = \"x\" })\n" + strings.Repeat("(", 500_000),
		"ok.kts":   "project { buildType(Fine) }\nobject Fine : BuildType({ name = \"Fine\" })\n",
	})
	if got := strings.Join(s.Warnings, "\n"); !strings.Contains(got, "deep.kts") || !strings.Contains(got, "deeper than") {
		t.Errorf("warnings: %q", got)
	}
	var names []string
	for _, b := range s.Builds {
		names = append(names, b.Name)
	}
	if !strings.Contains(strings.Join(names, ","), "Fine") {
		t.Errorf("the other files must still be analysed, got %q", names)
	}
}

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
