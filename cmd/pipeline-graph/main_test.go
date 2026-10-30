package main

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

var fixtures = filepath.Join("..", "..", "test", "fixtures", "repos")

// cli runs the tool in-process and returns its exit code and output.
func cli(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func fixture(name string) string {
	p, _ := filepath.Abs(filepath.Join(fixtures, name))
	return p
}

func TestVersionAndUsage(t *testing.T) {
	if code, out, _ := cli(t, "-version"); code != 0 || !strings.HasPrefix(out, "pipeline-graph ") {
		t.Errorf("-version: %d %q", code, out)
	}
	if code, _, errs := cli(t, "-no-such-flag"); code != 2 || !strings.Contains(errs, "usage: pipeline-graph") {
		t.Errorf("bad flag: %d %q", code, errs)
	}
}

func TestConfigErrorsExitTwo(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("repos: [a]\nbogus: 1\n"), 0o644)
	for name, args := range map[string][]string{
		"no repositories":  {"-out", dir},
		"missing config":   {"-config", filepath.Join(dir, "absent.yaml")},
		"unknown key":      {"-config", bad},
		"unknown provider": {"-out", dir, "-config", writeConfig(t, dir, "repos:\n  - url: "+fixture("service-a")+"\n    provider: jenkins\n")},
	} {
		if code, _, errs := cli(t, args...); code != 2 || !strings.HasPrefix(errs, "pipeline-graph:") {
			t.Errorf("%s: exit %d, stderr %q", name, code, errs)
		}
	}
}

func writeConfig(t *testing.T, dir, content string) string {
	t.Helper()
	f, err := os.CreateTemp(dir, "c-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	return f.Name()
}

func TestLocalReposWriteEveryOutput(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	t.Setenv("SOURCE_DATE_EPOCH", "1767225600")
	code, stdout, errs := cli(t, "-out", out, fixture("platform-base"), fixture("service-a"), fixture("release-train"))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	for _, f := range []string{"pipelines.drawio", "pipelines.vsdx", "pipelines.html", "pipelines.mmd", "pipelines.dot", "graph.json", "warnings.txt"} {
		if st, err := os.Stat(filepath.Join(out, f)); err != nil || st.Size() == 0 {
			t.Errorf("%s not written: %v", f, err)
		}
		if !strings.Contains(stdout, f) {
			t.Errorf("stdout should list %s:\n%s", f, stdout)
		}
	}
	if !strings.Contains(errs, "3 repos,") || !strings.Contains(errs, "build configurations") {
		t.Errorf("summary missing:\n%s", errs)
	}
	html, _ := os.ReadFile(filepath.Join(out, "pipelines.html"))
	if !strings.Contains(string(html), "2026-01-01 00:00 UTC") {
		t.Error("SOURCE_DATE_EPOCH must pin the generated timestamp")
	}
}

func TestFormatsAreSelectable(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, "formats: [json]\nout_dir: "+filepath.Join(dir, "o")+"\nrepos:\n  - "+fixture("service-a")+"\n")
	if code, _, errs := cli(t, "-config", cfg); code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "o"))
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if got := strings.Join(names, ","); got != "graph.json,history,warnings.txt" {
		t.Errorf("only the chosen format (plus its history and warnings) is written, got %s", got)
	}
}

func TestHistoryKeepsOneSnapshotPerDistinctGraph(t *testing.T) {
	dir := t.TempDir()
	cfg := writeConfig(t, dir, "formats: [json]\nout_dir: "+filepath.Join(dir, "o")+"\nrepos:\n  - "+fixture("service-a")+"\n")
	for _, epoch := range []string{"1767225600", "1767312000"} { // same graph on two days
		t.Setenv("SOURCE_DATE_EPOCH", epoch)
		if code, _, errs := cli(t, "-config", cfg); code != 0 {
			t.Fatalf("exit %d\n%s", code, errs)
		}
	}
	snaps, _ := filepath.Glob(filepath.Join(dir, "o", "history", "*.json"))
	if len(snaps) != 1 {
		t.Errorf("an unchanged graph must not add a snapshot, got %v", snaps)
	}
}

func TestStrictFailsOnWarnings(t *testing.T) {
	out := t.TempDir()
	// a repo with no pipeline definitions is a warning, not an error
	empty := t.TempDir()
	if code, _, errs := cli(t, "-out", out, empty); code != 0 {
		t.Fatalf("without -strict: exit %d\n%s", code, errs)
	}
	if code, _, _ := cli(t, "-strict", "-out", out, empty); code != 3 {
		t.Errorf("-strict with warnings: exit %d, want 3", code)
	}
	w, _ := os.ReadFile(filepath.Join(out, "warnings.txt"))
	if !strings.Contains(string(w), "no pipeline definitions found") {
		t.Errorf("warnings.txt: %s", w)
	}
}

