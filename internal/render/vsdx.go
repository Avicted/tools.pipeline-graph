package render

import (
	"archive/zip"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/Avicted/tools.pipeline-graph/internal/layout"
	"github.com/Avicted/tools.pipeline-graph/internal/model"
)

// VSDX writes a Visio drawing (one page per view). This is the format Miro
// imports natively; the shapes keep their colours and text, and connectors are
// glued to their shapes so they follow when shapes are moved.
//
// The package is written by hand (OPC zip + Visio 2013 XML): no masters, every
// shape carries its own geometry and formatting.
func VSDX(w io.Writer, g *model.Graph, views []View) error {
	z := zip.NewWriter(w)
	modified, err := time.Parse("2006-01-02 15:04 UTC", g.Generated)
	if err != nil {
		modified = time.Unix(0, 0).UTC()
	}
	add := func(name, content string) error {
		f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: modified})
		if err != nil {
			return err
		}
		_, err = io.WriteString(f, content)
		return err
	}

	var pages, pageRels, overrides strings.Builder
	for i, v := range views {
		n := i + 1
		W, H := v.Layout.W/ppi, v.Layout.H/ppi
		fmt.Fprintf(&pages, `<Page ID="%d" NameU="%s" Name="%s" ViewScale="-1" ViewCenterX="%.4f" ViewCenterY="%.4f"><PageSheet LineStyle="0" FillStyle="0" TextStyle="0">`+
			`<Cell N="PageWidth" V="%.4f"/><Cell N="PageHeight" V="%.4f"/><Cell N="ShdwOffsetX" V="0.125"/><Cell N="ShdwOffsetY" V="-0.125"/>`+
			`<Cell N="PageScale" V="1" U="IN_F"/><Cell N="DrawingScale" V="1" U="IN_F"/><Cell N="DrawingSizeType" V="3"/><Cell N="DrawingScaleType" V="0"/>`+
			`<Cell N="InhibitSnap" V="0"/><Cell N="UIVisibility" V="0"/><Cell N="ShdwType" V="0"/><Cell N="ShdwObliqueAngle" V="0"/><Cell N="ShdwScaleFactor" V="1"/>`+
			`<Cell N="DrawingResizeType" V="1"/><Cell N="PageShapeSplit" V="1"/></PageSheet><Rel r:id="rId%d"/></Page>`,
			i, xmlEscape(v.Title), xmlEscape(v.Title), W/2, H/2, W, H, n)
		fmt.Fprintf(&pageRels, `<Relationship Id="rId%d" Type="http://schemas.microsoft.com/visio/2010/relationships/page" Target="page%d.xml"/>`, n, n)
		fmt.Fprintf(&overrides, `<Override PartName="/visio/pages/page%d.xml" ContentType="application/vnd.ms-visio.page+xml"/>`, n)
	}

	parts := []struct{ name, content string }{
		{"[Content_Types].xml", xmlHeader + `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/visio/document.xml" ContentType="application/vnd.ms-visio.drawing.main+xml"/>` +
			`<Override PartName="/visio/pages/pages.xml" ContentType="application/vnd.ms-visio.pages+xml"/>` +
			overrides.String() +
			`<Override PartName="/visio/windows.xml" ContentType="application/vnd.ms-visio.windows+xml"/>` +
			`<Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>` +
			`<Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` +
			`</Types>`},
		{"_rels/.rels", xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.microsoft.com/visio/2010/relationships/document" Target="visio/document.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>` +
			`<Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>` +
			`</Relationships>`},
		{"docProps/core.xml", xmlHeader + `<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
			`<dc:title>Pipeline map</dc:title><dc:creator>pipeline-graph</dc:creator><dc:description>` + xmlEscape(Subtitle(g)) + `</dc:description>` +
			`<dcterms:modified xsi:type="dcterms:W3CDTF">` + modified.Format(time.RFC3339) + `</dcterms:modified></cp:coreProperties>`},
		{"docProps/app.xml", xmlHeader + `<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties"><Application>Microsoft Visio</Application><AppVersion>16.0000</AppVersion></Properties>`},
		{"visio/_rels/document.xml.rels", xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` +
			`<Relationship Id="rId1" Type="http://schemas.microsoft.com/visio/2010/relationships/pages" Target="pages/pages.xml"/>` +
			`<Relationship Id="rId2" Type="http://schemas.microsoft.com/visio/2010/relationships/windows" Target="windows.xml"/>` +
			`</Relationships>`},
		{"visio/document.xml", xmlHeader + `<VisioDocument xmlns="http://schemas.microsoft.com/office/visio/2012/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xml:space="preserve">` +
			`<DocumentSettings TopPage="0" DefaultTextStyle="0" DefaultLineStyle="0" DefaultFillStyle="0" DefaultGuideStyle="0"><GlueSettings>9</GlueSettings><SnapSettings>65847</SnapSettings><SnapExtensions>34</SnapExtensions><DynamicGridEnabled>0</DynamicGridEnabled></DocumentSettings>` +
			`<Colors/><FaceNames><FaceName NameU="Calibri" UnicodeRanges="-536859905 -1073732485 9 0" CharSets="536871423 0" Panos="2 15 5 2 2 2 4 3 2 4" Flags="325"/><FaceName NameU="Consolas" UnicodeRanges="-536869121 1073786111 1 0" CharSets="1073742335 0" Panos="2 11 6 9 2 2 4 3 2 4" Flags="357"/></FaceNames>` +
			`<StyleSheets>` + vsdxNoStyle + `</StyleSheets></VisioDocument>`},
		{"visio/windows.xml", xmlHeader + `<Windows ClientWidth="1600" ClientHeight="900" xmlns="http://schemas.microsoft.com/office/visio/2012/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xml:space="preserve">` +
			`<Window ID="0" WindowType="Drawing" WindowState="1073741824" WindowLeft="-8" WindowTop="-31" WindowWidth="1616" WindowHeight="939" ContainerType="Page" Page="0" ViewScale="-1" ViewCenterX="0" ViewCenterY="0"><ShowRulers>1</ShowRulers><ShowGrid>0</ShowGrid><ShowPageBreaks>0</ShowPageBreaks><ShowGuides>1</ShowGuides><ShowConnectionPoints>0</ShowConnectionPoints><GlueSettings>9</GlueSettings><SnapSettings>65847</SnapSettings><SnapExtensions>34</SnapExtensions><DynamicGridEnabled>0</DynamicGridEnabled><TabSplitterPos>0.5</TabSplitterPos></Window>` +
			`</Windows>`},
		{"visio/pages/pages.xml", xmlHeader + `<Pages xmlns="http://schemas.microsoft.com/office/visio/2012/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xml:space="preserve">` + pages.String() + `</Pages>`},
		{"visio/pages/_rels/pages.xml.rels", xmlHeader + `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + pageRels.String() + `</Relationships>`},
	}
	// [Content_Types].xml first: some OPC readers expect it at the start of the zip
	for _, p := range parts {
		if err := add(p.name, p.content); err != nil {
			return err
		}
	}
	for i, v := range views {
		if err := add(fmt.Sprintf("visio/pages/page%d.xml", i+1), vsdxPage(g, v)); err != nil {
			return err
		}
	}
	return z.Close()
}

const (
	xmlHeader = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` + "\n"
	ppi       = 96.0 // layout pixels per Visio inch
	pt        = 1.0 / 72
)

// vsdxNoStyle is the mandatory style 0 that every shape inherits from.
const vsdxNoStyle = `<StyleSheet ID="0" NameU="No Style" Name="No Style">` +
	`<Cell N="EnableLineProps" V="1"/><Cell N="EnableFillProps" V="1"/><Cell N="EnableTextProps" V="1"/><Cell N="HideForApply" V="0"/>` +
	`<Cell N="LineWeight" V="0.01041666666666667"/><Cell N="LineColor" V="0"/><Cell N="LinePattern" V="1"/><Cell N="Rounding" V="0"/>` +
	`<Cell N="EndArrowSize" V="2"/><Cell N="BeginArrow" V="0"/><Cell N="EndArrow" V="0"/><Cell N="LineCap" V="0"/><Cell N="BeginArrowSize" V="2"/>` +
	`<Cell N="LineColorTrans" V="0"/><Cell N="CompoundType" V="0"/><Cell N="FillForegnd" V="1"/><Cell N="FillBkgnd" V="0"/><Cell N="FillPattern" V="1"/>` +
	`<Cell N="ShdwForegnd" V="0"/><Cell N="ShdwPattern" V="0"/><Cell N="FillForegndTrans" V="0"/><Cell N="FillBkgndTrans" V="0"/><Cell N="ShdwForegndTrans" V="0"/>` +
	`<Cell N="LeftMargin" V="0"/><Cell N="RightMargin" V="0"/><Cell N="TopMargin" V="0"/><Cell N="BottomMargin" V="0"/><Cell N="VerticalAlign" V="1"/>` +
	`<Cell N="TextBkgnd" V="0"/><Cell N="DefaultTabStop" V="0.5"/><Cell N="TextDirection" V="0"/><Cell N="TextBkgndTrans" V="0"/>` +
	`<Cell N="NoObjHandles" V="0"/><Cell N="NonPrinting" V="0"/><Cell N="NoCtlHandles" V="0"/><Cell N="NoAlignBox" V="0"/><Cell N="UpdateAlignBox" V="0"/><Cell N="HideText" V="0"/>` +
	`<Cell N="DynFeedback" V="0"/><Cell N="GlueType" V="0"/><Cell N="WalkPreference" V="0"/><Cell N="BegTrigger" V="0" F="No Formula"/><Cell N="EndTrigger" V="0" F="No Formula"/>` +
	`<Cell N="ObjType" V="0"/><Cell N="Comment" V=""/><Cell N="IsDropSource" V="0"/><Cell N="NoLiveDynamics" V="0"/><Cell N="LocalizeMerge" V="0"/>` +
	`<Cell N="Calendar" V="0"/><Cell N="LangID" V="en-US"/><Cell N="ShapeKeywords" V=""/><Cell N="DropOnPageScale" V="1"/>` +
	`<Section N="Character"><Row IX="0"><Cell N="Font" V="Calibri"/><Cell N="Color" V="0"/><Cell N="Style" V="0"/><Cell N="Case" V="0"/><Cell N="Pos" V="0"/><Cell N="FontScale" V="1"/><Cell N="Size" V="0.1666666666666667"/><Cell N="DblUnderline" V="0"/><Cell N="Overline" V="0"/><Cell N="Strikethru" V="0"/><Cell N="DoubleStrikethrough" V="0"/><Cell N="Letterspace" V="0"/><Cell N="ColorTrans" V="0"/><Cell N="AsianFont" V="0"/><Cell N="ComplexScriptFont" V="0"/><Cell N="ComplexScriptSize" V="-1"/><Cell N="LangID" V="en-US"/></Row></Section>` +
	`<Section N="Paragraph"><Row IX="0"><Cell N="IndFirst" V="0"/><Cell N="IndLeft" V="0"/><Cell N="IndRight" V="0"/><Cell N="SpLine" V="-1.2"/><Cell N="SpBefore" V="0"/><Cell N="SpAfter" V="0"/><Cell N="HorzAlign" V="1"/><Cell N="Bullet" V="0"/><Cell N="BulletStr" V=""/><Cell N="BulletFont" V="0"/><Cell N="BulletFontSize" V="-1"/><Cell N="TextPosAfterBullet" V="0"/><Cell N="Flags" V="0"/></Row></Section>` +
	`</StyleSheet>`

// run is a piece of text with its own character formatting.
type run struct {
	text  string
	color string
	size  float64 // points
	bold  bool
	mono  bool
}

type vshape struct {
	b          *strings.Builder
	id         int
	pageH      float64
	x, y, w, h float64 // layout pixels, top-left origin
}

type vstyle struct {
	fill, line string
	weight     float64 // points
	pattern    int     // 1 solid, 2 dash, 3 dot
	rounding   float64 // pixels
	noFill     bool
	noLine     bool
	shadow     bool
	align      int // 0 left, 1 centre
	valign     int // 0 top, 1 middle
	margin     float64
}

func cell(b *strings.Builder, name string, v any) {
	switch x := v.(type) {
	case float64:
		fmt.Fprintf(b, `<Cell N="%s" V="%.6g"/>`, name, x)
	case int:
		fmt.Fprintf(b, `<Cell N="%s" V="%d"/>`, name, x)
	default:
		fmt.Fprintf(b, `<Cell N="%s" V="%s"/>`, name, xmlEscape(fmt.Sprint(x)))
	}
}

// box writes a 2-D shape: a (rounded) rectangle with formatted text.
func (s *vshape) box(st vstyle, runs []run) {
	b := s.b
	w, h := s.w/ppi, s.h/ppi
	px, py := (s.x+s.w/2)/ppi, s.pageH-(s.y+s.h/2)/ppi
	fmt.Fprintf(b, `<Shape ID="%d" Type="Shape" LineStyle="0" FillStyle="0" TextStyle="0">`, s.id)
	cell(b, "PinX", px)
	cell(b, "PinY", py)
	cell(b, "Width", w)
	cell(b, "Height", h)
	cell(b, "LocPinX", w/2)
	cell(b, "LocPinY", h/2)
	cell(b, "Angle", 0.0)
	cell(b, "FillForegnd", orDefault(st.fill, "#FFFFFF"))
	cell(b, "FillPattern", 1)
	cell(b, "LineColor", orDefault(st.line, "#000000"))
	cell(b, "LineWeight", st.weight*pt)
	cell(b, "LinePattern", max(st.pattern, 1))
	cell(b, "Rounding", st.rounding/ppi)
	if st.shadow {
		cell(b, "ShdwPattern", 1)
		cell(b, "ShdwForegnd", "#CBD5E1")
		cell(b, "ShapeShdwOffsetX", 0.02)
		cell(b, "ShapeShdwOffsetY", -0.02)
		cell(b, "ShapeShdwType", 1)
	}
	m := st.margin / ppi
	cell(b, "LeftMargin", m)
	cell(b, "RightMargin", m)
	cell(b, "TopMargin", m*0.7)
	cell(b, "BottomMargin", m*0.7)
	cell(b, "VerticalAlign", st.valign)
	writeText(b, runs, st.align)
	if !st.noFill || !st.noLine {
		b.WriteString(`<Section N="Geometry" IX="0">`)
		cell(b, "NoFill", boolInt(st.noFill))
		cell(b, "NoLine", boolInt(st.noLine))
		cell(b, "NoShow", 0)
		cell(b, "NoSnap", 0)
		cell(b, "NoQuickDrag", 0)
		b.WriteString(`<Row T="RelMoveTo" IX="1"><Cell N="X" V="0"/><Cell N="Y" V="0"/></Row>` +
			`<Row T="RelLineTo" IX="2"><Cell N="X" V="1"/><Cell N="Y" V="0"/></Row>` +
			`<Row T="RelLineTo" IX="3"><Cell N="X" V="1"/><Cell N="Y" V="1"/></Row>` +
			`<Row T="RelLineTo" IX="4"><Cell N="X" V="0"/><Cell N="Y" V="1"/></Row>` +
			`<Row T="RelLineTo" IX="5"><Cell N="X" V="0"/><Cell N="Y" V="0"/></Row></Section>`)
	}
	writeRuns(b, runs)
	b.WriteString(`</Shape>`)
}

func writeText(b *strings.Builder, runs []run, align int) {
	if len(runs) == 0 {
		return
	}
	b.WriteString(`<Section N="Character">`)
	for i, r := range runs {
		fmt.Fprintf(b, `<Row IX="%d">`, i)
		font := "Calibri"
		if r.mono {
			font = "Consolas"
		}
		cell(b, "Font", font)
		cell(b, "Color", orDefault(r.color, "#0F172A"))
		cell(b, "Style", boolInt(r.bold))
		cell(b, "Size", r.size*pt)
		b.WriteString(`</Row>`)
	}
	b.WriteString(`</Section><Section N="Paragraph"><Row IX="0">`)
	cell(b, "HorzAlign", align)
	cell(b, "SpLine", -1.1)
	b.WriteString(`</Row></Section>`)
}

// vsdxSymbols swaps the decorative symbols for ASCII. Some Visio readers
// (LibreOffice's, at least) count text-run offsets in bytes, so every
// multi-byte character shifts the formatting of the following runs.
var vsdxSymbols = strings.NewReplacer(
	"🐳 ", "", "→", "->", " · ", " | ", "▸ ", "> ", "…", "...",
	"⎇ ", "", "⏱ ", "", "▣ ", "", "◆ ", "", "⚡ ", "",
)

func writeRuns(b *strings.Builder, runs []run) {
	if len(runs) == 0 {
		return
	}
	b.WriteString(`<Text>`)
	for i, r := range runs {
		fmt.Fprintf(b, `<cp IX="%d"/>%s`, i, xmlEscape(vsdxSymbols.Replace(r.text)))
		if i < len(runs)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString(`</Text>`)
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func orDefault(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func vsdxPage(g *model.Graph, v View) string {
	L := v.Layout
	var b strings.Builder
	b.WriteString(xmlHeader + `<PageContents xmlns="http://schemas.microsoft.com/office/visio/2012/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xml:space="preserve"><Shapes>`)
	pageH := L.H / ppi
	id := 0
	shape := func(x, y, w, h float64) *vshape {
		id++
		return &vshape{b: &b, id: id, pageH: pageH, x: x, y: y, w: w, h: h}
	}
	m := 30.0

	// title block
	shape(m, m-6, 640, 32).box(vstyle{noFill: true, noLine: true, valign: 0},
		[]run{{text: "Pipeline map · " + v.Title, size: 16, bold: true}})
	shape(m, m+26, 720, 34).box(vstyle{noFill: true, noLine: true, valign: 0},
		[]run{{text: Subtitle(g), size: 8, color: "#64748B"}})
	vsdxLegend(&b, &id, pageH, v, L.W-m)

	// lanes: background band plus a coloured title strip
	for i, lane := range L.Lanes {
		fill := "#F8FAFC"
		if i%2 == 1 {
			fill = "#FFFFFF"
		}
		head := "#1E3A8A"
		if lane.Name == ExternalLane {
			head = "#9A3412"
		}
		lb := lane.Box
		shape(lb.X, lb.Y, lb.W, lb.H).box(vstyle{fill: fill, line: "#CBD5E1", weight: 0.75, rounding: 8}, nil)
		shape(lb.X, lb.Y, 44, lb.H).box(vstyle{fill: head, line: head, weight: 0.75, rounding: 8}, nil)
		// Miro drops rotated text, so the lane name is a horizontal title in the lane's top margin
		shape(lb.X+54, lb.Y+2, 400, 24).box(vstyle{noFill: true, noLine: true},
			[]run{{text: lane.Name, size: 11, bold: true, color: head}})
	}

	// nodes
	shapeOf := map[string]int{}
	for _, n := range v.Graph.Nodes {
		box := L.Nodes[n.ID]
		s := shape(box.X, box.Y, box.W, box.H)
		shapeOf[n.ID] = s.id
		ns := nodeStyle(n)
		st := vstyle{fill: ns.Fill, line: ns.Stroke, weight: 1.2, rounding: 6, margin: 9}
		switch n.Kind {
		case model.KindBuild:
			st.shadow = true
		case model.KindImage:
			st.rounding = 16
		case model.KindMake:
			st.weight = 0.75
		case model.KindExternal:
			st.pattern = 2
		}
		c := CardOf(n)
		runs := []run{{text: c.Title, size: 9, bold: true, color: ns.Text}}
		if c.Sub != "" {
			runs = append(runs, run{text: c.Sub, size: 7, color: "#64748B"})
		}
		if len(c.Lines) > 0 {
			runs = append(runs, run{text: strings.Join(c.Lines, "\n"), size: 7, color: "#334155", mono: true})
		}
		if len(c.Badges) > 0 {
			runs = append(runs, run{text: strings.Join(c.Badges, "   "), size: 7, color: "#9A3412"})
		}
		s.box(st, runs)
	}

	// connectors
	var connects strings.Builder
	for _, r := range L.Routes {
		id++
		vsdxConnector(&b, id, pageH, r, edgeLabel(r.Edge))
		fmt.Fprintf(&connects, `<Connect FromSheet="%d" FromCell="BeginX" FromPart="9" ToSheet="%d" ToCell="PinX" ToPart="3"/>`, id, shapeOf[r.Edge.From])
		fmt.Fprintf(&connects, `<Connect FromSheet="%d" FromCell="EndX" FromPart="12" ToSheet="%d" ToCell="PinX" ToPart="3"/>`, id, shapeOf[r.Edge.To])
	}
	b.WriteString(`</Shapes><Connects>` + connects.String() + `</Connects></PageContents>`)
	return b.String()
}

func linePattern(dash string) int {
	switch dash {
	case "dash":
		return 2
	case "dot":
		return 3
	}
	return 1
}

// vsdxConnector writes a 1-D shape from the first to the last route point,
// following the route's bends. Geometry is in the connector's local frame:
// x along the begin->end axis, y perpendicular to it.
func vsdxConnector(b *strings.Builder, id int, pageH float64, r layout.Route, label string) {
	es := edgeStyles[r.Edge.Kind]
	pts := make([][2]float64, len(r.Points))
	for i, p := range r.Points {
		pts[i] = [2]float64{p.X / ppi, pageH - p.Y/ppi}
	}
	bx, by := pts[0][0], pts[0][1]
	ex, ey := pts[len(pts)-1][0], pts[len(pts)-1][1]
	dx, dy := ex-bx, ey-by
	length := math.Hypot(dx, dy)
	if length == 0 {
		length = 1e-6
	}
	ux, uy := dx/length, dy/length
	fmt.Fprintf(b, `<Shape ID="%d" Type="Shape" LineStyle="0" FillStyle="0" TextStyle="0">`, id)
	cell(b, "PinX", (bx+ex)/2)
	cell(b, "PinY", (by+ey)/2)
	cell(b, "Width", length)
	cell(b, "Height", 0.0)
	cell(b, "LocPinX", length/2)
	cell(b, "LocPinY", 0.0)
	cell(b, "Angle", math.Atan2(dy, dx))
	cell(b, "BeginX", bx)
	cell(b, "BeginY", by)
	cell(b, "EndX", ex)
	cell(b, "EndY", ey)
	cell(b, "ObjType", 2)
	cell(b, "LineColor", es.Color)
	cell(b, "LineWeight", es.Width*0.75*pt)
	cell(b, "LinePattern", linePattern(es.Dash))
	cell(b, "EndArrow", 13)
	cell(b, "EndArrowSize", 1)
	cell(b, "Rounding", 0.08)
	cell(b, "TxtPinX", length/2)
	cell(b, "TxtPinY", 0.0)
	cell(b, "TxtWidth", math.Min(length, 2.5))
	cell(b, "TxtHeight", 0.2)
	cell(b, "TxtLocPinX", math.Min(length, 2.5)/2)
	cell(b, "TxtLocPinY", 0.1)
	cell(b, "TextBkgnd", "#FFFFFF")
	var runs []run
	if label != "" {
		if len(label) > 40 {
			label = label[:38] + "..."
		}
		runs = []run{{text: label, size: 7, color: "#475569"}}
	}
	writeText(b, runs, 1)
	b.WriteString(`<Section N="Geometry" IX="0"><Cell N="NoFill" V="1"/><Cell N="NoLine" V="0"/><Cell N="NoShow" V="0"/><Cell N="NoSnap" V="0"/><Cell N="NoQuickDrag" V="0"/>`)
	for i, p := range pts {
		px, py := p[0]-bx, p[1]-by
		lx, ly := px*ux+py*uy, -px*uy+py*ux
		t := "LineTo"
		if i == 0 {
			t = "MoveTo"
		}
		fmt.Fprintf(b, `<Row T="%s" IX="%d"><Cell N="X" V="%.6g"/><Cell N="Y" V="%.6g"/></Row>`, t, i+1, lx, ly)
	}
	b.WriteString(`</Section>`)
	writeRuns(b, runs)
	b.WriteString(`</Shape>`)
}

func vsdxLegend(b *strings.Builder, id *int, pageH float64, v View, right float64) {
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
	rows := (len(kinds) + 1) / 2
	w, h := colW*2+20, rowH*float64(rows)+30
	x, y := max(right-w, 780), 22.0
	*id++
	(&vshape{b: b, id: *id, pageH: pageH, x: x, y: y, w: w, h: h}).box(vstyle{fill: "#FFFFFF", line: "#E2E8F0", weight: 0.75, rounding: 6}, nil)
	*id++
	(&vshape{b: b, id: *id, pageH: pageH, x: x + 10, y: y + 2, w: 100, h: 16}).box(vstyle{noFill: true, noLine: true},
		[]run{{text: "LEGEND", size: 7, bold: true, color: "#94A3B8"}})
	for i, k := range kinds {
		es := edgeStyles[k]
		col, row := i/rows, i%rows
		ex, ey := x+10+float64(col)*colW, y+22+float64(row)*rowH
		*id++
		vsdxConnector(b, *id, pageH, layout.Route{Edge: model.Edge{Kind: k}, Points: []layout.Pt{{X: ex, Y: ey + 8}, {X: ex + 36, Y: ey + 8}}}, "")
		*id++
		(&vshape{b: b, id: *id, pageH: pageH, x: ex + 44, y: ey, w: colW - 50, h: 16}).box(vstyle{noFill: true, noLine: true},
			[]run{{text: es.Legend, size: 8, color: "#334155"}})
	}
}
