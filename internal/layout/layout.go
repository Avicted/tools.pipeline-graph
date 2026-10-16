// Package layout computes a left-to-right layered (Sugiyama-style) layout
// with one horizontal swimlane per repository. Pure Go and deterministic.
package layout

import (
	"math"
	"sort"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

type Pt struct{ X, Y float64 }

type Box struct{ X, Y, W, H float64 }

func (b Box) CY() float64 { return b.Y + b.H/2 }

type Lane struct {
	Name string
	Box  Box
}

type Route struct {
	Edge   model.Edge
	Points []Pt // source port, bends..., target port
}

type Layout struct {
	Nodes  map[string]Box
	Lanes  []Lane
	Routes []Route
	W, H   float64
}

type Options struct {
	LaneHeader float64 // width of the lane title band on the left
	LanePad    float64 // padding inside lanes
	LaneGap    float64
	LayerGap   float64 // horizontal gap between layers
	NodeGap    float64 // vertical gap between nodes
	Margin     float64
	TopOffset  float64 // space above the first lane (title block)
}

func DefaultOptions() Options {
	return Options{LaneHeader: 44, LanePad: 28, LaneGap: 18, LayerGap: 110, NodeGap: 26, Margin: 30, TopOffset: 0}
}

type vnode struct {
	id    string
	dummy bool
	lane  int
	layer int
	w, h  float64
	y     float64 // local to lane
	order float64
	in    []*vnode
	out   []*vnode
}

// Compute lays out g. size returns the box size for each node; lanes is the ordered
// list of lane names (a node's lane is laneOf(node)).
func Compute(g *model.Graph, size func(*model.Node) (float64, float64), lanes []string, laneOf func(*model.Node) string, o Options) Layout {
	laneIdx := map[string]int{}
	for i, l := range lanes {
		laneIdx[l] = i
	}
	nodes := map[string]*vnode{}
	var order []*vnode
	for _, n := range g.Nodes {
		w, h := size(n)
		li, ok := laneIdx[laneOf(n)]
		if !ok {
			li = len(lanes)
			lanes = append(lanes, laneOf(n))
			laneIdx[laneOf(n)] = li
		}
		v := &vnode{id: n.ID, lane: li, w: w, h: h}
		nodes[n.ID] = v
		order = append(order, v)
	}

	// 1. cycle removal: DFS, reverse back edges
	type dedge struct {
		from, to *vnode
		rev      bool
		e        model.Edge
	}
	adj := map[*vnode][]model.Edge{}
	for _, e := range g.Edges {
		adj[nodes[e.From]] = append(adj[nodes[e.From]], e)
	}
	state := map[*vnode]int{}
	var edges []dedge
	var dfs func(v *vnode)
	dfs = func(v *vnode) {
		state[v] = 1
		for _, e := range adj[v] {
			w := nodes[e.To]
			switch state[w] {
			case 0:
				edges = append(edges, dedge{v, w, false, e})
				dfs(w)
			case 1:
				edges = append(edges, dedge{w, v, true, e})
			default:
				edges = append(edges, dedge{v, w, false, e})
			}
		}
		state[v] = 2
	}
	for _, v := range order {
		if state[v] == 0 {
			dfs(v)
		}
	}
	for _, de := range edges {
		de.from.out = append(de.from.out, de.to)
		de.to.in = append(de.to.in, de.from)
	}

	// 2. layering: longest path, then pull sources next to their successors
	var topo []*vnode
	indeg := map[*vnode]int{}
	for _, v := range order {
		indeg[v] = len(v.in)
	}
	queue := []*vnode{}
	for _, v := range order {
		if indeg[v] == 0 {
			queue = append(queue, v)
		}
	}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		topo = append(topo, v)
		for _, w := range v.out {
			if w.layer < v.layer+1 {
				w.layer = v.layer + 1
			}
			indeg[w]--
			if indeg[w] == 0 {
				queue = append(queue, w)
			}
		}
	}
	for i := len(topo) - 1; i >= 0; i-- {
		v := topo[i]
		if len(v.in) == 0 && len(v.out) > 0 {
			min := math.MaxInt
			for _, w := range v.out {
				if w.layer < min {
					min = w.layer
				}
			}
			v.layer = min - 1
		}
	}

	// 3. dummy nodes for edges spanning several layers
	type chain struct {
		e     model.Edge
		rev   bool
		nodes []*vnode // from ... to (in layer order)
	}
	var chains []chain
	for _, v := range order {
		v.in, v.out = nil, nil
	}
	all := append([]*vnode{}, order...)
	for _, de := range edges {
		c := chain{e: de.e, rev: de.rev, nodes: []*vnode{de.from}}
		prev := de.from
		for l := de.from.layer + 1; l < de.to.layer; l++ {
			d := &vnode{dummy: true, lane: de.from.lane, layer: l, h: 6}
			if l > (de.from.layer+de.to.layer)/2 {
				d.lane = de.to.lane // cross-lane edges switch lanes half way
			}
			all = append(all, d)
			prev.out = append(prev.out, d)
			d.in = append(d.in, prev)
			c.nodes = append(c.nodes, d)
			prev = d
		}
		prev.out = append(prev.out, de.to)
		de.to.in = append(de.to.in, prev)
		c.nodes = append(c.nodes, de.to)
		chains = append(chains, c)
	}

	// 4. ordering within layers: lane first, then barycenter (several sweeps)
	maxLayer := 0
	for _, v := range all {
		if v.layer > maxLayer {
			maxLayer = v.layer
		}
	}
	layers := make([][]*vnode, maxLayer+1)
	for _, v := range all {
		layers[v.layer] = append(layers[v.layer], v)
	}
	for _, l := range layers {
		sort.SliceStable(l, func(i, j int) bool { return l[i].lane < l[j].lane })
		renumber(l)
	}
	for it := 0; it < 12; it++ {
		if it%2 == 0 {
			for i := 1; i <= maxLayer; i++ {
				sortByBary(layers[i], func(v *vnode) []*vnode { return v.in })
			}
		} else {
			for i := maxLayer - 1; i >= 0; i-- {
				sortByBary(layers[i], func(v *vnode) []*vnode { return v.out })
			}
		}
	}

	// 5. vertical placement inside lanes: start stacked, then pull nodes towards
	// the median of same-lane neighbours while preserving order and spacing.
	group := func(l []*vnode) map[int][]*vnode {
		m := map[int][]*vnode{}
		for _, v := range l {
			m[v.lane] = append(m[v.lane], v)
		}
		return m
	}
	for _, l := range layers {
		for _, grp := range group(l) {
			y := 0.0
			for _, v := range grp {
				v.y = y
				y += v.h + o.NodeGap
			}
		}
	}
	for it := 0; it < 16; it++ {
		for pass := 0; pass < 2; pass++ {
			var seq []int
			if (it+pass)%2 == 0 {
				for i := 0; i <= maxLayer; i++ {
					seq = append(seq, i)
				}
			} else {
				for i := maxLayer; i >= 0; i-- {
					seq = append(seq, i)
				}
			}
			for _, li := range seq {
				for _, grp := range group(layers[li]) {
					desired := make([]float64, len(grp))
					for k, v := range grp {
						desired[k] = v.y + v.h/2
						var ys []float64
						for _, w := range append(append([]*vnode{}, v.in...), v.out...) {
							if w.lane == v.lane {
								ys = append(ys, w.y+w.h/2)
							}
						}
						if len(ys) > 0 {
							desired[k] = median(ys)
						}
						desired[k] -= v.h / 2
					}
					place(grp, desired, o.NodeGap)
				}
			}
		}
	}

	// normalise: each lane's content starts at local y = 0
	minY := map[int]float64{}
	for _, v := range all {
		if m, ok := minY[v.lane]; !ok || v.y < m {
			minY[v.lane] = v.y
		}
	}
	for _, v := range all {
		v.y -= minY[v.lane]
	}

	// 6. absolute coordinates
	layerX := make([]float64, maxLayer+2)
	layerW := make([]float64, maxLayer+1)
	for _, v := range all {
		if v.w > layerW[v.layer] {
			layerW[v.layer] = v.w
		}
	}
	layerX[0] = o.Margin + o.LaneHeader + o.LanePad
	for i := 0; i <= maxLayer; i++ {
		gap := o.LayerGap
		if layerW[i] == 0 {
			gap = o.LayerGap / 2
		}
		layerX[i+1] = layerX[i] + layerW[i] + gap
	}
	laneH := make([]float64, len(lanes))
	for _, v := range all {
		if b := v.y + v.h; b > laneH[v.lane] {
			laneH[v.lane] = b
		}
	}
	width := layerX[maxLayer+1] - o.LayerGap + o.LanePad + o.Margin
	laneTop := make([]float64, len(lanes))
	y := o.Margin + o.TopOffset
	var out Layout
	out.Nodes = map[string]Box{}
	for i, name := range lanes {
		// tall enough for the vertical lane title
		h := max(laneH[i]+2*o.LanePad, 120, float64(len(name))*8.5+40)
		used := false
		for _, v := range order {
			if v.lane == i {
				used = true
				break
			}
		}
		if !used {
			continue
		}
		laneTop[i] = y + o.LanePad + (h-2*o.LanePad-laneH[i])/2
		out.Lanes = append(out.Lanes, Lane{Name: name, Box: Box{X: o.Margin, Y: y, W: width - 2*o.Margin, H: h}})
		y += h + o.LaneGap
	}
	out.W, out.H = width, y-o.LaneGap+o.Margin
	pos := func(v *vnode) Box {
		// centre nodes horizontally in their layer column
		return Box{X: layerX[v.layer] + (layerW[v.layer]-v.w)/2, Y: laneTop[v.lane] + v.y, W: v.w, H: v.h}
	}
	for _, v := range order {
		out.Nodes[v.id] = pos(v)
	}
	for _, c := range chains {
		var pts []Pt
		for k, v := range c.nodes {
			b := pos(v)
			switch {
			case k == 0:
				pts = append(pts, Pt{b.X + b.W, b.CY()})
			case k == len(c.nodes)-1:
				pts = append(pts, Pt{b.X, b.CY()})
			default:
				// run horizontally across the whole column so the edge cannot clip its cards
				pts = append(pts, Pt{layerX[v.layer] - o.LayerGap/4, b.CY()}, Pt{layerX[v.layer] + layerW[v.layer] + o.LayerGap/4, b.CY()})
			}
		}
		if c.rev {
			for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
				pts[i], pts[j] = pts[j], pts[i]
			}
		}
		out.Routes = append(out.Routes, Route{Edge: c.e, Points: pts})
	}
	sort.SliceStable(out.Routes, func(i, j int) bool {
		a, b := out.Routes[i].Edge, out.Routes[j].Edge
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	return out
}

func renumber(l []*vnode) {
	for i, v := range l {
		v.order = float64(i)
	}
}

func sortByBary(l []*vnode, nb func(*vnode) []*vnode) {
	bary := map[*vnode]float64{}
	for _, v := range l {
		ns := nb(v)
		if len(ns) == 0 {
			bary[v] = v.order
			continue
		}
		s := 0.0
		for _, w := range ns {
			s += w.order
		}
		bary[v] = s / float64(len(ns))
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].lane != l[j].lane {
			return l[i].lane < l[j].lane
		}
		return bary[l[i]] < bary[l[j]]
	})
	renumber(l)
}

