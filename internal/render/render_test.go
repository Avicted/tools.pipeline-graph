package render

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

// sample has one node of every kind across three lanes, with awkward characters in labels.
func sample() *model.Graph {
	g := model.New()
	g.Generated = "2026-01-01 00:00 UTC"
	g.Repos = []model.Repo{{Name: "lib", Commit: "0123456789abcdef"}, {Name: "app"}, {Name: "idle"}}
	steps := make([]model.Step, 6)
	for i := range steps {
		steps[i] = model.Step{Type: "script", Summary: "step"}
	}
	for _, n := range []*model.Node{
		{ID: "bt:lib/Pub", Kind: model.KindBuild, Repo: "lib", Label: `Pub "lib" <&>`, Sub: "Lib_Pub", Project: "Lib",
			Steps: steps, Artifacts: []string{"a", "b"},
			Triggers: []model.Trigger{{Type: "vcs"}, {Type: "vcs"}, {Type: "schedule", Detail: "daily"}, {Type: "finishBuild"}, {Type: "manual"}}},
		{ID: "bt:app/All", Kind: model.KindBuild, Repo: "app", Label: "All", Sub: "App_All", Composite: true},
		{ID: "bt:app/Build", Kind: model.KindBuild, Repo: "app", Label: "Build", Sub: "App_Build", Steps: steps[:1], Source: "x.kts:3",
			Params: map[string]string{"k": "v"}},
		{ID: "mk:app/Makefile:image", Kind: model.KindMake, Repo: "app", Label: "image", Sub: "Makefile", Steps: []model.Step{{Type: "recipe", Summary: "docker build"}}},
		{ID: "img:reg/app", Kind: model.KindImage, Repo: "app", Label: "app", Sub: "reg/app"},
		{ID: "ext:Other_X", Kind: model.KindExternal, Label: "Other_X", Sub: "not in analysed repos"},
	} {
		g.AddNode(n)
	}
	for _, e := range []model.Edge{
		{From: "bt:lib/Pub", To: "bt:app/Build", Kind: model.EdgeSnapshot},
		{From: "bt:lib/Pub", To: "bt:app/Build", Kind: model.EdgeArtifact, Label: "dist/**"},
		{From: "ext:Other_X", To: "bt:app/Build", Kind: model.EdgeTrigger, Label: "on success"},
		{From: "bt:app/Build", To: "mk:app/Makefile:image", Kind: model.EdgeRuns, Label: "make"},
		{From: "mk:app/Makefile:image", To: "img:reg/app", Kind: model.EdgeProduces},
		{From: "bt:app/All", To: "bt:app/Build", Kind: model.EdgeSnapshot},
	} {
		g.AddEdge(e)
	}
	g.Sort()
	return g
}

func TestCardOf(t *testing.T) {
	g := sample()
	c := CardOf(g.Node("bt:lib/Pub"))
	if c.Sub != "Lib · Lib_Pub" {
		t.Errorf("sub: %q", c.Sub)
	}
	if len(c.Lines) != maxStepLines+1 || c.Lines[maxStepLines] != "… 2 more steps" {
		t.Errorf("long step lists are cut: %v", c.Lines)
	}
	want := "⎇ on commit|⏱ daily|⚡ manual|▣ 2 artifact rules"
	if got := strings.Join(c.Badges, "|"); got != want {
		t.Errorf("badges (duplicates folded, finishBuild left to the edge):\n%s\nwant\n%s", got, want)
	}
	if b := CardOf(g.Node("bt:app/All")).Badges; len(b) != 1 || b[0] != "◆ composite" {
		t.Errorf("composite: %v", b)
	}
	if one := CardOf(&model.Node{Kind: model.KindBuild, Artifacts: []string{"a"}}); one.Badges[0] != "▣ 1 artifact rule" {
		t.Errorf("singular: %v", one.Badges)
	}
	if m := CardOf(g.Node("mk:app/Makefile:image")); m.Title != "make image" || m.Lines[0] != "docker build" {
		t.Errorf("make: %+v", m)
	}
	if i := CardOf(g.Node("img:reg/app")); !strings.HasSuffix(i.Title, "app") || i.Sub != "reg/app" {
		t.Errorf("image: %+v", i)
	}
}

func TestSizes(t *testing.T) {
	g := sample()
	small, big := g.Node("bt:app/All"), g.Node("bt:lib/Pub")
	w1, h1 := Size(small)
	w2, h2 := Size(big)
	if w1 < 200 || w2 > 320 || h2 <= h1 {
		t.Errorf("sizes: small %vx%v big %vx%v", w1, h1, w2, h2)
	}
	if w, _ := Size(g.Node("img:reg/app")); w < 150 || w > 260 {
		t.Errorf("image width %v", w)
	}
	if _, hv := SizeVSDX(big); hv <= h2 {
		t.Errorf("visio cards are taller than the same card in draw.io: %v vs %v", hv, h2)
	}
}

