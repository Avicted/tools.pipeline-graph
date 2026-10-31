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
