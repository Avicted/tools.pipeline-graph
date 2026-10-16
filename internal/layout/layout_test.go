package layout

import (
	"fmt"
	"reflect"
	"testing"

	"pipeline-graph/internal/model"
)

func sample() *model.Graph {
	g := model.New()
	repos := []string{"a", "b", "c"}
	for i := 0; i < 24; i++ {
		g.AddNode(&model.Node{ID: fmt.Sprintf("n%02d", i), Repo: repos[i%3], Label: fmt.Sprint(i)})
	}
	for i := 0; i < 24; i++ {
		for _, j := range []int{i + 1, i + 3, i + 7} {
			if j < 24 && (i*j)%5 != 0 {
				g.AddEdge(model.Edge{From: fmt.Sprintf("n%02d", i), To: fmt.Sprintf("n%02d", j), Kind: model.EdgeSnapshot})
			}
		}
	}
	g.AddEdge(model.Edge{From: "n21", To: "n02", Kind: model.EdgeTrigger}) // closes a cycle n02 -> ... -> n21
	return g
}

func compute(g *model.Graph) Layout {
	size := func(n *model.Node) (float64, float64) {
		return 100 + float64(len(n.ID)*3), 40 + float64(len(n.Label)*10)
	}
	return Compute(g, size, []string{"a", "b", "c"}, func(n *model.Node) string { return n.Repo }, DefaultOptions())
}

func TestLayoutInvariants(t *testing.T) {
	g := sample()
	L := compute(g)
	lane := map[string]Box{}
	for _, l := range L.Lanes {
		lane[l.Name] = l.Box
	}
	for _, a := range g.Nodes {
		ba := L.Nodes[a.ID]
		lb := lane[a.Repo]
		if ba.X < lb.X || ba.Y < lb.Y || ba.X+ba.W > lb.X+lb.W || ba.Y+ba.H > lb.Y+lb.H {
			t.Errorf("%s %+v outside lane %+v", a.ID, ba, lb)
		}
		for _, b := range g.Nodes {
			bb := L.Nodes[b.ID]
			if a.ID < b.ID && ba.X < bb.X+bb.W && bb.X < ba.X+ba.W && ba.Y < bb.Y+bb.H && bb.Y < ba.Y+ba.H {
				t.Errorf("overlap %s %+v and %s %+v", a.ID, ba, b.ID, bb)
			}
		}
	}
	if len(L.Routes) != len(g.Edges) {
		t.Fatalf("routes %d edges %d", len(L.Routes), len(g.Edges))
	}
	back := 0
	for _, r := range L.Routes {
		from, to := L.Nodes[r.Edge.From], L.Nodes[r.Edge.To]
		if from.X+from.W > to.X {
			back++
		}
		first, last := r.Points[0], r.Points[len(r.Points)-1]
		if (first.X != from.X+from.W && first.X != from.X) || (last.X != to.X && last.X != to.X+to.W) {
			t.Errorf("route %v does not start/end at ports", r.Edge)
		}
	}
	if back != 1 {
		t.Errorf("expected exactly the one cycle edge to point backwards, got %d", back)
	}
}

func TestLayoutDeterministic(t *testing.T) {
	a, b := compute(sample()), compute(sample())
	if !reflect.DeepEqual(a, b) {
		t.Error("layout differs between runs")
	}
}
