package render

import (
	_ "embed"
	"encoding/json"
	"io"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

//go:embed viewer.html
var viewerTemplate string

type htmlNode struct {
	ID   string  `json:"id"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
	W    float64 `json:"w"`
	H    float64 `json:"h"`
	Card Card    `json:"card"`
	Kind string  `json:"kind"` // CSS class; colours come from the page theme
	Lane string  `json:"lane"`
}

type htmlEdge struct {
	From   string       `json:"from"`
	To     string       `json:"to"`
	Kind   string       `json:"kind"`
	Label  string       `json:"label"`
	Points [][2]float64 `json:"points"`
}

type htmlLane struct {
	Name string     `json:"name"`
	Box  [4]float64 `json:"box"`
}

type htmlView struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	W     float64    `json:"w"`
	H     float64    `json:"h"`
	Lanes []htmlLane `json:"lanes"`
	Nodes []htmlNode `json:"nodes"`
	Edges []htmlEdge `json:"edges"`
}

type htmlEdgeStyle struct {
	Kind string `json:"kind"`
	edgeStyle
}

type htmlData struct {
	Title     string                 `json:"title"`
	Subtitle  string                 `json:"subtitle"`
	Generated string                 `json:"generated"`
	Repos     []model.Repo           `json:"repos"`
	Views     []htmlView             `json:"views"`
	Nodes     map[string]*model.Node `json:"nodes"`
	Edges     []model.Edge           `json:"edges"` // the full graph, for neighbours outside the current view
	Styles    []htmlEdgeStyle        `json:"edgeStyles"`
	Warnings  []string               `json:"warnings"`
}

// HTML writes a single self-contained interactive page (no external assets).
func HTML(w io.Writer, g *model.Graph, views []View, warnings []string) error {
	d := htmlData{
		Title: "Pipeline map", Subtitle: Subtitle(g), Generated: g.Generated, Repos: g.Repos,
		Nodes: map[string]*model.Node{}, Edges: g.Edges, Warnings: warnings,
	}
	for _, n := range g.Nodes {
		d.Nodes[n.ID] = n
	}
	for _, k := range edgeOrder {
		d.Styles = append(d.Styles, htmlEdgeStyle{string(k), edgeStyles[k]})
	}
	for _, v := range views {
		hv := htmlView{ID: v.ID, Title: v.Title, W: v.Layout.W, H: v.Layout.H}
		for _, l := range v.Layout.Lanes {
			hv.Lanes = append(hv.Lanes, htmlLane{Name: l.Name, Box: [4]float64{l.Box.X, l.Box.Y, l.Box.W, l.Box.H}})
		}
		for _, n := range v.Graph.Nodes {
			b := v.Layout.Nodes[n.ID]
			kind := string(n.Kind)
			if n.Composite {
				kind = "composite"
			}
			hv.Nodes = append(hv.Nodes, htmlNode{ID: n.ID, X: b.X, Y: b.Y, W: b.W, H: b.H, Card: CardOf(n), Kind: kind, Lane: laneOf(n)})
		}
		for _, r := range v.Layout.Routes {
			he := htmlEdge{From: r.Edge.From, To: r.Edge.To, Kind: string(r.Edge.Kind), Label: r.Edge.Label}
			for _, p := range r.Points {
				he.Points = append(he.Points, [2]float64{p.X, p.Y})
			}
			hv.Edges = append(hv.Edges, he)
		}
		d.Views = append(d.Views, hv)
	}
	js, err := json.Marshal(d) // escapes <, > and & so it is safe inside <script>
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, strings.Replace(viewerTemplate, "/*DATA*/null", string(js), 1))
	return err
}