func TestSubtitle(t *testing.T) {
	got := Subtitle(sample())
	if got != "Generated 2026-01-01 00:00 UTC from 3 repositories: app, idle, lib@01234567" {
		t.Errorf("got %q", got)
	}
}

func TestBuildViews(t *testing.T) {
	g := sample()
	views := BuildViews(g, []string{"overview", "full", "per-repo", "bogus"})
	var ids []string
	for _, v := range views {
		ids = append(ids, v.ID)
		if v.Layout.W <= 0 || v.Layout.H <= 0 || len(v.Layout.Nodes) != len(v.Graph.Nodes) {
			t.Errorf("%s: layout %vx%v for %d nodes", v.ID, v.Layout.W, v.Layout.H, len(v.Graph.Nodes))
		}
	}
	// "idle" has no nodes and gets no page; unknown view names are ignored
	if got := strings.Join(ids, ","); got != "overview,full,repo-lib,repo-app" {
		t.Errorf("views: %s", got)
	}
	if views[0].Graph.Node("mk:app/Makefile:image") != nil {
		t.Error("overview hides make targets")
	}
	repoLib := views[2].Graph
	if repoLib.Node("bt:app/Build") == nil || repoLib.Node("mk:app/Makefile:image") != nil {
		t.Error("a repo page shows neighbours from other repos, but not their make targets")
	}
}

