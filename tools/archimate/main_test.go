package main

import (
	"bytes"
	"encoding/xml"
	"errors"
	"flag"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite docs/architecture/model.archimate.xml")

const (
	root      = "../.."
	modelPath = root + "/docs/architecture/model.yaml"
	xmlPath   = root + "/docs/architecture/model.archimate.xml"
)

func loadModel(t *testing.T) *Model {
	t.Helper()
	m, err := load(modelPath)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestModelIsValid(t *testing.T) {
	for _, err := range loadModel(t).validate() {
		t.Error(err)
	}
}

func TestValidateRejects(t *testing.T) {
	m := &Model{
		Elements: []Element{
			{ID: "a", Type: "ApplicationComponent", Name: "a"},
			{ID: "d", Type: "DataObject", Name: "d"},
			{ID: "x", Type: "NoSuchType", Name: "x"},
		},
		Relationships: []Relationship{
			{ID: "r1", Type: "Serving", Source: "d", Target: "a"},                 // not in the allowed list
			{ID: "r2", Type: "Access", Source: "a", Target: "d"},                  // no access type
			{ID: "a", Type: "Association", Source: "a", Target: "ghost"},          // reused id, unknown target
			{ID: "r3", Type: "Composition", Source: "a", Target: "a"},             // same type: fine
			{ID: "r4", Type: "Access", Access: "Write", Source: "a", Target: "d"}, // fine
		},
		Views: []View{{ID: "v", Nodes: []Node{
			{M: "n", El: "a", In: "later"}, // parent not listed first
			{M: "n", El: "d"},              // Mermaid id twice
		}, Rels: []string{"r3"}}}, // a relationship from an element to itself
	}
	var reported []string
	for _, e := range m.validate() {
		reported = append(reported, e.Error())
	}
	got := strings.Join(reported, "\n")
	for _, want := range []string{
		`element "x": unknown type "NoSuchType"`,
		`relationship "r1": DataObject Serving ApplicationComponent is not in the allowed list`,
		`relationship "r2": access must be one of`,
		`relationship "a": id already used by element`,
		`relationship "a": source or target is not an element`,
		`view "v", node "n": parent "later" must be listed before it`,
		`view "v": node "n" or element "d" appears twice`,
		`view "v": relationship "r3" needs two distinct elements of the view`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("validate did not report %q; it reported:\n%s", want, got)
		}
	}
	if strings.Contains(got, `"r4"`) {
		t.Errorf("validate rejected a valid Access relationship:\n%s", got)
	}
}

// TestExchangeFileIsCurrent: the committed XML is what make archimate writes today.
func TestExchangeFileIsCurrent(t *testing.T) {
	got := render(loadModel(t))
	if *update {
		if err := os.WriteFile(xmlPath, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s is stale: run make archimate and commit it", xmlPath)
	}
}

// TestExchangeFileReferences: the XML parses, every identifier is unique, and every
// reference points at the right kind of thing: a node at an element, a connection at
// a relationship and at two nodes of its own view that show that relationship's ends.
func TestExchangeFileReferences(t *testing.T) {
	src, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatal(err)
	}
	kind := map[string]string{}     // identifier -> XML element name
	nodeEl := map[string]string{}   // node -> element it shows
	nodeView := map[string]string{} // node -> view it is in
	ends := map[string][2]string{}  // relationship -> source, target element
	type ref struct{ view, id, rel, src, tgt string }
	var refs []ref
	var nodeRefs [][2]string // node, elementRef
	view := ""
	dec := xml.NewDecoder(bytes.NewReader(src))
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("not well formed: %v", err)
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		at := map[string]string{}
		for _, a := range se.Attr {
			at[a.Name.Local] = a.Value
		}
		if se.Name.Space != namespace {
			t.Errorf("<%s> is not in the ArchiMate namespace", se.Name.Local)
		}
		if id := at["identifier"]; id != "" {
			if prev, dup := kind[id]; dup {
				t.Errorf("identifier %q used by <%s> and <%s>", id, prev, se.Name.Local)
			}
			kind[id] = se.Name.Local
		}
		switch se.Name.Local {
		case "view":
			view = at["identifier"]
		case "relationship":
			ends[at["identifier"]] = [2]string{at["source"], at["target"]}
		case "node":
			nodeEl[at["identifier"]], nodeView[at["identifier"]] = at["elementRef"], view
			nodeRefs = append(nodeRefs, [2]string{at["identifier"], at["elementRef"]})
		case "connection":
			refs = append(refs, ref{view, at["identifier"], at["relationshipRef"], at["source"], at["target"]})
		}
	}
	for _, r := range ends {
		if kind[r[0]] != "element" || kind[r[1]] != "element" {
			t.Errorf("relationship end %v is not an element", r)
		}
	}
	for _, n := range nodeRefs {
		if kind[n[1]] != "element" {
			t.Errorf("node %s shows %q, which is not an element", n[0], n[1])
		}
	}
	for _, c := range refs {
		e, ok := ends[c.rel]
		switch {
		case !ok:
			t.Errorf("connection %s: %q is not a relationship", c.id, c.rel)
		case nodeView[c.src] != c.view || nodeView[c.tgt] != c.view:
			t.Errorf("connection %s: its nodes are not in its view", c.id)
		case nodeEl[c.src] != e[0] || nodeEl[c.tgt] != e[1]:
			t.Errorf("connection %s: its nodes don't show the ends of %s", c.id, c.rel)
		}
	}
}

