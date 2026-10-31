package shell

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateNeverCutsACharacter(t *testing.T) {
	if got := Truncate("short", 60); got != "short" {
		t.Errorf("%q", got)
	}
	exact := strings.Repeat("a", 60)
	if got := Truncate(exact, 60); got != exact {
		t.Errorf("a line of exactly the limit is kept: %q", got)
	}
	if got := Truncate(strings.Repeat("a", 61), 60); got != strings.Repeat("a", 57)+"..." {
		t.Errorf("%q", got)
	}
	// 2-, 3- and 4-byte characters cut at every possible offset stay valid UTF-8 and within the limit
	for _, unit := range []string{"é", "日", "🚀"} {
		long := strings.Repeat(unit, 100)
		for n := 4; n < 70; n++ {
			got := Truncate(long, n)
			if !utf8.ValidString(got) || len(got) > n || !strings.HasSuffix(got, "...") {
				t.Fatalf("Truncate(%q×100, %d) = %q", unit, n, got)
			}
		}
	}
}

func TestSummaryIsTheFirstLineThatDoesWork(t *testing.T) {
	script := "\n  # build it\nset -euo pipefail\n  go build ./...  \ngo test ./...\n"
	if got := Summary(script); got != "go build ./..." {
		t.Errorf("%q", got)
	}
	if got := Summary("# only a comment\nset -e\n\n"); got != "" {
		t.Errorf("a script with nothing to run has no summary: %q", got)
	}
	if got := Summary("echo " + strings.Repeat("x", 100)); len(got) != 60 || !strings.HasSuffix(got, "...") {
		t.Errorf("%q", got)
	}
}

func TestDockerCommandsAreWhatTheScannerReadsBack(t *testing.T) {
	cmd := BuildCommand("build/Dockerfile", []string{"reg/a:1", "reg/a:latest"}, []string{"BASE=reg/base:2", "X=1"}, "ctx")
	want := "docker build -f build/Dockerfile -t reg/a:1 -t reg/a:latest --build-arg BASE=reg/base:2 --build-arg X=1 ctx"
	if cmd != want {
		t.Errorf("%q", cmd)
	}
	if got := BuildCommand("", nil, nil, ""); got != "docker build ." {
		t.Errorf("the context defaults to the current directory: %q", got)
	}
	if got := PushCommands([]string{"reg/a:1", "reg/a:2"}); strings.Join(got, "|") != "docker push reg/a:1|docker push reg/a:2" {
		t.Errorf("%q", got)
	}
	// and the scanner understands what was written
	res := Extract(cmd + "\n" + strings.Join(PushCommands([]string{"reg/a:1"}), "\n"))
	if len(res.Dockers) != 2 || res.Dockers[0].Dockerfile != "build/Dockerfile" || res.Dockers[0].Context != "ctx" {
		t.Errorf("%+v", res.Dockers)
	}
}
