package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/layout"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

const (
	namespace = "http://www.opengroup.org/xsd/archimate/3.0/"
	schema    = "http://www.opengroup.org/xsd/archimate/3.1/archimate3_Diagram.xsd"

	pad    = 20.0 // inside a container, around its children
	header = 30.0 // a container's title band
)

// render writes the model in the Open Group ArchiMate 3.1 exchange format. The
// output depends only on the model, so it can be compared byte for byte.
func render(m *Model) []byte {
	var b bytes.Buffer
	w := func(depth int, format string, a ...any) {
		b.WriteString(strings.Repeat("  ", depth))
		fmt.Fprintf(&b, format, a...)
		b.WriteByte('\n')
	}
	w(0, `<?xml version="1.0" encoding="UTF-8"?>`)
	w(0, `<!-- Generated from docs/architecture/model.yaml by make archimate. Do not edit. -->`)
	w(0, `<model xmlns=%q xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:schemaLocation="%s %s" identifier="id-model">`, namespace, namespace, schema)
	w(1, `<name xml:lang="en">%s</name>`, esc(m.Name))
	w(1, `<documentation xml:lang="en">%s</documentation>`, esc(m.Documentation))

	w(1, `<elements>`)
	for _, e := range m.Elements {
		w(2, `<element identifier="id-%s" xsi:type="%s">`, e.ID, e.Type)
		w(3, `<name xml:lang="en">%s</name>`, esc(e.Name))
		if e.Doc != "" {
			w(3, `<documentation xml:lang="en">%s</documentation>`, esc(e.Doc))
		}
		w(2, `</element>`)
	}
	w(1, `</elements>`)

	w(1, `<relationships>`)
	for _, r := range m.Relationships {
		access := ""
		if r.Access != "" {
			access = fmt.Sprintf(` accessType="%s"`, r.Access)
		}
		if r.Name == "" {
			w(2, `<relationship identifier="id-%s" source="id-%s" target="id-%s" xsi:type="%s"%s/>`, r.ID, r.Source, r.Target, r.Type, access)
			continue
		}
		w(2, `<relationship identifier="id-%s" source="id-%s" target="id-%s" xsi:type="%s"%s>`, r.ID, r.Source, r.Target, r.Type, access)
		w(3, `<name xml:lang="en">%s</name>`, esc(r.Name))
		w(2, `</relationship>`)
	}
	w(1, `</relationships>`)

	w(1, `<views>`)
	w(2, `<diagrams>`)
	for _, v := range m.Views {
		d := newDrawing(m, v)
		w(3, `<view identifier="id-%s" xsi:type="Diagram">`, v.ID)
		w(4, `<name xml:lang="en">%s</name>`, esc(v.Name))
		w(4, `<documentation xml:lang="en">The diagram tagged view: %s in %s.</documentation>`, v.ID, v.Doc)
		var node func(depth int, id string)
		node = func(depth int, id string) {
			bx := d.abs[id]
			open := fmt.Sprintf(`<node identifier="n-%s-%s" elementRef="id-%s" xsi:type="Element" x="%d" y="%d" w="%d" h="%d"`,
				v.ID, id, d.el[id], round(bx.X), round(bx.Y), round(bx.W), round(bx.H))
			if len(d.kids[id]) == 0 {
				w(depth, "%s/>", open)
				return
			}
			w(depth, "%s>", open)
			for _, k := range d.kids[id] {
				node(depth+1, k)
			}
			w(depth, `</node>`)
		}
		for _, id := range d.kids[""] {
			node(4, id)
		}
		for _, c := range d.conns {
			w(4, `<connection identifier="c-%s-%s" relationshipRef="id-%s" xsi:type="Relationship" source="n-%s-%s" target="n-%s-%s"/>`,
				v.ID, c.rel, c.rel, v.ID, c.from, v.ID, c.to)
		}
		w(3, `</view>`)
	}
	w(2, `</diagrams>`)
	w(1, `</views>`)
	w(0, `</model>`)
	return b.Bytes()
}

