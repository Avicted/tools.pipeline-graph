package shell

import (
	"reflect"
	"testing"
)

func TestWords(t *testing.T) {
	for in, want := range map[string][]string{
		`make a b`:              {"make", "a", "b"},
		`echo "a b" 'c d' e\ f`: {"echo", "a b", "c d", "e f"},
		`  spaced   out  `:      {"spaced", "out"},
		`FOO="x y" make`:        {"FOO=x y", "make"},
		``:                      nil,
	} {
		if got := Words(in); !reflect.DeepEqual(got, want) {
			t.Errorf("Words(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestChainsAndContinuations(t *testing.T) {
	r := Extract("cd a && make one; make two | tee log\nmake \\\n  three\nif true; then make four; fi\n")
	var targets []string
	for _, m := range r.Makes {
		targets = append(targets, m.Targets...)
	}
	for _, want := range []string{"one", "two", "three", "four"} {
		found := false
		for _, g := range targets {
			found = found || g == want
		}
		if !found {
			t.Errorf("make %s not found in %v", want, targets)
		}
	}
}

func TestCommentsAndQuotedMakeAreIgnored(t *testing.T) {
	r := Extract("# make nope\necho \"make nope\"\nmakeself archive\ngmake real\n")
	for _, m := range r.Makes {
		for _, tgt := range m.Targets {
			if tgt == "nope" || tgt == "archive" {
				t.Errorf("mistook %v for a make call", m)
			}
		}
	}
}

func TestDockerVariants(t *testing.T) {
	r := Extract(`docker pull reg.io/a:1
docker tag reg.io/a:1 reg.io/b:2
docker image build -t reg.io/c:3 .
docker compose up
docker login reg.io
docker`)
	if len(r.Dockers) < 3 {
		t.Fatalf("dockers: %+v", r.Dockers)
	}
	if d := r.Dockers[0]; d.Action != "pull" || d.Images[0] != "reg.io/a:1" {
		t.Errorf("pull: %+v", d)
	}
	var build bool
	for _, d := range r.Dockers {
		if d.Action == "build" && len(d.Images) == 1 && d.Images[0] == "reg.io/c:3" {
			build = true
		}
	}
	if !build {
		t.Errorf("`docker image build` is a build: %+v", r.Dockers)
	}
}

func TestLooksLikeImage(t *testing.T) {
	for in, want := range map[string]bool{
		"reg.io/app:1": true, "org/app:2": true, "reg.io/app": true, "\"reg.io/app:1\"": true, // quotes are ignored
		"org/app": false,
		// bare names are too ambiguous with paths and flags
		"alpine": false, "alpine:3": false,
		"": false, "-t": false, "./ctx": false, "/abs/path": false, "a b": false, "A=b/c.d": false,
	} {
		if got := LooksLikeImage(in); got != want {
			t.Errorf("LooksLikeImage(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDockerfileFromBuildArgs(t *testing.T) {
	got := DockerfileFrom("ARG BASE=reg.io/base:1\nFROM ${BASE}\nFROM $OTHER\nFROM scratch\n", map[string]string{"BASE": "reg.io/override:9"})
	// unresolved variables are left for the analyzer, which refuses dynamic names
	want := []string{"reg.io/override:9", "$OTHER"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("build args override ARG defaults and scratch is dropped: got %q, want %q", got, want)
	}
}