func TestFetchFailures(t *testing.T) {
	out := t.TempDir()
	missing := filepath.Join(t.TempDir(), "nope.git")
	cache := writeConfig(t, out, "cache_dir: "+filepath.Join(out, "cache")+"\nout_dir: "+filepath.Join(out, "o")+"\nrepos:\n  - file://"+missing+"\n")
	if code, _, errs := cli(t, "-config", cache); code != 1 || !strings.Contains(errs, "no repository could be fetched") {
		t.Errorf("nothing fetched: exit %d\n%s", code, errs)
	}
	// one bad repo among good ones: everything else is still rendered, but the run fails
	cfg := writeConfig(t, out, "cache_dir: "+filepath.Join(out, "cache")+"\nout_dir: "+filepath.Join(out, "o")+"\nrepos:\n  - "+fixture("service-a")+"\n  - file://"+missing+"\n")
	code, _, errs := cli(t, "-config", cfg)
	if code != 1 || !strings.Contains(errs, "nope") {
		t.Errorf("partial failure: exit %d\n%s", code, errs)
	}
	if _, err := os.Stat(filepath.Join(out, "o", "graph.json")); err != nil {
		t.Errorf("the repos that did fetch are still rendered: %v", err)
	}
}

func TestOffline(t *testing.T) {
	out := t.TempDir()
	cfg := writeConfig(t, out, "cache_dir: "+filepath.Join(out, "cache")+"\nout_dir: "+filepath.Join(out, "o")+"\nrepos:\n  - git@example.invalid:acme/a.git\n")
	if code, _, errs := cli(t, "-offline", "-config", cfg); code != 1 || !strings.Contains(errs, "offline and no cached checkout") {
		t.Errorf("exit %d\n%s", code, errs)
	}
}

// sshRemotes publishes each source directory as a git repository reachable at
// git@github.com:acme/<name>.git. git resolves the host through insteadOf, so
// no network or key is involved, but the URLs are real ones.
func sshRemotes(t *testing.T, tmp string, srcs map[string]string) (urls []string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	remotes := filepath.Join(tmp, "remotes")
	gitcfg := filepath.Join(tmp, "gitconfig")
	os.WriteFile(gitcfg, []byte("[url \"file://"+remotes+"/\"]\n\tinsteadOf = git@github.com:acme/\n"), 0o644)
	t.Setenv("GIT_CONFIG_GLOBAL", gitcfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "t")
	t.Setenv("GIT_AUTHOR_EMAIL", "t@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "t")
	t.Setenv("GIT_COMMITTER_EMAIL", "t@example.com")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(tmp, "xdg"))
	for _, name := range slices.Sorted(maps.Keys(srcs)) {
		dir := filepath.Join(remotes, name+".git")
		copyDir(t, srcs[name], dir)
		for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"add", "-A"}, {"commit", "-q", "-m", "x"}} {
			cmd := exec.Command("git", args...)
			cmd.Dir = dir
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("git %v: %v\n%s", args, err, b)
			}
		}
		urls = append(urls, "git@github.com:acme/"+name+".git")
	}
	return urls
}

// The whole path for a list of ssh URLs.
func TestSSHURLList(t *testing.T) {
	tmp := t.TempDir()
	urls := sshRemotes(t, tmp, map[string]string{
		"platform-base": fixture("platform-base"), "service-a": fixture("service-a"), "release-train": fixture("release-train"),
	})
	out := filepath.Join(tmp, "out")
	args := append([]string{"-out", out}, urls...)
	for i := 0; i < 2; i++ { // clone, then refresh
		code, _, errs := cli(t, args...)
		if code != 0 {
			t.Fatalf("run %d: exit %d\n%s", i, code, errs)
		}
		if !strings.Contains(errs, "fetching 3 repositories") || !strings.Contains(errs, "3 repos,") {
			t.Errorf("run %d summary:\n%s", i, errs)
		}
	}
	g, _ := os.ReadFile(filepath.Join(out, "graph.json"))
	for _, name := range []string{"platform-base", "service-a", "release-train"} {
		if !strings.Contains(string(g), `"name": "`+name+`"`) || !strings.Contains(string(g), "git@github.com:acme/"+name+".git") {
			t.Errorf("graph.json misses repo %s", name)
		}
	}
}

// GitHub Actions repos cloned over ssh link up exactly as local ones do.
func TestGitHubActionsOverSSH(t *testing.T) {
	tmp := t.TempDir()
	src := func(n string) string {
		p, _ := filepath.Abs(filepath.Join("..", "..", "examples", "github", "repos", n))
		return p
	}
	urls := sshRemotes(t, tmp, map[string]string{
		"base-images": src("base-images"), "shared-workflows": src("shared-workflows"),
		"api-service": src("api-service"), "web-app": src("web-app"),
	})
	out := filepath.Join(tmp, "out")
	if code, _, errs := cli(t, append([]string{"-strict", "-out", out}, urls...)...); code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	g, _ := os.ReadFile(filepath.Join(out, "graph.json"))
	if !strings.Contains(string(g), `"from": "bt:shared-workflows/go-checks/test"`) || !strings.Contains(string(g), `"from": "img:ghcr.io/globex/base-go"`) {
		t.Error("cross-repo links through a reusable workflow and an image must survive cloning")
	}
}