func esc(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func round(f float64) int { return int(math.Round(f)) }

type conn struct{ rel, from, to string } // from and to are Mermaid node ids

// drawing is one view laid out: every node's absolute box, nested boxes inside
// their parent, each level placed left to right by internal/layout.
type drawing struct {
	el     map[string]string   // Mermaid id -> element id
	name   map[string]string   // Mermaid id -> element name
	parent map[string]string   // Mermaid id -> enclosing Mermaid id ("" at the top)
	kids   map[string][]string // Mermaid id -> nested Mermaid ids, in model order
	conns  []conn
	abs    map[string]layout.Box
}

func newDrawing(m *Model, v View) *drawing {
	d := &drawing{el: map[string]string{}, name: map[string]string{}, parent: map[string]string{}, kids: map[string][]string{}, abs: map[string]layout.Box{}}
	names := map[string]string{}
	for _, e := range m.Elements {
		names[e.ID] = e.Name
	}
	byEl := map[string]string{} // element id -> Mermaid id
	for _, n := range v.Nodes {
		d.el[n.M], d.name[n.M], d.parent[n.M] = n.El, names[n.El], n.In
		d.kids[n.In] = append(d.kids[n.In], n.M)
		byEl[n.El] = n.M
	}
	for _, r := range m.Relationships {
		from, fok := byEl[r.Source]
		to, tok := byEl[r.Target]
		shown := len(v.Rels) == 0 || slices.Contains(v.Rels, r.ID)
		if fok && tok && shown && from != to && !d.encloses(from, to) && !d.encloses(to, from) {
			d.conns = append(d.conns, conn{r.ID, from, to})
		}
	}
	d.place("", 0, 0)
	return d
}

// encloses reports whether outer is an ancestor of inner; nesting already says
// what such a relationship would, so it isn't drawn.
func (d *drawing) encloses(outer, inner string) bool {
	for p := d.parent[inner]; p != ""; p = d.parent[p] {
		if p == outer {
			return true
		}
	}
	return false
}

// under returns the child of parent that is id or contains it, or "".
func (d *drawing) under(parent, id string) string {
	for id != "" && d.parent[id] != parent {
		id = d.parent[id]
	}
	return id
}

// arrange lays out the children of parent, linked by the connections between
// their subtrees, and returns their boxes relative to the parent's content area.
func (d *drawing) arrange(parent string) layout.Layout {
	g := &model.Graph{}
	for _, k := range d.kids[parent] {
		g.Nodes = append(g.Nodes, &model.Node{ID: k})
	}
	seen := map[[2]string]bool{}
	for _, c := range d.conns {
		a, b := d.under(parent, c.from), d.under(parent, c.to)
		if a != "" && b != "" && a != b && !seen[[2]string{a, b}] {
			seen[[2]string{a, b}] = true
			g.Edges = append(g.Edges, model.Edge{From: a, To: b})
		}
	}
	size := func(n *model.Node) (float64, float64) { return d.size(n.ID) }
	o := layout.Options{LayerGap: 60, NodeGap: 20}
	return layout.Compute(g, size, nil, func(*model.Node) string { return "" }, o)
}

func (d *drawing) size(id string) (float64, float64) {
	if len(d.kids[id]) == 0 {
		chars := float64(len([]rune(d.name[id])))
		w := math.Min(math.Max(chars*7+30, 120), 240)
		lines := math.Ceil(chars * 7 / (w - 30))
		return w, 40 + 15*lines
	}
	l := d.arrange(id)
	return l.W + 2*pad, l.H + header + pad
}

// place gives id (and, recursively, its children) absolute boxes, with the
// content area of id starting at x, y.
func (d *drawing) place(id string, x, y float64) {
	l := d.arrange(id)
	for _, k := range d.kids[id] {
		b := l.Nodes[k]
		w, h := d.size(k)
		d.abs[k] = layout.Box{X: x + b.X, Y: y + b.Y, W: w, H: h}
		if len(d.kids[k]) > 0 {
			d.place(k, x+b.X+pad, y+b.Y+header)
		}
	}
}
