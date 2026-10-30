// Package model is the graph produced by analysis and consumed by renderers.
package model

import (
	"encoding/json"
	"io"
	"sort"
)

type NodeKind string

const (
	KindBuild    NodeKind = "build"    // a CI job: a build configuration, a workflow job, ...
	KindExternal NodeKind = "external" // referenced job not found in any analysed repo
	KindMake     NodeKind = "make"     // make target
	KindImage    NodeKind = "image"    // docker image produced by a pipeline
)

type EdgeKind string

const (
	EdgeSnapshot EdgeKind = "snapshot" // build depends on (snapshot) build
	EdgeArtifact EdgeKind = "artifact" // build pulls artifacts from build
	EdgeTrigger  EdgeKind = "trigger"  // finish of build triggers build
	EdgeRuns     EdgeKind = "runs"     // build step invokes make target
	EdgeDepends  EdgeKind = "depends"  // make target needs prerequisite target
	EdgeCalls    EdgeKind = "calls"    // make recipe invokes another make
	EdgeProduces EdgeKind = "produces" // build/target produces image
	EdgeConsumes EdgeKind = "consumes" // image is used by build/target
	EdgeDerives  EdgeKind = "derives"  // image is built FROM another image
	EdgeRunsIn   EdgeKind = "runsIn"   // build/target runs inside a container of the image
)

// Edges point left to right in the diagram:
//   - between builds and images they follow the flow of work: upstream -> downstream
//     (snapshot dependency "B depends on A" is stored as A -> B);
//   - from builds into make they follow invocation: caller -> callee
//     (build -> target it runs -> prerequisite targets).

type Step struct {
	Name    string `json:"name,omitempty"`
	Type    string `json:"type"` // script, exec, dockerCommand, gradle, maven, ...
	Summary string `json:"summary,omitempty"`
	Script  string `json:"script,omitempty"`
	WorkDir string `json:"workDir,omitempty"`
}

type Trigger struct {
	Type   string `json:"type"` // vcs, schedule, finishBuild
	Detail string `json:"detail,omitempty"`
}

type Node struct {
	ID        string            `json:"id"`
	Kind      NodeKind          `json:"kind"`
	Repo      string            `json:"repo,omitempty"`
	Label     string            `json:"label"`
	Sub       string            `json:"sub,omitempty"` // secondary line: the job's id in its CI system, makefile path, ...
	Project   string            `json:"project,omitempty"`
	Composite bool              `json:"composite,omitempty"`
	Steps     []Step            `json:"steps,omitempty"`
	Triggers  []Trigger         `json:"triggers,omitempty"`
	Params    map[string]string `json:"params,omitempty"`
	Artifacts []string          `json:"artifacts,omitempty"` // published artifact rules
	Source    string            `json:"source,omitempty"`    // file:line
	System    string            `json:"system,omitempty"`    // System.Name of the CI system the job comes from
}

type Edge struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Kind  EdgeKind `json:"kind"`
	Label string   `json:"label,omitempty"`
}

// System is how a CI system names itself and its things, for messages and viewers.
type System struct {
	Name  string `json:"name"`  // `provider:` value and settings key, e.g. "teamcity"
	Title string `json:"title"` // for people, e.g. "GitHub Actions"
	Job   string `json:"job"`   // what one job is called, e.g. "build configuration"
	ID    string `json:"id"`    // what a job's id (Node.Sub) is called, e.g. "TeamCity id"
}

type Repo struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Commit string `json:"commit,omitempty"`
}

// SchemaVersion is bumped when graph.json changes shape; the viewer checks it on load.
const SchemaVersion = 1

type Graph struct {
	SchemaVersion int      `json:"schemaVersion"`
	Generated     string   `json:"generated"`
	Repos         []Repo   `json:"repos"`
	Nodes         []*Node  `json:"nodes"`
	Edges         []Edge   `json:"edges"`
	Warnings      []string `json:"warnings,omitempty"` // what the analysis could not resolve
	Systems       []System `json:"systems,omitempty"`  // the CI systems the jobs come from

	index map[string]*Node
	seen  map[Edge]bool
}

func New() *Graph {
	return &Graph{SchemaVersion: SchemaVersion, index: map[string]*Node{}, seen: map[Edge]bool{}}
}

func (g *Graph) Node(id string) *Node {
	if g.index == nil {
		g.reindex()
	}
	return g.index[id]
}

// AddNode adds n unless a node with the same ID exists; returns the stored node.
func (g *Graph) AddNode(n *Node) *Node {
	if old := g.Node(n.ID); old != nil {
		return old
	}
	g.Nodes = append(g.Nodes, n)
	g.index[n.ID] = n
	return n
}

// AddEdge adds e unless an identical edge exists or it is a self-loop.
func (g *Graph) AddEdge(e Edge) {
	if e.From == e.To {
		return
	}
	if g.seen == nil {
		g.seen = map[Edge]bool{}
		for _, x := range g.Edges {
			g.seen[x] = true
		}
	}
	if g.seen[e] {
		return
	}
	g.seen[e] = true
	g.Edges = append(g.Edges, e)
}

func (g *Graph) reindex() {
	g.index = map[string]*Node{}
	for _, n := range g.Nodes {
		g.index[n.ID] = n
	}
}

// Sort orders nodes and edges deterministically.
func (g *Graph) Sort() {
	sort.Slice(g.Nodes, func(i, j int) bool { return g.Nodes[i].ID < g.Nodes[j].ID })
	sort.Slice(g.Edges, func(i, j int) bool {
		a, b := g.Edges[i], g.Edges[j]
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		if a.Kind != b.Kind {
			return a.Kind < b.Kind
		}
		return a.Label < b.Label
	})
}

// Filter returns a copy holding only nodes accepted by keep and edges between them.
func (g *Graph) Filter(keep func(*Node) bool) *Graph {
	out := New()
	out.Generated, out.Repos, out.Systems = g.Generated, g.Repos, g.Systems
	for _, n := range g.Nodes {
		if keep(n) {
			out.AddNode(n)
		}
	}
	for _, e := range g.Edges {
		if out.Node(e.From) != nil && out.Node(e.To) != nil {
			out.AddEdge(e)
		}
	}
	return out
}

func (g *Graph) WriteJSON(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}
