package fetch

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func TestURLIsNeverAnOption(t *testing.T) {
	remotes, cache := setup(t)
	marker := filepath.Join(remotes, "pwned")
	res := All(context.Background(), []Request{{URL: "--upload-pack=touch " + marker}}, cache, 1, false)
	if res[0].Err == nil {
		t.Error("an option-looking URL must fail as a missing repository")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("the URL was run as a git option")
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

// A URL made only of characters the cache key strips used to give an empty key, so the
// checkout directory was the cache itself and a failed clone deleted all of it.
func TestHostileURLNeverTouchesTheCache(t *testing.T) {
	_, cache := setup(t)
	if err := os.MkdirAll(cache, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(cache, "other-repo-checkout")
	if err := os.WriteFile(keep, []byte("precious"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"@@", "://", "@", "@@@@"} {
		res := All(context.Background(), []Request{{URL: url}}, cache, 1, false)
		if res[0].Err == nil {
			t.Errorf("%q: cloning nonsense must fail", url)
		}
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("%q: a failed clone destroyed the cache: %v", url, err)
		}
	}
}

// git runs a remote helper named git-remote-<x> for the URL "<x>::...". The allowed
// protocols are listed, so a URL cannot make git start a program of the repository's choosing.
func TestUnlistedProtocolsAreRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a shell script")
	}
	remotes, cache := setup(t)
	bin := filepath.Join(remotes, "bin")
	marker := filepath.Join(remotes, "helper-ran")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ntouch " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git-remote-evil"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	res := All(context.Background(), []Request{{URL: "evil::anything"}}, cache, 1, false)
	if res[0].Err == nil {
		t.Error("an unlisted protocol must fail")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("git started a remote helper for a protocol that is not allowed")
	}
}

func TestCacheKeys(t *testing.T) {
	// Requests that differ at all must not share a directory, even when their readable parts match.
	reqs := [][2]string{
		{"a/b", ""}, {"a:b", ""}, {"a_b", ""}, {"x", "main"}, {"x@main", ""}, {"x", ""}, {"x", "dev"},
		{"git@github.com:acme/svc.git", ""}, {"https://github.com/acme/svc.git", ""},
	}
	seen := map[string][2]string{}
	for _, r := range reqs {
		k := cacheKey(r[0], r[1])
		if prev, dup := seen[k]; dup {
			t.Errorf("%q and %q share the key %q", prev, r, k)
		}
		seen[k] = r
		if k == "" || k != filepath.Base(k) || strings.HasPrefix(k, ".") {
			t.Errorf("%q: %q is not one safe path element", r, k)
		}
	}
	// nothing readable survives: still a name, never empty or a dot segment
	for _, u := range []string{"@@", "://", "..", ".", ""} {
		if k := cacheKey(u, ""); !strings.HasPrefix(k, "repo-") {
			t.Errorf("%q: %q", u, k)
		}
	}
	if k := cacheKey("https://github.com/acme/svc.git", ""); !strings.HasPrefix(k, "https_github.com_acme_svc.git-") {
		t.Errorf("the key should stay readable: %q", k)
	}
	if k := cacheKey(strings.Repeat("a/", 100)+"svc", ""); len(k) > 80 {
		t.Errorf("a long URL gives a %d-byte name", len(k))
	}
}

// What is already at the checkout path is not ours: a failed clone must leave it alone.
func TestForeignDirectoryAtTheCheckoutPathSurvives(t *testing.T) {
	remotes, cache := setup(t)
	req := Request{URL: "file://" + filepath.Join(remotes, "missing")}
	dir := filepath.Join(cache, cacheKey(req.URL, req.Branch))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(keep, []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := All(context.Background(), []Request{req}, cache, 1, false)
	if res[0].Err == nil || !strings.Contains(res[0].Err.Error(), "not a git checkout") {
		t.Errorf("want an error that says what is wrong, got %v", res[0].Err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Errorf("a directory this run did not create was deleted: %v", err)
	}
}

func TestCacheDirectoryIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no unix modes on windows")
	}
	remotes, cache := setup(t)
	src := remote(t, remotes, "svc", "one")
	if res := All(context.Background(), []Request{{URL: "file://" + src}}, cache, 1, false); res[0].Err != nil {
		t.Fatal(res[0].Err)
	}
	fi, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("cache dir mode %v: other users could plant a checkout in it", fi.Mode().Perm())
	}
}
