package shell

import (
	"reflect"
	"testing"
)

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
		if got := looksLikeImage(in); got != want {
			t.Errorf("looksLikeImage(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestDockerfileFrom(t *testing.T) {
	got := DockerfileFrom(`ARG BASE=reg.io/base:1
FROM --platform=$BUILDPLATFORM golang:1.22 AS build
FROM ${BASE}
COPY --from=build /out /app
FROM build AS test
`, nil)
	want := []string{"golang:1.22", "reg.io/base:1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %q want %q", got, want)
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

// Replacing keys one after another made "$AB" depend on whether A or AB came first, and
// Go randomises map order: the same Dockerfile gave different graphs from run to run.
func TestExpandArgsIsDeterministicAndWholeName(t *testing.T) {
	args := map[string]string{"A": "x", "AB": "y", "REG": "reg.io", "NESTED": "$A"}
	cases := map[string]string{
		"$AB":          "y",
		"$A":           "x",
		"${A}B":        "xB",
		"$A-$AB":       "x-y",
		"$REG/app:$AB": "reg.io/app:y",
		"${REG}/app":   "reg.io/app",
		"$UNKNOWN/$A":  "$UNKNOWN/x",
		"${UNKNOWN}":   "${UNKNOWN}",
		"$NESTED":      "$A", // a value is not expanded again
		"price$":       "price$",
		"$$A":          "$x",
		"plain":        "plain",
		"$A_B":         "$A_B", // the whole name is A_B, which is not an arg
		"${A}_B":       "x_B",
	}
	for i := 0; i < 100; i++ {
		for in, want := range cases {
			if got := expandArgs(in, args); got != want {
				t.Fatalf("run %d: expandArgs(%q) = %q, want %q", i, in, got, want)
			}
		}
	}
}
