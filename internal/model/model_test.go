package model

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestAddNodeKeepsTheFirst(t *testing.T) {
	g := New()
	first := g.AddNode(&Node{ID: "a", Label: "one"})
	if again := g.AddNode(&Node{ID: "a", Label: "two"}); again != first || again.Label != "one" || len(g.Nodes) != 1 {
		t.Errorf("duplicate id must return the stored node, got %+v", again)
	}
	if g.Node("missing") != nil {
		t.Error("unknown id")
	}
}

func TestAddEdgeDedupesAndDropsSelfLoops(t *testing.T) {
	g := New()
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeSnapshot})
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeSnapshot})
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeArtifact}) // another kind is another edge
	g.AddEdge(Edge{From: "a", To: "a", Kind: EdgeSnapshot})
	if len(g.Edges) != 2 {
		t.Errorf("edges: %+v", g.Edges)
	}
}

func TestZeroValueGraphWorks(t *testing.T) {
	// a graph read back from JSON has no indexes yet
	var g Graph
	if err := json.Unmarshal([]byte(`{"nodes":[{"id":"a","kind":"build","label":"A"}],"edges":[{"from":"a","to":"b","kind":"runs"}]}`), &g); err != nil {
		t.Fatal(err)
	}
	if g.Node("a") == nil {
		t.Error("lookup must index lazily")
	}
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeRuns})
	if len(g.Edges) != 1 {
		t.Errorf("existing edges must count as seen: %+v", g.Edges)
	}
}

func TestSortIsDeterministic(t *testing.T) {
	g := New()
	for _, id := range []string{"c", "a", "b"} {
		g.AddNode(&Node{ID: id})
	}
	g.AddEdge(Edge{From: "b", To: "c", Kind: EdgeRuns, Label: "z"})
	g.AddEdge(Edge{From: "b", To: "c", Kind: EdgeRuns, Label: "y"})
	g.AddEdge(Edge{From: "b", To: "c", Kind: EdgeCalls})
	g.AddEdge(Edge{From: "b", To: "a", Kind: EdgeRuns})
	g.AddEdge(Edge{From: "a", To: "c", Kind: EdgeRuns})
	g.Sort()
	if g.Nodes[0].ID != "a" || g.Nodes[2].ID != "c" {
		t.Errorf("nodes: %v", g.Nodes)
	}
	var got []string
	for _, e := range g.Edges {
		got = append(got, e.From+e.To+string(e.Kind)+e.Label)
	}
	want := []string{"acruns", "baruns", "bccalls", "bcrunsy", "bcrunsz"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("edges: %v, want %v", got, want)
		}
	}
}

func TestFilter(t *testing.T) {
	g := New()
	g.Generated = "now"
	g.Repos = []Repo{{Name: "r"}}
	g.AddNode(&Node{ID: "a", Kind: KindBuild})
	g.AddNode(&Node{ID: "b", Kind: KindMake})
	g.AddNode(&Node{ID: "c", Kind: KindBuild})
	g.AddEdge(Edge{From: "a", To: "b", Kind: EdgeRuns})
	g.AddEdge(Edge{From: "a", To: "c", Kind: EdgeSnapshot})
	out := g.Filter(func(n *Node) bool { return n.Kind == KindBuild })
	if len(out.Nodes) != 2 || len(out.Edges) != 1 || out.Edges[0].To != "c" || out.Generated != "now" || len(out.Repos) != 1 {
		t.Errorf("filtered: %+v", out)
	}
	if len(g.Nodes) != 3 {
		t.Error("the source graph must be untouched")
	}
}

func TestWriteJSON(t *testing.T) {
	g := New()
	g.AddNode(&Node{ID: "a", Kind: KindBuild, Label: "A"})
	var buf bytes.Buffer
	if err := g.WriteJSON(&buf); err != nil {
		t.Fatal(err)
	}
	var back Graph
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil || len(back.Nodes) != 1 || back.Nodes[0].Label != "A" {
		t.Errorf("round trip: %v %+v", err, back)
	}
}
