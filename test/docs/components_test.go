// Package docs checks that the architecture diagrams still describe the code.
package docs

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	root   = "../.."
	module = "github.com/Avicted/tools.pipeline-graph/"
)

// mermaidView returns the body of the Mermaid block tagged `%% view: <id>` in a doc.
func mermaidView(t *testing.T, doc, id string) string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(root, doc))
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range regexp.MustCompile("(?s)```mermaid\n(.*?)```").FindAllStringSubmatch(string(src), -1) {
		if strings.HasPrefix(block[1], "%% view: "+id+"\n") {
			return block[1]
		}
	}
	t.Fatalf("%s: no Mermaid block tagged %%%% view: %s", doc, id)
	return ""
}

var plainEdge = regexp.MustCompile(`(?m)^\s*(\w+)\s*-->\s*(\w+)\s*$`)

// edges returns the unlabelled `a --> b` arrows of a diagram, one per line.
func edges(diagram string) []string {
	var out []string
	for _, m := range plainEdge.FindAllStringSubmatch(diagram, -1) {
		out = append(out, m[1]+" --> "+m[2])
	}
	slices.Sort(out)
	return out
}

func sorted(set map[string]bool) []string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func compare(t *testing.T, doc string, drawn, real []string) {
	t.Helper()
	for _, e := range real {
		if !slices.Contains(drawn, e) {
			t.Errorf("%s: the code has %s, the diagram doesn't", doc, e)
		}
	}
	for _, e := range drawn {
		if !slices.Contains(real, e) {
			t.Errorf("%s: the diagram has %s, the code doesn't", doc, e)
		}
	}
}

// goNode names the diagram node of a directory: cmd/* is "cmd", internal/x/... is "x".
func goNode(dir string) string {
	parts := strings.Split(filepath.ToSlash(dir), "/")
	if parts[0] == "cmd" {
		return "cmd"
	}
	return parts[1]
}

// TestCLIPackageDiagram: the imports between our Go packages are exactly the arrows
// in docs/architecture/cli.md, with subpackages (ci/teamcity) drawn as their parent.
func TestCLIPackageDiagram(t *testing.T) {
	real := map[string]bool{}
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, filepath.Dir(path))
			from := goNode(rel)
			for _, imp := range f.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				if !strings.HasPrefix(p, module) {
					continue
				}
				if to := goNode(strings.TrimPrefix(p, module)); to != from {
					real[from+" --> "+to] = true
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	doc := "docs/architecture/cli.md"
	compare(t, doc, edges(mermaidView(t, doc, "cli-packages")), sorted(real))
}

var (
	rustPath = regexp.MustCompile(`\b(crate|super)::(\{[^}]*\}|\w+)(?:::(\{[^}]*\}|\w+))?`)
	egui     = regexp.MustCompile(`\b(egui|eframe)\b`)
)

// rustNode names the diagram node of a module: every ui module is "ui", core::x is "core_x".
func rustNode(module string) string {
	top, sub, _ := strings.Cut(module, "::")
	if top == "core" && sub != "" {
		return "core_" + sub
	}
	return top
}

// TestViewerModuleDiagram: the `use`s between the viewer's modules are exactly the
// arrows in docs/architecture/viewer.md, and core never names ui, egui or eframe.
func TestViewerModuleDiagram(t *testing.T) {
	src := filepath.Join(root, "viewer", "src")
	real := map[string]bool{}
	err := filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".rs") {
			return err
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		code, _, _ := strings.Cut(string(body), "#[cfg(test)]")
		rel, _ := filepath.Rel(src, strings.TrimSuffix(path, ".rs"))
		parts := strings.Split(filepath.ToSlash(rel), "/")
		self, parent := parts[0], parts[0] // main, lib, or a mod.rs
		if len(parts) == 2 && parts[1] != "mod" {
			self = parts[0] + "::" + parts[1]
		}
		if parts[0] == "main" {
			// The binary reaches the library as pipeline_graph_viewer::ui.
			code = strings.ReplaceAll(code, "pipeline_graph_viewer::", "crate::")
		}
		if strings.HasPrefix(self, "core") && (egui.Match(body) || strings.Contains(string(body), "crate::ui")) {
			t.Errorf("%s: core must not use ui, egui or eframe", path)
		}
		for _, m := range rustPath.FindAllStringSubmatch(code, -1) {
			var targets []string
			switch {
			case m[1] == "crate" && strings.HasPrefix(m[3], "{"):
				for n := range strings.SplitSeq(strings.Trim(m[3], "{}"), ",") {
					targets = append(targets, m[2]+"::"+strings.TrimSpace(n))
				}
			case m[1] == "crate":
				targets = append(targets, m[2]+"::"+m[3])
			default: // super:: names a sibling module
				for n := range strings.SplitSeq(strings.Trim(m[2], "{}"), ",") {
					n, _, _ = strings.Cut(strings.TrimSpace(n), "::")
					if n != "self" && n != "*" && n != "" {
						targets = append(targets, parent+"::"+n)
					}
				}
			}
			for _, to := range targets {
				if a, b := rustNode(self), rustNode(strings.TrimSuffix(to, "::")); a != b {
					real[a+" --> "+b] = true
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	doc := "docs/architecture/viewer.md"
	compare(t, doc, edges(mermaidView(t, doc, "viewer-modules")), sorted(real))
}
