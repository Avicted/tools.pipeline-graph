// Package render turns the pipeline graph into diagrams: draw.io (Miro
// importable), a self-contained interactive HTML page, Mermaid and Graphviz DOT.
package render

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/layout"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

const ExternalLane = "External"

// View is one page of the diagram: a subset of the graph with its layout.
type View struct {
	ID     string
	Title  string
	Graph  *model.Graph
	Layout layout.Layout
}

// Theme colours shared by every renderer.
type style struct {
	Fill, Stroke, Text string
	Dashed             bool
}

var nodeStyles = map[model.NodeKind]style{
	model.KindBuild:    {Fill: "#FFFFFF", Stroke: "#2563EB", Text: "#0F172A"},
	model.KindExternal: {Fill: "#FFF7ED", Stroke: "#EA580C", Text: "#7C2D12", Dashed: true},
	model.KindMake:     {Fill: "#F1F5F9", Stroke: "#64748B", Text: "#1E293B"},
	model.KindImage:    {Fill: "#ECFDF5", Stroke: "#059669", Text: "#064E3B"},
}

var compositeStyle = style{Fill: "#FAF5FF", Stroke: "#7C3AED", Text: "#2E1065"}

func nodeStyle(n *model.Node) style {
	if n.Composite {
		return compositeStyle
	}
	return nodeStyles[n.Kind]
}

type edgeStyle struct {
	Color  string  `json:"color"`
	Width  float64 `json:"width"`
	Dash   string  `json:"dash"` // "" solid, "dash", "dot"
	Legend string  `json:"legend"`
}

var edgeStyles = map[model.EdgeKind]edgeStyle{
	model.EdgeSnapshot: {"#2563EB", 1.6, "", "snapshot dependency"},
	model.EdgeArtifact: {"#2563EB", 1.6, "dash", "artifact dependency"},
	model.EdgeTrigger:  {"#EA580C", 2.4, "", "finish-build trigger"},
	model.EdgeRuns:     {"#64748B", 1.2, "", "step runs make target"},
	model.EdgeDepends:  {"#94A3B8", 1.0, "dash", "make prerequisite"},
	model.EdgeCalls:    {"#94A3B8", 1.2, "dot", "recursive make"},
	model.EdgeProduces: {"#059669", 1.6, "dot", "produces image"},
	model.EdgeConsumes: {"#059669", 1.6, "dot", "consumes image"},
}

var edgeOrder = []model.EdgeKind{
	model.EdgeSnapshot, model.EdgeArtifact, model.EdgeTrigger, model.EdgeProduces,
	model.EdgeConsumes, model.EdgeRuns, model.EdgeDepends, model.EdgeCalls,
}

// Card is the text content of a node box.
type Card struct {
	Title  string   `json:"title"`
	Sub    string   `json:"sub,omitempty"`
	Lines  []string `json:"lines,omitempty"`
	Badges []string `json:"badges,omitempty"`
}

const maxStepLines = 4

