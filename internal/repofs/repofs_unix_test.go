//go:build unix

package repofs

import (
	"errors"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestSpecialFilesAreSkippedWithoutBlocking(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o644); err != nil {
		t.Skipf("cannot create a fifo here: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := ReadFile(root, "pipe")
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrNotRegular) {
			t.Fatalf("want ErrNotRegular, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("reading a fifo blocked")
	}
	if Exists(root, "pipe") {
		t.Error("Exists must be false for a fifo")
	}
}
