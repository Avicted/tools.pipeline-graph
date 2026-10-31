//go:build unix

package integration

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// main wires SIGINT to the run's context. This is the one part the in-process tests cannot
// reach, so it runs the real binary: git hangs on a fake ssh command, Ctrl-C arrives, and the
// run must end promptly with the interrupt status and write nothing.
func TestInterruptEndsTheRun(t *testing.T) {
	root := filepath.Join("..", "..")
	tmp := t.TempDir()
	bin := filepath.Join(tmp, "pipeline-graph")
	run(t, root, "go", "build", "-o", bin, "./cmd/pipeline-graph")

	out := filepath.Join(tmp, "out")
	cfg := filepath.Join(tmp, "config.yaml")
	conf := "cache_dir: " + filepath.Join(tmp, "cache") + "\nout_dir: " + out + "\nrepos:\n  - ssh://git@example.invalid/x.git\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "-config", cfg)
	cmd.Env = append(os.Environ(), "GIT_SSH_COMMAND=sh -c 'exec sleep 60' sh")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }) // the hung ssh as well

	lines := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	for line := range lines {
		if strings.Contains(line, "fetching") {
			break
		}
	}
	time.Sleep(300 * time.Millisecond) // let git start and block
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		_ = cmd.Wait() // an interrupted run exits non-zero; the status is read from ProcessState below
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the run did not stop after SIGINT")
	}
	if code := cmd.ProcessState.ExitCode(); code != 130 {
		t.Errorf("exit status %d, want 130", code)
	}
	if _, err := os.Stat(out); err == nil {
		t.Error("an interrupted run wrote output")
	}
}
