package makefile

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func write(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLoad(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{
		"svc/Makefile": `# comment
include ../mk/common.mk
-include missing.mk
BIN := out/$(NAME)
FLAGS = -v
FLAGS += -race
ENV ?= dev
.PHONY: all test

all: $(BIN) test   # default goal
$(BIN): gen
	go build $(FLAGS) -o $@ \
	  ./cmd
test: export CGO_ENABLED = 0
test:
	go test $(FLAGS) ./...
gen: ; go generate
ifeq ($(ENV),prod)
deploy:
	kubectl apply -f prod
else
deploy:
	kubectl apply -f $(ENV)
endif
%.o: %.c
	cc -c $<
`,
		"mk/common.mk": "NAME = svc\nlint:\n\tgolangci-lint run\n",
	})
	m, err := Load(root, "svc", "", map[string]string{"ENV": "stage"})
	if err != nil {
		t.Fatal(err)
	}
	// like GNU make, the first target of an included file counts for the default goal
	if m.Path != "svc/Makefile" || m.DefaultGoal != "lint" {
		t.Errorf("path %q default %q", m.Path, m.DefaultGoal)
	}
	if got := m.Rules["all"].Prereqs; !reflect.DeepEqual(got, []string{"out/svc", "test"}) {
		t.Errorf("all prereqs: %q", got)
	}
	r := m.Rules["out/svc"]
	if r == nil || len(r.Recipe) != 1 {
		t.Fatalf("out/svc: %+v", r)
	}
	if got := m.ExpandRecipe(r, r.Recipe[0]); got != "go build -v -race -o out/svc ./cmd" {
		t.Errorf("recipe: %q", got)
	}
	if m.Rules["gen"].Recipe[0] != "go generate" {
		t.Errorf("inline recipe: %q", m.Rules["gen"].Recipe)
	}
	if m.Rules["lint"] == nil || m.Rules["lint"].File != "mk/common.mk" {
		t.Errorf("include: %+v", m.Rules["lint"])
	}
	if m.Vars["ENV"] != "stage" {
		t.Errorf("command line var should win: %q", m.Vars["ENV"])
	}
	if len(m.Rules["deploy"].Recipe) != 2 {
		t.Errorf("both conditional branches expected: %q", m.Rules["deploy"].Recipe)
	}
	if !m.Phony["test"] || len(m.Rules["test"].Prereqs) != 0 {
		t.Errorf("phony/target-specific var: %+v", m.Rules["test"])
	}
	if len(m.Warnings) != 0 {
		t.Errorf("-include must not warn: %v", m.Warnings)
	}
}

func TestShellFunctionsAreNotRun(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "V := $(shell rm -rf /)\nall:\n\techo $(V)\n"})
	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := m.ExpandRecipe(m.Rules["all"], m.Rules["all"].Recipe[0]); got != "echo $(shell rm -rf /)" {
		t.Errorf("got %q", got)
	}
}

func TestLoadErrorsAndWarnings(t *testing.T) {
	root := t.TempDir()
	if _, err := Load(root, ".", "", nil); err == nil {
		t.Error("no makefile is an error")
	}
	write(t, root, map[string]string{"Makefile": "include nothere.mk\nall:\n\t@true\n"})
	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Warnings) == 0 {
		t.Error("a missing mandatory include is reported")
	}
	if got := m.Targets(); !reflect.DeepEqual(got, []string{"all"}) {
		t.Errorf("targets: %v", got)
	}
}

func TestFilePreferenceAndExplicitFile(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "a:\n", "GNUmakefile": "g:\n", "ci.mk": "c:\n"})
	m, _ := Load(root, ".", "", nil)
	if m.Path != "GNUmakefile" {
		t.Errorf("GNU make prefers GNUmakefile, got %s", m.Path)
	}
	m, _ = Load(root, ".", "ci.mk", nil)
	if m.Path != "ci.mk" || m.Rules["c"] == nil {
		t.Errorf("explicit -f: %+v", m)
	}
}

func TestExpand(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": `A = 1
B = $(A)2
C := $(B)3
D = ${A}${A}
E = $(subst x,y,xx)
F = $(undefined)
G = $$HOME
`})
	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]string{
		"$(A)": "1", "$(B)": "12", "$(C)": "123", "${A}": "1", "$(D)": "11",
		"$(F)x": "x", "plain": "plain",
	} {
		if got := m.Expand(in); got != want {
			t.Errorf("Expand(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCommandLineVarsWinOverAssignments(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "TAG = dev\nTAG := other\nimg:\n\tdocker build -t x:$(TAG) .\n"})
	m, _ := Load(root, ".", "", map[string]string{"TAG": "v1"})
	if got := m.Expand("$(TAG)"); got != "v1" {
		t.Errorf("got %q", got)
	}
}

func TestAutomaticVariables(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "out: a b\n\tcp $< $@ # $^\na b:\n"})
	m, _ := Load(root, ".", "", nil)
	r := m.Rules["out"]
	if got := m.ExpandRecipe(r, r.Recipe[0]); got != "cp a out # a b" {
		t.Errorf("got %q", got)
	}
	if _, leaked := m.Vars["@"]; leaked {
		t.Error("automatic variables must not leak out of the recipe")
	}
}

func TestStaticPatternAndMultiTargetRules(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "a b: shared\n\t@true\nx.o y.o: %.o: %.c\n\t@true\nshared:\n"})
	m, _ := Load(root, ".", "", nil)
	for _, want := range []string{"a", "b", "shared", "x.o", "y.o"} {
		if m.Rules[want] == nil {
			t.Errorf("rule %s missing; have %v", want, m.Targets())
		}
	}
}

func TestIncludeCannotLeaveTheRepository(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	write(t, parent, map[string]string{"secret.mk": "LEAK := top-secret\n"})
	write(t, root, map[string]string{"Makefile": "include ../secret.mk\ninclude ../../etc/hosts\nall:\n\techo $(LEAK)\n"})

	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := m.Vars["LEAK"]; ok {
		t.Errorf("a file outside the repository was included: LEAK=%q", v)
	}
	if got := strings.Join(m.Warnings, "\n"); !strings.Contains(got, "include ../secret.mk") || !strings.Contains(got, "escapes") {
		t.Errorf("the refused include must be reported with its reason, got %q", got)
	}
	// `make -f ../secret.mk` is the same escape by another door.
	if _, err := Load(root, ".", "../secret.mk", nil); err == nil {
		t.Error("Load must refuse a makefile outside the repository")
	}
}

// The scanner stopped silently at a line over 1 MiB, and everything after it was lost.
func TestVeryLongLineDoesNotHideTheRules(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "BIG := " + strings.Repeat("x", 2<<20) + "\nall:\n\techo hi\n"})
	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m.Rules["all"] == nil {
		t.Error("the rule after a 2 MiB line was dropped")
	}
}

func TestCRLFLineContinuation(t *testing.T) {
	root := t.TempDir()
	write(t, root, map[string]string{"Makefile": "all:\r\n\tgo build \\\r\n\t  ./cmd\r\n"})
	m, err := Load(root, ".", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := m.Rules["all"]
	if r == nil || len(r.Recipe) != 1 || strings.ContainsAny(r.Recipe[0], "\r\\") {
		t.Errorf("recipe %+v", r)
	}
}
