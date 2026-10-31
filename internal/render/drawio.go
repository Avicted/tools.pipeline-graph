package render

import (
	"fmt"
	"html"
	"io"
	"strings"

	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

// DrawIO writes an uncompressed .drawio file with one page per view.
// The file opens in diagrams.net / Confluence and can be imported into Miro.
func DrawIO(w io.Writer, g *model.Graph, views []View) error {
	var b strings.Builder
	fmt.Fprintf(&b, "<mxfile host=\"pipeline-graph\" modified=%q type=\"device\">\n", g.Generated)
	for _, v := range views {
		drawioPage(&b, g, v)
	}
	b.WriteString("</mxfile>\n")
	_, err := io.WriteString(w, b.String())
	return err
}

type cellWriter struct {
	b  *strings.Builder
	id int
}

func (c *cellWriter) next() string {
	c.id++
	return fmt.Sprintf("c%d", c.id)
}

func (c *cellWriter) vertex(id, parent, value, style string, x, y, w, h float64) {
	fmt.Fprintf(c.b, "        <mxCell id=%q value=\"%s\" style=\"%s\" vertex=\"1\" parent=%q>\n", id, xmlEscape(value), xmlEscape(style), parent)
	fmt.Fprintf(c.b, "          <mxGeometry x=\"%.0f\" y=\"%.0f\" width=\"%.0f\" height=\"%.0f\" as=\"geometry\" />\n        </mxCell>\n", x, y, w, h)
}

func drawioPage(b *strings.Builder, g *model.Graph, v View) {
	L := v.Layout
	fmt.Fprintf(b, "  <diagram id=\"%s\" name=\"%s\">\n", xmlEscape("pg-"+v.ID), xmlEscape(v.Title))
	fmt.Fprintf(b, "    <mxGraphModel dx=\"1200\" dy=\"800\" grid=\"0\" gridSize=\"10\" guides=\"1\" tooltips=\"1\" connect=\"1\" arrows=\"1\" fold=\"1\" page=\"0\" pageScale=\"1\" pageWidth=\"%.0f\" pageHeight=\"%.0f\" math=\"0\" shadow=\"0\">\n", L.W, L.H)
	b.WriteString("      <root>\n        <mxCell id=\"0\" />\n        <mxCell id=\"1\" parent=\"0\" />\n")
	c := &cellWriter{b: b}
	m := 30.0

	// title block
	c.vertex(c.next(), "1", "Pipeline map · "+v.Title,
		"text;html=1;align=left;verticalAlign=top;fontSize=22;fontStyle=1;fontColor=#0F172A;strokeColor=none;fillColor=none;", m, m-6, 640, 32)
	c.vertex(c.next(), "1", subtitle(g),
		"text;html=1;align=left;verticalAlign=top;fontSize=11;fontColor=#64748B;strokeColor=none;fillColor=none;whiteSpace=wrap;", m, m+26, 720, 34)
	drawioLegend(c, v, L.W-m)

	// lanes
	laneID := map[string]string{}
	laneBox := map[string][2]float64{}
	for i, lane := range L.Lanes {
		id := c.next()
		laneID[lane.Name] = id
		laneBox[lane.Name] = [2]float64{lane.Box.X, lane.Box.Y}
		fill := "#F8FAFC"
		if i%2 == 1 {
			fill = "#FFFFFF"
		}
		head := "#1E3A8A"
		if lane.Name == externalLane {
			head = "#9A3412"
		}
		c.vertex(id, "1", lane.Name,
			fmt.Sprintf("swimlane;horizontal=0;startSize=44;html=1;whiteSpace=wrap;rounded=1;arcSize=4;fontSize=14;fontStyle=1;fontColor=#FFFFFF;fillColor=%s;swimlaneFillColor=%s;strokeColor=#CBD5E1;collapsible=0;", head, fill),
			lane.Box.X, lane.Box.Y, lane.Box.W, lane.Box.H)
	}

	// nodes (children of their lane, relative coordinates)
	cellOf := map[string]string{}
	for _, n := range v.Graph.Nodes {
		box := L.Nodes[n.ID]
		lane := laneOf(n)
		parent, off := "1", [2]float64{}
		if id, ok := laneID[lane]; ok {
			parent, off = id, laneBox[lane]
		}
		id := c.next()
		cellOf[n.ID] = id
		c.vertex(id, parent, drawioLabel(n), drawioNodeStyle(n), box.X-off[0], box.Y-off[1], box.W, box.H)
	}

	// edges
	for _, r := range L.Routes {
		e := r.Edge
		es := edgeStyles[e.Kind]
		st := fmt.Sprintf("edgeStyle=orthogonalEdgeStyle;rounded=1;orthogonalLoop=1;jettySize=auto;html=1;strokeColor=%s;strokeWidth=%.1f;endArrow=blockThin;endFill=1;endSize=5;fontSize=10;fontColor=#475569;labelBackgroundColor=#FFFFFF;exitX=1;exitY=0.5;exitDx=0;exitDy=0;entryX=0;entryY=0.5;entryDx=0;entryDy=0;", es.Color, es.Width)
		switch es.Dash {
		case "dash":
			st += "dashed=1;dashPattern=6 4;"
		case "dot":
			st += "dashed=1;dashPattern=1 3;"
		}
		if r.Points[0].X > r.Points[len(r.Points)-1].X { // back edge: leave/enter on the other sides
			st = strings.NewReplacer("exitX=1;", "exitX=0;", "entryX=0;", "entryX=1;").Replace(st)
		}
		label := e.Label
		if e.Kind == model.EdgeTrigger && label == "" {
			label = "triggers"
		} else if e.Kind == model.EdgeTrigger {
			label = "triggers " + label
		}
		if e.Kind == model.EdgeRuns {
			label = "" // step names add noise on the dense make side
		}
		fmt.Fprintf(b, "        <mxCell id=%q value=\"%s\" style=\"%s\" edge=\"1\" parent=\"1\" source=%q target=%q>\n", c.next(), xmlEscape(label), xmlEscape(st), cellOf[e.From], cellOf[e.To])
		b.WriteString("          <mxGeometry relative=\"1\" as=\"geometry\">")
		if len(r.Points) > 2 {
			b.WriteString("\n            <Array as=\"points\">\n")
			for _, p := range r.Points[1 : len(r.Points)-1] {
				fmt.Fprintf(b, "              <mxPoint x=\"%.0f\" y=\"%.0f\" />\n", p.X, p.Y)
			}
			b.WriteString("            </Array>\n          ")
		}
		b.WriteString("</mxGeometry>\n        </mxCell>\n")
	}
	b.WriteString("      </root>\n    </mxGraphModel>\n  </diagram>\n")
}

func drawioNodeStyle(n *model.Node) string {
	s := nodeStyle(n)
	st := fmt.Sprintf("rounded=1;whiteSpace=wrap;html=1;align=left;verticalAlign=top;spacingLeft=10;spacingTop=4;spacingRight=6;fillColor=%s;strokeColor=%s;fontColor=%s;fontSize=11;", s.Fill, s.Stroke, s.Text)
	switch n.Kind {
	case model.KindBuild:
		st += "arcSize=6;strokeWidth=1.6;shadow=1;"
	case model.KindImage:
		st += "arcSize=40;strokeWidth=1.4;"
	case model.KindMake:
		st += "arcSize=10;strokeWidth=1;"
	case model.KindExternal:
		st += "arcSize=6;strokeWidth=1.4;dashed=1;"
	}
	return st
}

func drawioLabel(n *model.Node) string {
	c := cardOf(n)
	var b strings.Builder
	fmt.Fprintf(&b, "<b style=\"font-size:12px\">%s</b>", html.EscapeString(c.Title))
	if c.Sub != "" {
		fmt.Fprintf(&b, "<br><font style=\"font-size:10px\" color=\"#64748B\">%s</font>", html.EscapeString(c.Sub))
	}
	if len(c.Lines) > 0 {
		b.WriteString("<div style=\"margin-top:4px;font-family:monospace;font-size:10px;color:#334155\">")
		for i, l := range c.Lines {
			if i > 0 {
				b.WriteString("<br>")
			}
			b.WriteString(html.EscapeString(l))
		}
		b.WriteString("</div>")
	}
	if len(c.Badges) > 0 {
		fmt.Fprintf(&b, "<div style=\"margin-top:4px;font-size:10px;color:#9A3412\">%s</div>", html.EscapeString(strings.Join(c.Badges, "   ")))
	}
	return b.String()
}

func drawioLegend(c *cellWriter, v View, right float64) {
	present := map[model.EdgeKind]bool{}
	for _, e := range v.Graph.Edges {
		present[e.Kind] = true
	}
	var kinds []model.EdgeKind
	for _, k := range edgeOrder {
		if present[k] {
			kinds = append(kinds, k)
		}
	}
	if len(kinds) == 0 {
		return
	}
	const colW, rowH = 190.0, 18.0
	cols := 2
	rows := (len(kinds) + cols - 1) / cols
	w, h := colW*float64(cols)+20, rowH*float64(rows)+30
	x := max(right-w, 780)
	y := 22.0
	box := c.next()
	c.vertex(box, "1", "", "rounded=1;arcSize=6;fillColor=#FFFFFF;strokeColor=#E2E8F0;html=1;", x, y, w, h)
	c.vertex(c.next(), box, "LEGEND", "text;html=1;fontSize=9;fontStyle=1;fontColor=#94A3B8;strokeColor=none;fillColor=none;align=left;", 10, 2, 100, 16)
	for i, k := range kinds {
		es := edgeStyles[k]
		col, row := i/rows, i%rows
		ex, ey := 10+float64(col)*colW, 22+float64(row)*rowH
		dash := ""
		switch es.Dash {
		case "dash":
			dash = "dashed=1;dashPattern=6 4;"
		case "dot":
			dash = "dashed=1;dashPattern=1 3;"
		}
		id := c.next()
		fmt.Fprintf(c.b, "        <mxCell id=%q value=\"\" style=\"%s\" edge=\"1\" parent=%q>\n", id,
			xmlEscape(fmt.Sprintf("endArrow=blockThin;endFill=1;endSize=5;html=1;strokeColor=%s;strokeWidth=%.1f;%s", es.Color, es.Width, dash)), box)
		fmt.Fprintf(c.b, "          <mxGeometry relative=\"1\" as=\"geometry\"><mxPoint x=\"%.0f\" y=\"%.0f\" as=\"sourcePoint\" /><mxPoint x=\"%.0f\" y=\"%.0f\" as=\"targetPoint\" /></mxGeometry>\n        </mxCell>\n", ex, ey+8, ex+36, ey+8)
		c.vertex(c.next(), box, es.Legend, "text;html=1;fontSize=10;fontColor=#334155;strokeColor=none;fillColor=none;align=left;verticalAlign=middle;", ex+44, ey, colW-50, 16)
	}
}
