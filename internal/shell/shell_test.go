package shell

import (
	"reflect"
	"testing"
)

func TestExtractMake(t *testing.T) {
	r := Extract(`#!/bin/bash
set -euo pipefail
# make commented
cd services/api && make -j 4 build test VERSION=%build.number%
make -C ../web -f Build.mk bundle
FOO=1 $(MAKE) --directory=lib all || echo failed
@$(MAKE) -s
`)
	want := []Make{
		{Dir: "services/api", Targets: []string{"build", "test"}, Vars: map[string]string{"VERSION": "%build.number%"}},
		{Dir: "services/web", File: "Build.mk", Targets: []string{"bundle"}, Vars: map[string]string{}},
		{Dir: "services/api/lib", Targets: []string{"all"}, Vars: map[string]string{}},
		{Dir: "services/api", Vars: map[string]string{}},
	}
	if len(r.Makes) != len(want) {
		t.Fatalf("got %+v", r.Makes)
	}
	for i, w := range want {
		g := r.Makes[i]
		g.Line = ""
		if !reflect.DeepEqual(g, w) {
			t.Errorf("make %d: got %+v want %+v", i, g, w)
		}
	}
}

func TestExtractDockerAndScripts(t *testing.T) {
	r := Extract(`docker build --pull -f docker/App.Dockerfile -t reg.io/app:$TAG --build-arg BASE=reg.io/base:1 .
docker push reg.io/app:$TAG
docker run --rm -v $PWD:/src -e X=1 reg.io/tools:2 make lint
docker buildx build --platform linux/amd64 --tag=reg.io/x:1 --push ctx
./ci/deploy.sh staging
bash -e scripts/smoke.sh
/usr/bin/true`)
	if len(r.Dockers) != 4 {
		t.Fatalf("dockers: %+v", r.Dockers)
	}
	d := r.Dockers[0]
	if d.Action != "build" || d.Dockerfile != "docker/App.Dockerfile" || d.Context != "." ||
		!reflect.DeepEqual(d.Images, []string{"reg.io/app:$TAG"}) || !reflect.DeepEqual(d.Consumed, []string{"reg.io/base:1"}) {
		t.Errorf("build: %+v", d)
	}
	if r.Dockers[1].Action != "push" || r.Dockers[1].Images[0] != "reg.io/app:$TAG" {
		t.Errorf("push: %+v", r.Dockers[1])
	}
	if r.Dockers[2].Action != "run" || !reflect.DeepEqual(r.Dockers[2].Consumed, []string{"reg.io/tools:2"}) {
		t.Errorf("run: %+v", r.Dockers[2])
	}
	if r.Dockers[3].Action != "build+push" || r.Dockers[3].Images[0] != "reg.io/x:1" || r.Dockers[3].Context != "ctx" {
		t.Errorf("buildx: %+v", r.Dockers[3])
	}
	if len(r.Scripts) != 2 || r.Scripts[0].Path != "ci/deploy.sh" || r.Scripts[1].Path != "scripts/smoke.sh" {
		t.Errorf("scripts: %+v", r.Scripts)
	}
	// make inside docker run is the container's command, not a host make call
	if len(r.Makes) != 0 {
		t.Errorf("makes: %+v", r.Makes)
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
