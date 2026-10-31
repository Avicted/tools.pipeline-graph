package kts

import (
	"strings"
	"testing"
)

func TestLexStrings(t *testing.T) {
	toks := Lex(`a = "x\"y${b("q")}" // c
/* nested /* comment */ */ s = """raw ${'$'}HOME
"quoted" """`)
	var strs []string
	for _, tk := range toks {
		if tk.Kind == TString {
			strs = append(strs, tk.Val)
		}
	}
	want := []string{`x"y${b("q")}`, "raw ${'$'}HOME\n\"quoted\" "}
	if len(strs) != len(want) {
		t.Fatalf("got %q", strs)
	}
	for i := range want {
		if strs[i] != want[i] {
			t.Errorf("string %d: got %q want %q", i, strs[i], want[i])
		}
	}
	if toks[len(toks)-1].Line != 3 {
		t.Errorf("line tracking: %d", toks[len(toks)-1].Line)
	}
}

func TestParseCallsAndLambdas(t *testing.T) {
	nodes, _ := Parse(`
object A : BuildType({
    name = "a"
    steps { script { scriptContent = "make x" } }
})
X.dependsOn(Y)
`)
	if len(nodes) != 2 {
		t.Fatalf("got %d top-level nodes", len(nodes))
	}
	o := nodes[0]
	if o.Kind != NObject || o.Name != "A" || LastName(o.Supers[0].CallName()) != "BuildType" {
		t.Fatalf("object: %+v", o)
	}
	body := o.Supers[0].Lambda().Body
	if body[0].Kind != NAssign || body[0].Name != "name" || body[1].CallName() != "steps" {
		t.Fatalf("body: %+v %+v", body[0], body[1])
	}
	if nodes[1].CallName() != "X.dependsOn" {
		t.Fatalf("call: %q", nodes[1].CallName())
	}
}

func parse(t *testing.T, srcs map[string]string) *Settings {
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
func TestParseDepthIsBounded(t *testing.T) {
	cases := []struct{ name, prefix, unit string }{
		{"parens", "", "("},
		{"lambdas", "", "{"},
		{"calls", "", "x("},
		{"annotations", "x = ", "@a "}, // in an expression an annotation wraps what follows; between statements it is flat
		{"modifiers", "", "private "},
		{"else chain", "", "if (a) else "},
		{"catch chain", "", "try {} catch "},
		{"object bodies", "", "object O { "},
		{"function body", "", "fun f() = "},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, truncated := Parse(c.prefix + strings.Repeat(c.unit, 500_000)); !truncated {
				t.Error("a file nested half a million levels deep must be reported as cut off")
			}
		})
	}
}

func TestParseKeepsRealisticNesting(t *testing.T) {
	deep := strings.Repeat("a { ", 40) + strings.Repeat("} ", 40)
	chain := "if (x) { a() }" + strings.Repeat(" else if (x) { a() }", 150)
	args := "f(" + strings.Repeat("g(", 100) + "1" + strings.Repeat(")", 100) + ")"
	for name, src := range map[string]string{"40 nested blocks": deep, "150 else-ifs": chain, "100 nested calls": args} {
		if _, truncated := Parse(src); truncated {
			t.Errorf("%s is normal DSL and must not be cut off", name)
		}
	}
}

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