func median(xs []float64) float64 {
	sort.Float64s(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}

// place sets v.y as close as possible (least squares) to desired while keeping
// order, the minimum gap and y >= 0: isotonic regression via pool-adjacent-violators.
func place(grp []*vnode, desired []float64, gap float64) {
	n := len(grp)
	off := make([]float64, n)
	for i := 1; i < n; i++ {
		off[i] = off[i-1] + grp[i-1].h + gap
	}
	type block struct {
		sum   float64
		count int
	}
	var blocks []block
	var sizes []int
	for i := 0; i < n; i++ {
		blocks = append(blocks, block{desired[i] - off[i], 1})
		sizes = append(sizes, 1)
		for len(blocks) > 1 {
			a, b := blocks[len(blocks)-2], blocks[len(blocks)-1]
			if a.sum/float64(a.count) <= b.sum/float64(b.count) {
				break
			}
			blocks = blocks[:len(blocks)-2]
			blocks = append(blocks, block{a.sum + b.sum, a.count + b.count})
			sa, sb := sizes[len(sizes)-2], sizes[len(sizes)-1]
			sizes = append(sizes[:len(sizes)-2], sa+sb)
		}
	}
	i := 0
	for k, b := range blocks {
		z := b.sum / float64(b.count)
		if z < 0 {
			z = 0
		}
		for j := 0; j < sizes[k]; j++ {
			grp[i].y = z + off[i]
			i++
		}
	}
}