func TestHTML(t *testing.T) {
	g := sample()
	var buf bytes.Buffer
	if err := HTML(&buf, g, BuildViews(g, []string{"overview", "full"}), []string{"app: a warning"}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "/*DATA*/null") {
		t.Error("the data placeholder must be replaced")
	}
	for _, want := range []string{`"bt:lib/Pub"`, `"overview"`, `"app: a warning"`, `"edgeStyles"`, "composite", `"focus:bt:lib/Pub"`, `"kind":"focus"`, `"flow:lib"`} {
		if !strings.Contains(out, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	// labels cannot break out of the <script> element
	if strings.Count(out, "</script>") != strings.Count(viewerTemplate, "</script>") || strings.Contains(out, `<&>`) {
		t.Error("data must be escaped inside the script")
	}
}

func TestDrawIO(t *testing.T) {
	g := sample()
	views := BuildViews(g, []string{"overview", "per-repo"})
	var buf bytes.Buffer
	if err := DrawIO(&buf, g, views); err != nil {
		t.Fatal(err)
	}
	var f struct {
		Diagrams []struct {
			Name  string `xml:"name,attr"`
			Cells []struct {
				ID     string `xml:"id,attr"`
				Value  string `xml:"value,attr"`
				Source string `xml:"source,attr"`
				Target string `xml:"target,attr"`
			} `xml:"mxGraphModel>root>mxCell"`
		} `xml:"diagram"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &f); err != nil {
		t.Fatalf("not well-formed XML: %v", err)
	}
	if len(f.Diagrams) != len(views) {
		t.Fatalf("%d pages for %d views", len(f.Diagrams), len(views))
	}
	for _, d := range f.Diagrams {
		ids := map[string]bool{}
		for _, c := range d.Cells {
			if ids[c.ID] {
				t.Errorf("%s: duplicate cell id %s", d.Name, c.ID)
			}
			ids[c.ID] = true
		}
		for _, c := range d.Cells {
			if (c.Source != "" && !ids[c.Source]) || (c.Target != "" && !ids[c.Target]) {
				t.Errorf("%s: edge %s points at a missing cell", d.Name, c.ID)
			}
		}
	}
	// cell values are HTML, itself escaped into an XML attribute
	if !strings.Contains(buf.String(), "Pub &amp;#34;lib&amp;#34; &amp;lt;&amp;amp;&amp;gt;") {
		t.Error("labels must be escaped twice: as HTML, then as an attribute")
	}
}

func TestVSDX(t *testing.T) {
	g := sample()
	var buf bytes.Buffer
	if err := VSDX(&buf, g, BuildViewsSized(g, []string{"overview", "full"}, SizeVSDX)); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, f := range zr.File {
		have[f.Name] = true
		rc, _ := f.Open()
		var b bytes.Buffer
		b.ReadFrom(rc)
		rc.Close()
		if strings.HasSuffix(f.Name, ".xml") || strings.HasSuffix(f.Name, ".rels") {
			if err := xml.Unmarshal(b.Bytes(), new(struct{})); err != nil {
				t.Errorf("%s is not well-formed: %v", f.Name, err)
			}
		}
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "visio/document.xml", "visio/pages/page1.xml", "visio/pages/page2.xml"} {
		if !have[name] {
			t.Errorf("package lacks %s", name)
		}
	}
}

func TestMermaid(t *testing.T) {
	g := sample()
	var buf bytes.Buffer
	if err := Mermaid(&buf, WithoutMake(g)); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"flowchart LR", `subgraph lane0["lib"]`, ":::composite", ":::ext", ":::image",
		"#quot;lib#quot; #lt;#amp;#gt;", // quotes and markup are escaped
		`==>|"triggers on success"|`,    // trigger edges are bold and labelled
		`-.->|"dist/**"|`,               // artifact dependencies are dashed
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mermaid lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "subgraph lane2") {
		t.Error("lanes without nodes are skipped")
	}
	if strings.Count(out, "linkStyle") != len(WithoutMake(g).Edges) {
		t.Error("every edge gets a style")
	}
}

func TestDOT(t *testing.T) {
	g := sample()
	var buf bytes.Buffer
	if err := DOT(&buf, g); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.HasPrefix(out, "// Generated by pipeline-graph.") || !strings.HasSuffix(strings.TrimSpace(out), "}") || !strings.Contains(out, `\"lib\"`) {
		t.Errorf("dot:\n%s", out)
	}
	if strings.Count(out, "->") < len(g.Edges) {
		t.Error("every edge is drawn")
	}
	if got := dotQuote("a\"b\\c\nd"); got != `"a\"b\\c\nd"` {
		t.Errorf("dotQuote: %s", got)
	}
}

// Labels come from analysed repositories. XML 1.0 forbids most control characters and
// invalid UTF-8 whatever the escaping, so one NUL in a job name made draw.io and Visio
// refuse the whole file.
const hostile = "a\x00b\x1b[31m\x0b\x7f ￾￿ \xff\xfe \"<&>' back\\slash end"

func hostileGraph() *model.Graph {
	g := sample()
	g.Repos = append(g.Repos, model.Repo{Name: hostile, Commit: hostile})
	g.AddNode(&model.Node{ID: "bt:evil/Job", Kind: model.KindBuild, Repo: hostile, Label: hostile, Sub: hostile, Project: hostile,
		Steps:     []model.Step{{Type: "script", Summary: hostile, Script: hostile}},
		Artifacts: []string{hostile}, Params: map[string]string{hostile: hostile},
		Triggers: []model.Trigger{{Type: "schedule", Detail: hostile}}})
	g.AddEdge(model.Edge{From: "bt:lib/Pub", To: "bt:evil/Job", Kind: model.EdgeSnapshot, Label: hostile})
	g.Sort()
	return g
}

func wellFormed(t *testing.T, name string, data []byte) {
	t.Helper()
	d := xml.NewDecoder(bytes.NewReader(data))
	for {
		if _, err := d.Token(); err != nil {
			if !errors.Is(err, io.EOF) {
				t.Errorf("%s is not well-formed XML: %v", name, err)
			}
			return
		}
	}
}

func TestHostileTextKeepsDrawIOWellFormed(t *testing.T) {
	g := hostileGraph()
	views := BuildViews(g, []string{"overview", "full", "per-repo"})
	var buf bytes.Buffer
	if err := DrawIO(&buf, g, views); err != nil {
		t.Fatal(err)
	}
	wellFormed(t, "drawio", buf.Bytes())

	// a page name is one escape away from what the user wrote: no extra backslash from %q
	var f struct {
		Diagrams []struct {
			Name string `xml:"name,attr"`
		} `xml:"diagram"`
	}
	if err := xml.Unmarshal(buf.Bytes(), &f); err != nil {
		return // reported above
	}
	for _, d := range f.Diagrams {
		if strings.Contains(d.Name, `back\\slash`) {
			t.Errorf("page name %q has its backslash doubled", d.Name)
		}
	}
}

func TestHostileTextKeepsVSDXWellFormed(t *testing.T) {
	g := hostileGraph()
	var buf bytes.Buffer
	if err := VSDX(&buf, g, BuildViewsSized(g, []string{"overview", "full", "per-repo"}, SizeVSDX)); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range zr.File {
		if !strings.HasSuffix(f.Name, ".xml") && !strings.HasSuffix(f.Name, ".rels") {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		wellFormed(t, f.Name, b)
		if bytes.Contains(b, []byte(`back\\slash`)) { // the title as %q would write it
			t.Errorf("%s has a doubled backslash", f.Name)
		}
	}
}

func TestXMLEscape(t *testing.T) {
	for in, want := range map[string]string{
		"plain":           "plain",
		`<a href="x">&`:   "&lt;a href=&#34;x&#34;&gt;&amp;",
		"it's":            "it&#39;s",
		"nul\x00gone":     "nulgone",
		"esc\x1b[0m":      "esc[0m",
		"tab\tnl\ncr\r":   "tab\tnl\ncr\r",
		"bad\xffbyte":     "bad�byte",
		"￾￿":              "",
		"emoji 🚀 ünï":     "emoji 🚀 ünï",
		"\x7f\u0085 kept": "\x7f\u0085 kept", // allowed by XML 1.0, only discouraged
	} {
		if got := xmlEscape(in); got != want {
			t.Errorf("xmlEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
