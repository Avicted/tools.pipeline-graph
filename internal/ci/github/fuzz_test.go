package github

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/ci"
)

// Workflow files come from untrusted repositories. Reading one must not panic, however broken
// the YAML is. `go test` runs the seeds; to search for more:
// go test ./internal/ci/github -fuzz=FuzzWorkflow -fuzztime=1m
func FuzzWorkflow(f *testing.F) {
	for _, seed := range []string{
		"on: push\njobs:\n  build:\n    runs-on: x\n    steps:\n      - run: make\n",
		"on: [push, pull_request]\njobs:\n  a:\n    needs: [b]\n    uses: o/r/.github/workflows/x.yml@main\n  b:\n    runs-on: x\n    container: img:1\n    services:\n      db:\n        image: pg\n    steps:\n      - uses: actions/upload-artifact@v4\n        with: {name: n}\n",
		"on:\n  schedule:\n    - cron: '0 0 * * *'\n  workflow_run:\n    workflows: [X]\njobs: {}\n",
		"jobs:\n  j:\n    steps:\n      - uses: docker/build-push-action@v5\n        with:\n          tags: |\n            a:1\n            b:2\n          push: ${{ github.ref == 'refs/heads/main' }}\n",
		"", "-", "- - -", "jobs: [", "jobs:\n  a:\n    needs: a\n", "on: &x push\njobs: *x", "{{", "jobs:\n  a: 1",
	} {
		f.Add(seed)
	}
	root := f.TempDir()
	wf := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(wf, 0o755); err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, src string) {
		if err := os.WriteFile(filepath.Join(wf, "ci.yml"), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		_, _ = New().Load(ci.Repo{Name: "r", Root: root})
	})
}
