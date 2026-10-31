package repofs

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReadFileInsideTheRoot(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "b.txt"), "hello")
	b, err := ReadFile(root, "a/b.txt")
	if err != nil || string(b) != "hello" {
		t.Fatalf("got %q, %v", b, err)
	}
	// a path that wanders but stays inside is fine
	if b, err := ReadFile(root, "a/../a/./b.txt"); err != nil || string(b) != "hello" {
		t.Fatalf("got %q, %v", b, err)
	}
}

func TestMissingFileIsNotExist(t *testing.T) {
	_, err := ReadFile(t.TempDir(), "nope.txt")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
	if Exists(t.TempDir(), "nope.txt") {
		t.Fatal("Exists reports a missing file")
	}
}

func TestPathsOutsideTheRootAreRefused(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	write(t, filepath.Join(root, "ok.txt"), "ok")
	write(t, filepath.Join(parent, "secret.txt"), "secret")

	for _, rel := range []string{"../secret.txt", "a/../../secret.txt", filepath.Join(parent, "secret.txt"), "/etc/passwd"} {
		b, err := ReadFile(root, rel)
		if !errors.Is(err, ErrOutside) || len(b) != 0 {
			t.Errorf("%q: want ErrOutside and no data, got %q, %v", rel, b, err)
		}
		if Exists(root, rel) {
			t.Errorf("%q: Exists must be false for a path outside the root", rel)
		}
	}
}

func TestSymlinkOutsideTheRootIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on windows")
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repo")
	write(t, filepath.Join(parent, "secret.txt"), "secret")
	write(t, filepath.Join(root, "inside.txt"), "inside")
	if err := os.Symlink(filepath.Join(parent, "secret.txt"), filepath.Join(root, "leak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../secret.txt", filepath.Join(root, "relleak.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(parent, filepath.Join(root, "dirleak")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "alias.txt")); err != nil {
		t.Fatal(err)
	}

	for _, rel := range []string{"leak.txt", "relleak.txt", "dirleak/secret.txt"} {
		if b, err := ReadFile(root, rel); err == nil || len(b) != 0 {
			t.Errorf("%q: a symlink out of the repo was followed: %q, %v", rel, b, err)
		}
	}
	// a symlink that stays inside the repository still works
	if b, err := ReadFile(root, "alias.txt"); err != nil || string(b) != "inside" {
		t.Errorf("inner symlink: %q, %v", b, err)
	}
}

func TestDirectoryIsNotAFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadFile(root, "d"); !errors.Is(err, ErrNotRegular) {
		t.Fatalf("want ErrNotRegular, got %v", err)
	}
}

func TestOversizedFileIsRefused(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "big"), strings.Repeat("x", MaxFileSize+1))
	write(t, filepath.Join(root, "exact"), strings.Repeat("x", MaxFileSize))
	b, err := ReadFile(root, "big")
	if !errors.Is(err, ErrTooLarge) || len(b) != 0 {
		t.Fatalf("want ErrTooLarge and no data, got %d bytes, %v", len(b), err)
	}
	if b, err := ReadFile(root, "exact"); err != nil || len(b) != MaxFileSize {
		t.Fatalf("a file of exactly the limit must load: %d bytes, %v", len(b), err)
	}
}

func TestOpenReturnsAReaderOverTheSameChecks(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "f"), "line1\nline2\n")
	r, err := Open(root, "f")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil || buf.String() != "line1\nline2\n" {
		t.Fatalf("got %q, %v", buf.String(), err)
	}
	if _, err := Open(root, "../x"); !errors.Is(err, ErrOutside) {
		t.Fatalf("want ErrOutside, got %v", err)
	}
}

func TestReasonDropsThePathPrefix(t *testing.T) {
	_, err := ReadFile(t.TempDir(), "../x")
	if got := Reason(err); got != ErrOutside.Error() {
		t.Errorf("got %q", got)
	}
	if got := Reason(errors.New("plain")); got != "plain" {
		t.Errorf("got %q", got)
	}
}