var (
	mermaidBlock = regexp.MustCompile("(?s)```mermaid\n(.*?)```")
	viewTag      = regexp.MustCompile(`^%% view: ([\w-]+)\n`)
	quoted       = regexp.MustCompile(`"[^"]*"`)
	bracketed    = regexp.MustCompile(`[\[({][^\[\](){}]*[\])}]`)
	word         = regexp.MustCompile(`\w+`)
	erLink       = regexp.MustCompile(`^(\w+)\s+[|}o{]+--[|}o{]+\s+(\w+)\s*:`)
	erEntity     = regexp.MustCompile(`^(\w+)\s*\{$`)
	classDecl    = regexp.MustCompile(`^class\s+(\w+)`)
	classLink    = regexp.MustCompile(`^(\w+)\s+(?:<\|--|<\|\.\.|\.\.>|-->|\*--|o--|--|\.\.)\s+(\w+)`)
	stateLink    = regexp.MustCompile(`^(\[\*\]|\w+)\s*-->\s*(\[\*\]|\w+)`)
	participant  = regexp.MustCompile(`^(?:participant|actor)\s+(\w+)`)
)

// mermaidIDs returns the node ids of a Mermaid diagram: flowchart nodes and
// subgraphs, ER entities, classes, states or sequence participants.
func mermaidIDs(diagram string) []string {
	lines := strings.Split(diagram, "\n")
	ids := map[string]bool{}
	add := func(names ...string) {
		for _, n := range names {
			if n != "[*]" {
				ids[n] = true
			}
		}
	}
	kindLine := ""
	for _, l := range lines {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "%%") {
			kindLine = l
			break
		}
	}
	diagramKind := strings.Fields(kindLine)[0]
	depth := 0 // inside an ER entity or a class body
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" || strings.HasPrefix(l, "%%") || l == kindLine {
			continue
		}
		switch diagramKind {
		case "erDiagram":
			if m := erLink.FindStringSubmatch(l); m != nil && depth == 0 {
				add(m[1], m[2])
			} else if m := erEntity.FindStringSubmatch(l); m != nil {
				add(m[1])
				depth++
			} else if l == "}" {
				depth--
			}
		case "classDiagram":
			if m := classDecl.FindStringSubmatch(l); m != nil && depth == 0 {
				add(m[1])
				if strings.HasSuffix(l, "{") {
					depth++
				}
			} else if l == "}" {
				depth--
			} else if m := classLink.FindStringSubmatch(l); m != nil && depth == 0 {
				add(m[1], m[2])
			}
		case "stateDiagram-v2":
			if m := stateLink.FindStringSubmatch(l); m != nil {
				add(m[1], m[2])
			}
		case "sequenceDiagram":
			if m := participant.FindStringSubmatch(l); m != nil {
				add(m[1])
			}
		case "flowchart":
			first := strings.Fields(l)[0]
			if slices.Contains([]string{"classDef", "class", "style", "direction", "end", "linkStyle"}, first) {
				continue
			}
			l = quoted.ReplaceAllString(l, "")
			for bracketed.MatchString(l) {
				l = bracketed.ReplaceAllString(l, "")
			}
			l = strings.TrimPrefix(l, "subgraph ")
			add(word.FindAllString(l, -1)...)
		}
	}
	return sorted(ids)
}

