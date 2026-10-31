// Package fetch makes shallow checkouts of the configured repositories using
// the system git (and therefore the user's ssh agent / keys).
package fetch

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

type Request struct {
	URL    string
	Branch string
}

type Result struct {
	Dir    string
	Commit string
	Err    error
}

var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// allowedProtocols are the transports git may use for a configured URL. Listing them
// keeps a URL such as "evil::x" from making git start a remote helper of that name.
const allowedProtocols = "http:https:ssh:git:file"

// cacheKey names the checkout of url@branch: a readable tail of the URL, so a person can
// tell the directories apart, and a hash of the exact request, so two requests never
// share one and the name is never empty or a path component like "..".
func cacheKey(url, branch string) string {
	sum := sha256.Sum256([]byte(url + "\x00" + branch))
	name := strings.Trim(reUnsafe.ReplaceAllString(url, "_"), "_.")
	if len(name) > 48 {
		name = name[len(name)-48:]
	}
	if name == "" {
		name = "repo"
	}
	return name + "-" + hex.EncodeToString(sum[:6])
}

// IsLocal reports whether url refers to an existing local directory (used as-is, no clone).
func IsLocal(url string) bool {
	if strings.Contains(url, "://") || strings.Contains(url, "@") {
		return false
	}
	st, err := os.Stat(url)
	return err == nil && st.IsDir()
}

// All fetches every request into cacheDir with limited concurrency.
// offline skips network access and uses existing checkouts.
func All(ctx context.Context, reqs []Request, cacheDir string, concurrency int, offline bool) []Result {
	// identical requests share one checkout
	results := map[Request]Result{}
	var todo []Request
	for _, r := range reqs {
		if _, dup := results[r]; !dup {
			results[r] = Result{}
			todo = append(todo, r)
		}
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	var mu sync.Mutex
	for _, r := range todo {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			res := one(ctx, r, cacheDir, offline)
			mu.Lock()
			results[r] = res
			mu.Unlock()
		}()
	}
	wg.Wait()
	out := make([]Result, len(reqs))
	for i, r := range reqs {
		out[i] = results[r]
	}
	return out
}

func one(ctx context.Context, r Request, cacheDir string, offline bool) Result {
	if IsLocal(r.URL) {
		dir, err := filepath.Abs(r.URL)
		if err != nil {
			return Result{Err: err}
		}
		commit, _ := git(ctx, dir, "rev-parse", "HEAD") // a plain directory has no commit
		return Result{Dir: dir, Commit: commit}
	}
	dir := filepath.Join(cacheDir, cacheKey(r.URL, r.Branch))
	_, statErr := os.Stat(filepath.Join(dir, ".git"))
	exists := statErr == nil
	switch {
	case offline && !exists:
		return Result{Err: fmt.Errorf("offline and no cached checkout at %s", dir)}
	case offline:
	case exists:
		ref := r.Branch
		if ref == "" {
			ref = "HEAD"
		}
		if _, err := git(ctx, dir, "fetch", "--depth", "1", "--quiet", "--", "origin", ref); err != nil {
			return Result{Err: err}
		}
		if _, err := git(ctx, dir, "reset", "--hard", "--quiet", "FETCH_HEAD"); err != nil {
			return Result{Err: err}
		}
		if _, err := git(ctx, dir, "clean", "-fdxq"); err != nil {
			return Result{Err: err}
		}
	default:
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return Result{Err: err}
		}
		// Only a directory this run creates may be removed again. Anything already
		// there is not ours: refuse it rather than clone into it or delete it.
		fresh := false
		switch entries, err := os.ReadDir(dir); {
		case errors.Is(err, fs.ErrNotExist):
			fresh = true
		case err != nil:
			return Result{Err: err}
		case len(entries) > 0:
			return Result{Err: fmt.Errorf("%s exists but is not a git checkout; remove it to fetch again", dir)}
		}
		args := []string{"clone", "--depth", "1", "--quiet", "--no-tags"}
		if r.Branch != "" {
			args = append(args, "--branch", r.Branch)
		}
		// "--" so a URL starting with "-" is never read as a git option
		args = append(args, "--", r.URL, dir)
		if _, err := git(ctx, "", args...); err != nil {
			if fresh {
				os.RemoveAll(dir)
			}
			return Result{Err: err}
		}
	}
	commit, err := git(ctx, dir, "rev-parse", "HEAD")
	return Result{Dir: dir, Commit: commit, Err: err}
}

func git(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if os.Getenv("GIT_ALLOW_PROTOCOL") == "" {
		cmd.Env = append(cmd.Env, "GIT_ALLOW_PROTOCOL="+allowedProtocols)
	}
	if os.Getenv("GIT_SSH_COMMAND") == "" {
		// never hang on a password / host-key prompt
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND=ssh -o BatchMode=yes")
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}