func copyDir(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if info.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedAt(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "0")
	if got := generatedAt(); got != "1970-01-01 00:00 UTC" {
		t.Errorf("got %q", got)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "not a number")
	if got := generatedAt(); !strings.HasSuffix(got, " UTC") || strings.HasPrefix(got, "1970") {
		t.Errorf("an invalid value falls back to now: %q", got)
	}
}

func exampleGraph(t *testing.T, config string, args ...string) (edges map[string]bool, warnings string) {
	t.Helper()
	out := filepath.Join(t.TempDir(), "out")
	cfg, _ := filepath.Abs(filepath.Join("..", "..", "examples", config, "pipeline-graph.yaml"))
	if code, _, errs := cli(t, append([]string{"-config", cfg, "-out", out}, args...)...); code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	var g struct {
		Edges []struct{ From, To, Kind string }
	}
	b, err := os.ReadFile(filepath.Join(out, "graph.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &g); err != nil {
		t.Fatal(err)
	}
	edges = map[string]bool{}
	for _, e := range g.Edges {
		edges[e.From+" -"+e.Kind+"-> "+e.To] = true
	}
	w, _ := os.ReadFile(filepath.Join(out, "warnings.txt"))
	return edges, strings.TrimSpace(string(w))
}

// The GitHub Actions example links its repos through workflows, images and workflow_run.
func TestGitHubActionsExample(t *testing.T) {
	edges, warnings := exampleGraph(t, "github", "-strict")
	if warnings != "" {
		t.Errorf("the example is meant to be warning-free:\n%s", warnings)
	}
	for _, want := range []string{
		// api-service calls shared-workflows' reusable workflow: it runs before the calling job
		"bt:shared-workflows/go-checks/test -snapshot-> bt:api-service/ci/checks",
		// needs:, and the job order within a workflow
		"bt:api-service/ci/lint -snapshot-> bt:api-service/ci/checks",
		"bt:api-service/ci/checks -snapshot-> bt:api-service/ci/build",
		// workflow_run: a deploy workflow starts when CI has finished
		"bt:api-service/ci/build -trigger-> bt:api-service/deploy/staging",
		"bt:web-app/ci/image -trigger-> bt:web-app/deploy/publish",
		// upload-artifact / download-artifact
		"bt:web-app/ci/bundle -artifact-> bt:web-app/ci/image",
		// the base image is built in one repo and used in another, by container: and by FROM
		"bt:base-images/images/base-go -produces-> img:ghcr.io/globex/base-go",
		"img:ghcr.io/globex/base-go -consumes-> bt:api-service/ci/lint",
		"img:ghcr.io/globex/base-go -consumes-> bt:api-service/ci/build",
		// images built by build-push-action and by a make target
		"bt:api-service/ci/build -produces-> img:ghcr.io/globex/api",
		"mk:web-app/Makefile:image -produces-> img:ghcr.io/globex/web",
		// run: steps reach the Makefile
		"bt:api-service/deploy/staging -runs-> mk:api-service/Makefile:deploy",
	} {
		if !edges[want] {
			t.Errorf("missing edge: %s", want)
		}
	}
}

// Acme stays exactly as the TeamCity-only tool produced it: one intended warning.
func TestAcmeExampleStillWorks(t *testing.T) {
	edges, warnings := exampleGraph(t, "acme")
	if len(edges) < 20 || strings.Count(warnings, "\n") != 0 || !strings.Contains(warnings, "Legacy_Billing_DeployProd") {
		t.Errorf("%d edges, warnings:\n%s", len(edges), warnings)
	}
}

// One organisation, two CI systems: a TeamCity repo and a GitHub Actions repo in the same map.
func TestMixedSystems(t *testing.T) {
	out := filepath.Join(t.TempDir(), "out")
	gh, _ := filepath.Abs(filepath.Join("..", "..", "examples", "github", "repos", "base-images"))
	code, _, errs := cli(t, "-out", out, fixture("platform-base"), gh)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errs)
	}
	b, _ := os.ReadFile(filepath.Join(out, "graph.json"))
	if !strings.Contains(string(b), `"bt:platform-base/`) || !strings.Contains(string(b), `"bt:base-images/images/base-go"`) {
		t.Errorf("both systems' jobs belong in the graph:\n%s", errs)
	}
}

func TestServe(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pipelines.html"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	go run([]string{"-serve", addr, "-out", dir}, io.Discard, io.Discard)
	var body []byte
	for i := 0; i < 50; i++ {
		if resp, err := http.Get("http://" + addr + "/pipelines.html"); err == nil {
			body, _ = io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if string(body) != "hello" {
		t.Errorf("served %q", body)
	}
}