func CardOf(n *model.Node) Card {
	c := Card{Title: n.Label, Sub: n.Sub}
	switch n.Kind {
	case model.KindBuild:
		if n.Project != "" {
			c.Sub = n.Project + " · " + n.Sub
		}
		for i, s := range n.Steps {
			if i == maxStepLines {
				c.Lines = append(c.Lines, fmt.Sprintf("… %d more steps", len(n.Steps)-maxStepLines))
				break
			}
			c.Lines = append(c.Lines, "▸ "+s.Summary)
		}
		if n.Composite {
			c.Badges = append(c.Badges, "◆ composite")
		}
		for _, t := range n.Triggers {
			switch t.Type {
			case "vcs":
				c.Badges = append(c.Badges, "⎇ on commit")
			case "schedule":
				c.Badges = append(c.Badges, "⏱ "+t.Detail)
			case "finishBuild":
				// drawn as an edge
			default:
				c.Badges = append(c.Badges, "⚡ "+t.Type)
			}
		}
		if len(n.Artifacts) > 0 {
			c.Badges = append(c.Badges, fmt.Sprintf("▣ %d artifact rule%s", len(n.Artifacts), plural(len(n.Artifacts))))
		}
	case model.KindMake:
		c.Title = "make " + n.Label
		if len(n.Steps) > 0 {
			c.Lines = []string{n.Steps[0].Summary}
		}
	case model.KindImage:
		c.Title = "🐳 " + n.Label
	}
	c.Badges = dedupe(c.Badges)
	return c
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Size estimates the box size of a node from its card text.
func Size(n *model.Node) (float64, float64) {
	c := CardOf(n)
	const charW = 6.6
	longest := float64(len([]rune(c.Title))) * 7.6
	for _, l := range append(append([]string{c.Sub}, c.Lines...), strings.Join(c.Badges, "  ")) {
		if w := float64(len([]rune(l))) * charW; w > longest {
			longest = w
		}
	}
	minW, maxW := 200.0, 320.0
	if n.Kind == model.KindMake || n.Kind == model.KindImage {
		minW, maxW = 150, 260
	}
	w := clamp(longest+28, minW, maxW)
	h := 14 + 18 + 15.0 // padding + title + sub
	if len(c.Lines) > 0 {
		h += 6 + 15*float64(len(c.Lines))
	}
	if len(c.Badges) > 0 {
		h += 22
	}
	return w, h
}

// SizeVSDX sizes cards for Miro's Visio import, which ignores the line
// spacing in the file and gives every text line about 27px (measured on an
// imported board). Boxes sized like Size let the text spill over the edges.
func SizeVSDX(n *model.Node) (float64, float64) {
	w, _ := Size(n)
	c := CardOf(n)
	lines := 1 + len(c.Lines)
	if c.Sub != "" {
		lines++
	}
	if len(c.Badges) > 0 {
		lines++
	}
	return w * 1.1, 22 + 27*float64(lines)
}

func clamp(v, lo, hi float64) float64 { return max(lo, min(hi, v)) }

func laneOf(n *model.Node) string {
	if n.Repo == "" {
		return ExternalLane
	}
	return n.Repo
}

// BuildViews creates the requested views: "overview" (pipelines and images),
// "full" (plus make targets) and "per-repo" (one page per repository with neighbours).
func BuildViews(g *model.Graph, names []string) []View {
	return BuildViewsSized(g, names, Size)
}

// BuildViewsSized is BuildViews with a custom node size estimate, for
// renderers whose target lays text out differently (see SizeVSDX).
func BuildViewsSized(g *model.Graph, names []string, size func(*model.Node) (float64, float64)) []View {
	mk := func(id, title string, sub *model.Graph) View { return layoutView(g, id, title, sub, size) }
	var views []View
	for _, name := range names {
		switch name {
		case "overview":
			views = append(views, mk("overview", "Overview", WithoutMake(g)))
		case "full":
			views = append(views, mk("full", "Pipelines + make", g))
		case "per-repo":
			for _, r := range g.Repos {
				sub := repoView(g, r.Name)
				if len(sub.Nodes) == 0 {
					continue
				}
				views = append(views, mk("repo-"+r.Name, r.Name, sub))
			}
		}
	}
	return views
}

func layoutView(g *model.Graph, id, title string, sub *model.Graph, size func(*model.Node) (float64, float64)) View {
	var lanes []string
	for _, r := range g.Repos {
		lanes = append(lanes, r.Name)
	}
	lanes = append(lanes, ExternalLane)
	opts := layout.DefaultOptions()
	opts.TopOffset = 96
	return View{ID: id, Title: title, Graph: sub, Layout: layout.Compute(sub, size, lanes, laneOf, opts)}
}

// compactView lays a lineage out in one lane, ordered by dependency alone: a
// lineage crossing six repositories would otherwise be six mostly empty bands.
func compactView(id, title string, sub *model.Graph) View {
	opts := layout.DefaultOptions()
	opts.TopOffset = 24
	one := func(*model.Node) string { return "" }
	l := layout.Compute(sub, Size, []string{""}, one, opts)
	l.Lanes = nil // the single lane has no name worth drawing
	return View{ID: id, Title: title, Graph: sub, Layout: l}
}

// reach returns the nodes reachable from the roots along edges (down) or against them (up), roots included.
func reach(g *model.Graph, roots []string, down bool) map[string]bool {
	next := map[string][]string{}
	for _, e := range g.Edges {
		if down {
			next[e.From] = append(next[e.From], e.To)
		} else {
			next[e.To] = append(next[e.To], e.From)
		}
	}
	seen := map[string]bool{}
	stack := append([]string{}, roots...)
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if !seen[id] {
			seen[id] = true
			stack = append(stack, next[id]...)
		}
	}
	return seen
}