func sorted(set map[string]bool) []string {
	var out []string
	for k := range set {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestMermaidIDs(t *testing.T) {
	for _, c := range []struct{ diagram, want string }{
		{"flowchart LR\n    a[\"x --> y\"] -- \"label\" --> b((\"z\"))\n    subgraph s[\"S\"]\n    c[(\"d\")] -. \"k\" .-> d{\"e\"}\n    end\n    e([\"f\"]) <--> a --> c\n    classDef go fill:#fff\n    class a,b go\n    style s fill:none", "a b c d e s"},
		{"erDiagram\n    A }o--o{ B : \"x\"\n    C {\n        string kind \"k\"\n    }", "A B C"},
		{"classDiagram\n    direction LR\n    class P {\n        <<interface>>\n        Load(Repo) Pipelines\n    }\n    P <|.. Q\n    P ..> R : x\n    style P fill:#fff", "P Q R"},
		{"stateDiagram-v2\n    direction LR\n    [*] --> A\n    A --> B : x\n    B --> B", "A B"},
		{"sequenceDiagram\n    actor dev as developer\n    participant mk as make\n    dev->>mk: run", "dev mk"},
	} {
		if got := strings.Join(mermaidIDs(c.diagram), " "); got != c.want {
			t.Errorf("mermaidIDs(%q) = %q, want %q", c.diagram, got, c.want)
		}
	}
}

// TestViewsMatchTheDocs: every Mermaid diagram in the architecture docs is tagged
// with a view, every view has its diagram in the doc it names, and the two have
// the same nodes.
func TestViewsMatchTheDocs(t *testing.T) {
	docs, _ := filepath.Glob(filepath.Join(root, "docs", "architecture", "*.md"))
	docs = append(docs, filepath.Join(root, "docs", "architecture.md"))
	drawn := map[string]string{} // view id -> diagram
	in := map[string]string{}    // view id -> doc
	for _, doc := range docs {
		src, err := os.ReadFile(doc)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, doc)
		for i, b := range mermaidBlock.FindAllStringSubmatch(string(src), -1) {
			tag := viewTag.FindStringSubmatch(b[1])
			if tag == nil {
				t.Errorf("%s: Mermaid block %d has no %%%% view: tag on its first line", rel, i+1)
				continue
			}
			if prev, dup := in[tag[1]]; dup {
				t.Errorf("%s: view %q is already drawn in %s", rel, tag[1], prev)
			}
			drawn[tag[1]], in[tag[1]] = b[1], filepath.ToSlash(rel)
		}
	}
	m := loadModel(t)
	for _, v := range m.Views {
		diagram, ok := drawn[v.ID]
		if !ok {
			t.Errorf("view %q: no Mermaid block is tagged with it", v.ID)
			continue
		}
		delete(drawn, v.ID)
		if in[v.ID] != v.Doc {
			t.Errorf("view %q: says doc %s, but is drawn in %s", v.ID, v.Doc, in[v.ID])
		}
		var ids []string
		for _, n := range v.Nodes {
			ids = append(ids, n.M)
		}
		slices.Sort(ids)
		if got := mermaidIDs(diagram); !slices.Equal(got, ids) {
			t.Errorf("view %q: the diagram has nodes %v, model.yaml has %v", v.ID, got, ids)
		}
	}
	for id := range drawn {
		t.Errorf("%s: Mermaid block tagged %q has no view in model.yaml", in[id], id)
	}
}
