// Package integration runs the real binary against the fixture repositories,
// served as git remotes over file://, and compares the output with golden files.
package integration

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.com", "GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
		"GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.com", "GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	_, self, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(self), "..", "..")
	tmp := t.TempDir()

	bin := filepath.Join(tmp, "pipeline-graph")
	run(t, root, "go", "build", "-o", bin, "./cmd/pipeline-graph")

	// one git repository per fixture, with deterministic commits
	var urls []string
	for _, name := range []string{"platform-base", "service-a", "release-train"} {
		dir := filepath.Join(tmp, "remotes", name)
		copyTree(t, filepath.Join(root, "test", "fixtures", "repos", name), dir)
		run(t, dir, "git", "init", "-q", "-b", "main")
		run(t, dir, "git", "add", "-A")
		run(t, dir, "git", "commit", "-q", "-m", "fixture")
		urls = append(urls, "file://"+dir)
	}
	cfg := filepath.Join(tmp, "config.yaml")
	conf := "cache_dir: " + filepath.Join(tmp, "cache") + "\nout_dir: " + filepath.Join(tmp, "out") + "\nrepos:\n"
	for _, u := range urls {
		conf += "  - " + u + "\n"
	}
	conf += "  - url: " + urls[1] + "\n    name: service-a-main\n    branch: main\n"
	if err := os.WriteFile(cfg, []byte(conf), 0o644); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(tmp, "out")
	for i := 0; i < 2; i++ { // second run exercises the fetch-into-existing-cache path
		cmd := exec.Command(bin, "-config", cfg)
		cmd.Env = append(os.Environ(), "SOURCE_DATE_EPOCH=1767225600")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("run %d: %v\n%s", i, err, b)
		}
	}

	for _, f := range []string{"graph.json", "pipelines.mmd", "pipelines.dot", "warnings.txt"} {
		got, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		// cache paths and temp dirs differ per run
		got = []byte(strings.ReplaceAll(string(got), tmp, "$TMP"))
		golden := filepath.Join(root, "test", "integration", "testdata", f+".golden")
		if *update {
			if err := os.WriteFile(golden, got, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(golden)
		if err != nil {
			t.Fatalf("%v (run with -update to create)", err)
		}
		if string(got) != string(want) {
			t.Errorf("%s differs from golden; run `go test ./test/integration -update` and review the diff", f)
		}
	}

	// The drawings are checked for structure below, but a layout change moves every box
	// without breaking any of that. Their hashes are golden too: a change to what is drawn
	// shows up here, and `-update` accepts it once the pictures have been looked at.
	var sums strings.Builder
	for _, f := range []string{"pipelines.drawio", "pipelines.vsdx"} {
		got, err := os.ReadFile(filepath.Join(out, f))
		if err != nil {
			t.Fatal(err)
		}
		got = []byte(strings.ReplaceAll(string(got), tmp, "$TMP"))
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(got), f)
	}
	sumsGolden := filepath.Join(root, "test", "integration", "testdata", "drawings.sha256.golden")
	if *update {
		if err := os.WriteFile(sumsGolden, []byte(sums.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	} else if want, err := os.ReadFile(sumsGolden); err != nil {
		t.Fatalf("%v (run with -update to create)", err)
	} else if sums.String() != string(want) {
		t.Errorf("the drawings differ from golden:\n%swant:\n%s\nrun `go test ./test/integration -update` after looking at them", sums.String(), want)
	}

	// draw.io: well-formed, unique ids, every reference resolves
	b, err := os.ReadFile(filepath.Join(out, "pipelines.drawio"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Diagrams []struct {
			Name  string `xml:"name,attr"`
			Cells []struct {
				ID     string `xml:"id,attr"`
				Parent string `xml:"parent,attr"`
				Source string `xml:"source,attr"`
				Target string `xml:"target,attr"`
			} `xml:"mxGraphModel>root>mxCell"`
		} `xml:"diagram"`
	}
	if err := xml.Unmarshal(b, &file); err != nil {
		t.Fatalf("drawio is not valid XML: %v", err)
	}
	var pages []string
	for _, d := range file.Diagrams {
		pages = append(pages, d.Name)
		ids := map[string]bool{}
		for _, c := range d.Cells {
			if ids[c.ID] {
				t.Errorf("%s: duplicate cell id %s", d.Name, c.ID)
			}
			ids[c.ID] = true
		}
		for _, c := range d.Cells {
			for _, ref := range []string{c.Parent, c.Source, c.Target} {
				if ref != "" && !ids[ref] {
					t.Errorf("%s: cell %s references missing %s", d.Name, c.ID, ref)
				}
			}
		}
	}
	if want := "Overview,Pipelines + make,platform-base,service-a,release-train,service-a-main"; strings.Join(pages, ",") != want {
		t.Errorf("pages: %q", pages)
	}

	checkVSDX(t, filepath.Join(out, "pipelines.vsdx"), len(pages))
}

// checkVSDX validates the Visio package: required OPC parts, well-formed XML,
// one part per page, and every connector glued to shapes that exist.
func checkVSDX(t *testing.T, path string, wantPages int) {
	t.Helper()
	z, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("vsdx is not a zip: %v", err)
	}
	defer z.Close()
	if z.File[0].Name != "[Content_Types].xml" {
		t.Errorf("first part is %s, want [Content_Types].xml", z.File[0].Name)
	}
	parts := map[string][]byte{}
	for _, f := range z.File {
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		var any struct{}
		if err := xml.Unmarshal(b, &any); err != nil {
			t.Errorf("%s: not well-formed XML: %v", f.Name, err)
		}
		parts[f.Name] = b
	}
	for _, p := range []string{"_rels/.rels", "visio/document.xml", "visio/pages/pages.xml", "visio/pages/_rels/pages.xml.rels", "docProps/core.xml"} {
		if parts[p] == nil {
			t.Errorf("missing part %s", p)
		}
	}
	for i := 1; i <= wantPages; i++ {
		name := fmt.Sprintf("visio/pages/page%d.xml", i)
		var page struct {
			Shapes []struct {
				ID string `xml:"ID,attr"`
			} `xml:"Shapes>Shape"`
			Connects []struct {
				From string `xml:"FromSheet,attr"`
				To   string `xml:"ToSheet,attr"`
			} `xml:"Connects>Connect"`
		}
		if err := xml.Unmarshal(parts[name], &page); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		ids := map[string]bool{}
		for _, s := range page.Shapes {
			if ids[s.ID] {
				t.Errorf("%s: duplicate shape id %s", name, s.ID)
			}
			ids[s.ID] = true
		}
		if len(page.Connects) == 0 {
			t.Errorf("%s: no connectors", name)
		}
		for _, c := range page.Connects {
			if !ids[c.From] || !ids[c.To] {
				t.Errorf("%s: connect %s -> %s references a missing shape", name, c.From, c.To)
			}
		}
	}
}
