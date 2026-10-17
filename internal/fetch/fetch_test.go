package fetch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitEnv isolates git from the developer's own config and identity.
func gitEnv(t *testing.T, cfg string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	for k, v := range map[string]string{"NAME": "t", "EMAIL": "t@example.com"} {
		t.Setenv("GIT_AUTHOR_"+k, v)
		t.Setenv("GIT_COMMITTER_"+k, v)
	}
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// remote creates a git repository holding one file and returns its path.
func remote(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	sh(t, p, "init", "-q", "-b", "main")
	commit(t, p, "f.txt", content)
	return p
}

func commit(t *testing.T, repo, file, content string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(repo, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, repo, "add", "-A")
	sh(t, repo, "commit", "-q", "-m", content)
	return sh(t, repo, "rev-parse", "HEAD")
}

func setup(t *testing.T) (remotes, cache string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	tmp := t.TempDir()
	remotes = filepath.Join(tmp, "remotes")
	gitEnv(t, filepath.Join(tmp, "gitconfig"))
	return remotes, filepath.Join(tmp, "cache")
}

// A list of ssh-style URLs, resolved to local repositories by git itself.
func TestAllSSHURLs(t *testing.T) {
	remotes, cache := setup(t)
	for _, name := range []string{"service-a", "service-b", "platform-base"} {
		remote(t, remotes, name+".git", name)
	}
	cfg := "[url \"file://" + remotes + "/\"]\n\tinsteadOf = git@github.com:acme/\n" +
		"[url \"file://" + remotes + "/\"]\n\tinsteadOf = ssh://git@git.example.com/acme/\n"
	if err := os.WriteFile(os.Getenv("GIT_CONFIG_GLOBAL"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	reqs := []Request{
		{URL: "git@github.com:acme/service-a.git"},
		{URL: "ssh://git@git.example.com/acme/service-b.git"},
		{URL: "git@github.com:acme/platform-base.git", Branch: "main"},
		{URL: "git@github.com:acme/service-a.git"}, // duplicate: one checkout
	}
	res := All(context.Background(), reqs, cache, 2, false)
	if len(res) != len(reqs) {
		t.Fatalf("got %d results for %d requests", len(res), len(reqs))
	}
	for i, r := range res {
		if r.Err != nil {
			t.Fatalf("%s: %v", reqs[i].URL, r.Err)
		}
		if len(r.Commit) != 40 {
			t.Errorf("%s: commit %q", reqs[i].URL, r.Commit)
		}
		if _, err := os.Stat(filepath.Join(r.Dir, "f.txt")); err != nil {
			t.Errorf("%s: checkout lacks the file: %v", reqs[i].URL, err)
		}
	}
	if res[0].Dir != res[3].Dir || res[0].Commit != res[3].Commit {
		t.Errorf("duplicate requests must share a checkout: %+v vs %+v", res[0], res[3])
	}
	if res[0].Dir == res[1].Dir {
		t.Error("different repositories must not share a checkout")
	}
}

func TestOneFailureDoesNotStopTheRest(t *testing.T) {
	remotes, cache := setup(t)
	good := remote(t, remotes, "good", "x")
	res := All(context.Background(), []Request{
		{URL: "file://" + filepath.Join(remotes, "missing")},
		{URL: "file://" + good},
	}, cache, 1, false)
	if res[0].Err == nil {
		t.Error("cloning a missing repository must fail")
	}
	if res[1].Err != nil {
		t.Errorf("the good repository must still be fetched: %v", res[1].Err)
	}
	if entries, _ := os.ReadDir(cache); len(entries) != 1 {
		t.Errorf("a failed clone must not leave a half checkout behind, cache has %d entries", len(entries))
	}
}

func TestRefreshPicksUpNewCommits(t *testing.T) {
	remotes, cache := setup(t)
	src := remote(t, remotes, "svc", "one")
	req := []Request{{URL: "file://" + src}}
	first := All(context.Background(), req, cache, 1, false)[0]
	if first.Err != nil {
		t.Fatal(first.Err)
	}
	second := commit(t, src, "f.txt", "two")
	// an untracked leftover in the cache must not survive a refresh
	stray := filepath.Join(first.Dir, "stray.txt")
	if err := os.WriteFile(stray, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	got := All(context.Background(), req, cache, 1, false)[0]
	if got.Err != nil {
		t.Fatal(got.Err)
	}
	if got.Commit != second {
		t.Errorf("commit %s, want the new %s", got.Commit, second)
	}
	if _, err := os.Stat(stray); err == nil {
		t.Error("refresh must clean untracked files")
	}
}

func TestBranch(t *testing.T) {
	remotes, cache := setup(t)
	src := remote(t, remotes, "svc", "main")
	sh(t, src, "checkout", "-q", "-b", "release")
	rel := commit(t, src, "f.txt", "release")
	sh(t, src, "checkout", "-q", "main")
	req := []Request{{URL: "file://" + src, Branch: "release"}}
	for i := 0; i < 2; i++ { // clone, then refresh
		r := All(context.Background(), req, cache, 1, false)[0]
		if r.Err != nil || r.Commit != rel {
			t.Fatalf("run %d: %+v, want commit %s", i, r, rel)
		}
	}
	main := All(context.Background(), []Request{{URL: "file://" + src}}, cache, 1, false)[0]
	if main.Dir == "" || main.Dir == All(context.Background(), req, cache, 1, true)[0].Dir {
		t.Error("a branch must have its own checkout")
	}
}

func TestOffline(t *testing.T) {
	remotes, cache := setup(t)
	src := remote(t, remotes, "svc", "one")
	req := []Request{{URL: "file://" + src}}
	if r := All(context.Background(), req, cache, 1, true)[0]; r.Err == nil || !strings.Contains(r.Err.Error(), "offline") {
		t.Errorf("offline without a cache must fail, got %+v", r)
	}
	first := All(context.Background(), req, cache, 1, false)[0]
	commit(t, src, "f.txt", "two")
	r := All(context.Background(), req, cache, 1, true)[0]
	if r.Err != nil || r.Commit != first.Commit {
		t.Errorf("offline must reuse the cached commit %s, got %+v", first.Commit, r)
	}
}

func TestLocalDirectoryIsUsedAsIs(t *testing.T) {
	remotes, cache := setup(t)
	src := remote(t, remotes, "svc", "one")
	want := sh(t, src, "rev-parse", "HEAD")
	r := All(context.Background(), []Request{{URL: src}}, cache, 1, false)[0]
	if r.Err != nil || r.Dir != src || r.Commit != want {
		t.Errorf("got %+v, want dir %s commit %s", r, src, want)
	}
	if _, err := os.Stat(cache); err == nil {
		t.Error("a local directory must not be cloned")
	}
	// not a git repo: still usable, just without a commit
	plain := t.TempDir()
	if r := All(context.Background(), []Request{{URL: plain}}, cache, 1, false)[0]; r.Err != nil || r.Dir != plain || r.Commit != "" {
		t.Errorf("plain directory: %+v", r)
	}
}

func TestIsLocal(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	os.WriteFile(file, nil, 0o644)
	for url, want := range map[string]bool{
		dir:                           true,
		file:                          false, // a file is not a checkout
		filepath.Join(dir, "missing"): false,
		"git@github.com:acme/a.git":   false,
		"ssh://git@host/acme/a.git":   false,
		"https://host/acme/a.git":     false,
		"file://" + dir:               false, // goes through git
		"user@" + dir:                 false,
	} {
		if got := IsLocal(url); got != want {
			t.Errorf("IsLocal(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestGitDoesNotPrompt(t *testing.T) {
	t.Setenv("GIT_SSH_COMMAND", "")
	_, err := git(context.Background(), t.TempDir(), "--version")
	if err != nil {
		t.Skip("git not installed")
	}
	// an invalid command must surface git's own message
	_, err = git(context.Background(), t.TempDir(), "no-such-subcommand")
	if err == nil || !strings.Contains(err.Error(), "no-such-subcommand") {
		t.Errorf("error should name the command: %v", err)
	}
}
