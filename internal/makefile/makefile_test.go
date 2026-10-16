package makefile

import (
	"os"
	"path/filepath"
	"reflect"
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
