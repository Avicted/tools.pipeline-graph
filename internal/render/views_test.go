package render

import (
	"testing"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

func TestWithoutMakeLiftsImageEdges(t *testing.T) {
	g := model.New()
	for _, n := range []*model.Node{
		{ID: "b", Kind: model.KindBuild}, {ID: "c", Kind: model.KindBuild},
		{ID: "m1", Kind: model.KindMake}, {ID: "m2", Kind: model.KindMake}, {ID: "img", Kind: model.KindImage},
	} {
		g.AddNode(n)
	}
	g.AddEdge(model.Edge{From: "b", To: "m1", Kind: model.EdgeRuns})
	g.AddEdge(model.Edge{From: "m1", To: "m2", Kind: model.EdgeDepends})
	g.AddEdge(model.Edge{From: "m2", To: "img", Kind: model.EdgeProduces})
	g.AddEdge(model.Edge{From: "c", To: "m2", Kind: model.EdgeRuns})
	g.AddEdge(model.Edge{From: "img", To: "m1", Kind: model.EdgeConsumes})

	out := WithoutMake(g)
	if len(out.Nodes) != 3 {
		t.Fatalf("nodes: %d", len(out.Nodes))
	}
	want := map[model.Edge]bool{
		{From: "b", To: "img", Kind: model.EdgeProduces}: true,
		{From: "c", To: "img", Kind: model.EdgeProduces}: true,
	}
	// b both produces and (via m1) consumes img: the consume edge is kept too
	want[model.Edge{From: "img", To: "b", Kind: model.EdgeConsumes}] = true
	if len(out.Edges) != len(want) {
		t.Fatalf("edges: %+v", out.Edges)
	}
	for _, e := range out.Edges {
		if !want[e] {
			t.Errorf("unexpected edge %+v", e)
		}
	}
}