// FocusView is a node's lineage laid out on its own, with the transitive counts.
type FocusView struct {
	View
	Root     string
	Up, Down int
}

// FocusViews lays out the upstream and downstream lineage of every node.
// shortcut: one layout per node, fine for a few hundred nodes, upgrade to client-side layout beyond that.
func FocusViews(g *model.Graph) []FocusView {
	nomake := WithoutMake(g)
	var out []FocusView
	for _, n := range g.Nodes {
		base := nomake
		if n.Kind == model.KindMake {
			base = g
		}
		up, down := reach(base, []string{n.ID}, false), reach(base, []string{n.ID}, true)
		sub := base.Filter(func(x *model.Node) bool { return up[x.ID] || down[x.ID] })
		out = append(out, FocusView{
			View: compactView("focus:"+n.ID, n.Label, sub), Root: n.ID, Up: len(up) - 1, Down: len(down) - 1,
		})
	}
	return out
}

// FlowViews lays out, per repository, everything downstream of its commit-triggered builds.
func FlowViews(g *model.Graph) []View {
	nomake := WithoutMake(g)
	var out []View
	for _, r := range g.Repos {
		var roots []string
		for _, n := range nomake.Nodes {
			if n.Repo != r.Name || n.Kind != model.KindBuild {
				continue
			}
			for _, t := range n.Triggers {
				if t.Type == "vcs" {
					roots = append(roots, n.ID)
					break
				}
			}
		}
		if len(roots) == 0 {
			continue
		}
		down := reach(nomake, roots, true)
		out = append(out, compactView("flow:"+r.Name, r.Name+": commit → …", nomake.Filter(func(x *model.Node) bool { return down[x.ID] })))
	}
	return out
}

// WithoutMake hides make targets. Images produced or consumed inside make are
// re-attached to the build configurations that run those targets.
func WithoutMake(g *model.Graph) *model.Graph {
	out := g.Filter(func(n *model.Node) bool { return n.Kind != model.KindMake })
	next := map[string][]string{}
	for _, e := range g.Edges {
		switch e.Kind {
		case model.EdgeRuns, model.EdgeDepends, model.EdgeCalls:
			next[e.From] = append(next[e.From], e.To)
		}
	}
	for _, b := range g.Nodes {
		if b.Kind != model.KindBuild {
			continue
		}
		reach := map[string]bool{}
		stack := append([]string{}, next[b.ID]...)
		for len(stack) > 0 {
			id := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if !reach[id] {
				reach[id] = true
				stack = append(stack, next[id]...)
			}
		}
		for _, e := range g.Edges {
			switch {
			case e.Kind == model.EdgeProduces && reach[e.From]:
				out.AddEdge(model.Edge{From: b.ID, To: e.To, Kind: e.Kind})
			case e.Kind == model.EdgeConsumes && reach[e.To]:
				out.AddEdge(model.Edge{From: e.From, To: b.ID, Kind: e.Kind})
			}
		}
	}
	out.Sort()
	return out
}

// repoView keeps a repo's nodes plus directly connected nodes from elsewhere.
func repoView(g *model.Graph, repo string) *model.Graph {
	keep := map[string]bool{}
	for _, n := range g.Nodes {
		if n.Repo == repo {
			keep[n.ID] = true
		}
	}
	ctx := map[string]bool{}
	for _, e := range g.Edges {
		if keep[e.From] && !keep[e.To] {
			ctx[e.To] = true
		}
		if keep[e.To] && !keep[e.From] {
			ctx[e.From] = true
		}
	}
	for id := range ctx {
		// context nodes from other repos: pipelines and images only
		if n := g.Node(id); n.Kind != model.KindMake {
			keep[id] = true
		}
	}
	return g.Filter(func(n *model.Node) bool { return keep[n.ID] })
}

// Subtitle summarises where the diagram came from.
func Subtitle(g *model.Graph) string {
	var parts []string
	for _, r := range g.Repos {
		s := r.Name
		if r.Commit != "" {
			s += "@" + short(r.Commit)
		}
		parts = append(parts, s)
	}
	sort.Strings(parts)
	return fmt.Sprintf("Generated %s from %d repositories: %s", g.Generated, len(g.Repos), strings.Join(parts, ", "))
}

func short(c string) string {
	if len(c) > 8 {
		return c[:8]
	}
	return c
}
