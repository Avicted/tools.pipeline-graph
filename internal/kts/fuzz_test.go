package kts

import (
	"reflect"
	"testing"
)

// settings.kts comes from untrusted repositories. The lexer and the parser must not panic,
// and must give the same tree the second time. `go test` runs the seeds; to search for more:
// go test ./internal/kts -fuzz=FuzzParse -fuzztime=1m
func FuzzParse(f *testing.F) {
	for _, seed := range []string{
		`project { buildType(Build) }
object Build : BuildType({ name = "b"; steps { script { scriptContent = "make" } } })`,
		"fun f(a: String) = a + \"x${a}\"\nval v by lazy { listOf(1, 2) }",
		"if (a) { b() } else if (c) { d() } else { e() }\ntry { x() } catch (e: Exception) { y() }",
		`val s = """raw ${'$'}HOME "quoted" """ // c /* nested /* c */ */`,
		"@Suppress(\"x\") private object O { init { a.b.c(d) } }",
		"(", ")", "{", "}", "[", "\"", "\"\"\"", "'", "/*", "//", "${", "$", "@", "a.", "a?.b!!", "x<y>(z)", "a { b { c {",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		a, ta := Parse(src)
		b, tb := Parse(src)
		if ta != tb || !reflect.DeepEqual(a, b) {
			t.Fatalf("not deterministic for %q", src)
		}
		for _, tok := range Lex(src) {
			_ = tok
		}
	})
}
