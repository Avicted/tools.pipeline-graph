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
