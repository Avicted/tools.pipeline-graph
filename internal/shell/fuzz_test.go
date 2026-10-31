package shell

import (
	"reflect"
	"testing"
)

// Step text comes from untrusted repositories. Whatever it holds, the scanner must neither
// panic nor give a different answer the second time. `go test` runs the seeds; to search
// for more: go test ./internal/shell -fuzz=FuzzExtract -fuzztime=1m
func FuzzExtract(f *testing.F) {
	for _, seed := range []string{
		"make -C svc build TAG=1",
		"cd a && make -f x.mk all; docker build -t r/a:1 --build-arg B=r/b -f d/Dockerfile .",
		"docker run --rm -v $PWD:/w r/img:1 ./check.sh \\\n  --flag",
		"./ci/build.sh $(MAKE) 'quoted && make no' \"x $(y) z\"",
		"# make comment\nset -e\n   \n\tdocker push $IMAGE:${TAG}",
		"make -C", "docker", "docker build -f", "$(", "'", "\"", "\\", "&& ||", "cd", "cd ..",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, script string) {
		a, b := Extract(script), Extract(script)
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("not deterministic for %q", script)
		}
		for _, w := range splitWords(script) {
			_ = w
		}
		_ = expandArgs(script, map[string]string{"A": "x", "AB": "y"})
		_ = DockerfileFrom(script, nil)
		_ = Summary(script)
		_ = Truncate(script, 20)
	})
}
