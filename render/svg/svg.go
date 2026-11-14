// Package svg reads the static subset of SVG that badges, logos and icons use and draws it as vectors: shapes, paths,
// text, transforms, linear gradients and a rect clip. Scripts, filters, patterns and CSS stylesheets are ignored.
package svg

import (
	"bytes"
	"cmp"
	"encoding/xml"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"

	"github.com/liamg/oriel/render"
)

// Doc is a parsed SVG, its shapes in its viewBox's units.
type Doc struct {
	Size  render.Point // its own size, in logical pixels
	view  render.Rect
	items []item

	mu    sync.Mutex
	drawn []drawnAt // the paths placed for the rects drawn into lately, the latest last
}

// drawnAt is each item's path placed into r; nil for items that aren't paths.
type drawnAt struct {
	r     render.Rect
	paths []*render.Path
}

type itemKind uint8

const (
	itemPath itemKind = iota
	itemText
)

// item is one thing a Doc draws, in viewBox units.
type item struct {
	kind   itemKind
	path   *render.Path
	rect   *render.Rect // set when the path is an axis-aligned rect, so a gradient can fill it
	radius float32
	fill   paint
	stroke paint
	width  float32
	clip   *clipRect

	text   string
	at     render.Point
	size   float32 // font size in viewBox units
	family string
	weight int
	anchor string
	fitTo  float32 // textLength in viewBox units, or 0
}

type clipRect struct {
	rect   render.Rect
	radius float32
}

// paint is a fill or stroke: a colour, or a gradient's ends.
type paint struct {
	set  bool
	none bool
	c    render.Color
	grad *render.Gradient
}

// node is an element as read, before styles and references are resolved.
type node struct {
	name string
	attr map[string]string
	kids []*node
	text strings.Builder
}

const maxNodes = 50000

// Parse reads an SVG document.
func Parse(b []byte) (*Doc, error) {
	root, err := readTree(b)
	if err != nil {
		return nil, err
	}
	if root == nil || root.name != "svg" {
		return nil, errors.New("not an SVG document")
	}
	ids := map[string]*node{}
	var index func(n *node)
	index = func(n *node) {
		if id := n.attr["id"]; id != "" {
			ids[id] = n
		}
		for _, k := range n.kids {
			index(k)
		}
	}
	index(root)

	d := &Doc{}
	w, wok := length(root.attr["width"])
	h, hok := length(root.attr["height"])
	if vb := numbers(root.attr["viewBox"]); len(vb) == 4 && vb[2] > 0 && vb[3] > 0 {
		d.view = render.XYWH(vb[0], vb[1], vb[2], vb[3])
		switch {
		case wok && hok:
		case wok:
			h = w * vb[3] / vb[2]
		case hok:
			w = h * vb[2] / vb[3]
		default:
			w, h = vb[2], vb[3]
		}
	} else {
		if !wok {
			w = 300
		}
		if !hok {
			h = 150
		}
		d.view = render.XYWH(0, 0, w, h)
	}
	if w <= 0 || h <= 0 {
		return nil, errors.New("an SVG with no size")
	}
	d.Size = render.Point{X: w, Y: h}
	r := &reader{doc: d, ids: ids}
	r.walk(root, defaultStyle(), identity, nil, 0)
	return d, nil
}

func readTree(b []byte) (*node, error) {
	dec := xml.NewDecoder(bytes.NewReader(b))
	dec.Strict = false
	dec.Entity = xml.HTMLEntity
	var stack []*node
	var root *node
	count := 0
	for {
		tok, err := dec.Token()
		if err != nil {
			if root != nil && errors.Is(err, io.EOF) {
				return root, nil
			}
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if count++; count > maxNodes {
				return nil, errors.New("an SVG too large to draw")
			}
			n := &node{name: t.Name.Local, attr: map[string]string{}}
			for _, a := range t.Attr {
				n.attr[a.Name.Local] = a.Value
			}
			if len(stack) > 0 {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			} else if root == nil {
				root = n
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if len(stack) == 0 && root != nil {
				return root, nil
			}
		case xml.CharData:
			for _, n := range stack {
				if n.name == "text" || n.name == "tspan" {
					n.text.Write(t)
				}
			}
		}
	}
}

// style is the presentation an element inherits and sets.
type style struct {
	fill, stroke         paint
	fillRef, strokeRef   string
	width                float32
	fillOp, strokeOp, op float32
	fontSize             float32
	family, anchor       string
	weight               int
	hidden               bool
}

func defaultStyle() style {
	return style{fill: paint{set: true, c: render.Color{A: 255}}, stroke: paint{set: true, none: true}, width: 1, fillOp: 1, strokeOp: 1,
		op: 1, fontSize: 16, weight: 400}
}

// props are an element's presentation attributes with its style attribute over them.
func props(n *node) map[string]string {
	p := map[string]string{}
	for _, k := range []string{"fill", "stroke", "stroke-width", "opacity", "fill-opacity", "stroke-opacity", "font-size", "font-family",
		"font-weight", "text-anchor", "display", "visibility", "clip-path", "mask", "stop-color", "stop-opacity"} {
		if v, ok := n.attr[k]; ok {
			p[k] = strings.TrimSpace(v)
		}
	}
	for decl := range strings.SplitSeq(n.attr["style"], ";") {
		k, v, ok := strings.Cut(decl, ":")
		if ok {
			p[strings.TrimSpace(k)] = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "!important"))
		}
	}
	return p
}

func (s style) with(p map[string]string) style {
	s.op = 1
	if v, ok := p["fill"]; ok {
		s.fill, s.fillRef = parsePaint(v)
	}
	if v, ok := p["stroke"]; ok {
		s.stroke, s.strokeRef = parsePaint(v)
	}
	if v, ok := length(p["stroke-width"]); ok {
		s.width = v
	}
	if v, ok := fraction(p["opacity"]); ok {
		s.op = v
	}
	if v, ok := fraction(p["fill-opacity"]); ok {
		s.fillOp = v
	}
	if v, ok := fraction(p["stroke-opacity"]); ok {
		s.strokeOp = v
	}
	if v, ok := length(p["font-size"]); ok {
		s.fontSize = v
	}
	if v := p["font-family"]; v != "" {
		s.family = v
	}
	switch v := p["font-weight"]; v {
	case "":
	case "bold", "bolder":
		s.weight = 700
	case "normal", "lighter":
		s.weight = 400
	default:
		if n, err := strconv.Atoi(v); err == nil {
			s.weight = n
		}
	}
	if v := p["text-anchor"]; v != "" {
		s.anchor = v
	}
	if p["display"] == "none" || p["visibility"] == "hidden" {
		s.hidden = true
	}
	return s
}

// reader turns the element tree into items.
type reader struct {
	doc   *Doc
	ids   map[string]*node
	depth int
}

func (r *reader) walk(n *node, s style, m matrix, clip *clipRect, op float32) {
	if r.depth > 64 {
		return
	}
	r.depth++
	defer func() { r.depth-- }()
	switch n.name {
	case "defs", "clipPath", "mask", "linearGradient", "radialGradient", "pattern", "symbol", "marker", "title", "desc",
		"metadata", "style", "script", "filter":
		return
	}
	p := props(n)
	s = s.with(p)
	if s.hidden {
		return
	}
	if op == 0 {
		op = 1
	}
	op *= s.op
	if t, ok := n.attr["transform"]; ok {
		m = m.mul(parseTransform(t))
	}
	for _, k := range []string{"clip-path", "mask"} {
		if id := urlRef(p[k]); id != "" {
			if c := r.clipOf(id, m); c != nil {
				clip = c
			}
		}
	}
	switch n.name {
	case "svg", "g", "a", "switch":
		if n.name == "svg" && r.depth > 1 {
			m = m.mul(translate(attr(n, "x"), attr(n, "y")))
		}
		for _, k := range n.kids {
			r.walk(k, s, m, clip, op)
		}
		return
	case "use":
		ref := n.attr["href"]
		if t := r.ids[strings.TrimPrefix(ref, "#")]; strings.HasPrefix(ref, "#") && t != nil && t != n {
			m = m.mul(translate(attr(n, "x"), attr(n, "y")))
			if t.name == "symbol" {
				t = &node{name: "g", attr: t.attr, kids: t.kids}
			}
			r.walk(t, s, m, clip, op)
		}
		return
	case "text":
		r.text(n, s, m, clip, op)
		return
	}
	path, rect, radius := shapeOf(n)
	if path == nil {
		return
	}
	it := item{kind: itemPath, path: m.path(path), clip: clip}
	if rect != nil && m.axisAligned() {
		rr := m.rect(*rect)
		it.rect, it.radius = &rr, radius*m.scale()
	}
	it.fill = r.resolve(s.fill, s.fillRef, s.fillOp*op)
	it.stroke = r.resolve(s.stroke, s.strokeRef, s.strokeOp*op)
	it.width = s.width * m.scale()
	if n.name == "line" || n.name == "polyline" {
		it.fill = paint{set: true, none: true}
	}
	r.doc.items = append(r.doc.items, it)
}

func (r *reader) text(n *node, s style, m matrix, clip *clipRect, op float32) {
	x, y := attr(n, "x"), attr(n, "y")
	for _, k := range n.kids {
		if k.name == "tspan" {
			if _, ok := k.attr["x"]; ok {
				x = attr(k, "x")
			}
			if _, ok := k.attr["y"]; ok {
				y = attr(k, "y")
			}
			s = s.with(props(k))
			break
		}
	}
	txt := strings.Join(strings.Fields(n.text.String()), " ")
	if txt == "" {
		return
	}
	k := m.scale()
	it := item{kind: itemText, text: txt, at: m.apply(render.Point{X: x, Y: y}), size: s.fontSize * k, family: s.family, weight: s.weight,
		anchor: s.anchor, clip: clip}
	if l, ok := length(n.attr["textLength"]); ok {
		it.fitTo = l * k
	}
	it.fill = r.resolve(s.fill, s.fillRef, s.fillOp*op)
	r.doc.items = append(r.doc.items, it)
}

// clipOf is a clip path or mask that is one rect, in viewBox units; nil for any other.
func (r *reader) clipOf(id string, m matrix) *clipRect {
	c := r.ids[id]
	if c == nil || len(c.kids) != 1 || c.kids[0].name != "rect" || !m.axisAligned() {
		return nil
	}
	el := c.kids[0]
	if t, ok := el.attr["transform"]; ok {
		m = m.mul(parseTransform(t))
	}
	_, rect, radius := shapeOf(el)
	if rect == nil {
		return nil
	}
	return &clipRect{rect: m.rect(*rect), radius: radius * m.scale()}
}

// resolve is a paint with its gradient reference looked up and its opacity applied.
func (r *reader) resolve(p paint, ref string, op float32) paint {
	if ref != "" {
		if g := r.gradient(ref); g != nil {
			g.From.A = uint8(float32(g.From.A) * op)
			g.To.A = uint8(float32(g.To.A) * op)
			return paint{set: true, grad: g, c: mix(g.From, g.To)}
		}
		if !p.set {
			return paint{set: true, none: true}
		}
	}
	p.c.A = uint8(float32(p.c.A) * op)
	return p
}

// gradient is a linear gradient as its first and last stops, across its axis; a radial one is its stops' average.
func (r *reader) gradient(id string) *render.Gradient {
	n := r.ids[id]
	if n == nil {
		return nil
	}
	stopsOf := n
	for range 8 {
		if hasStops(stopsOf) {
			break
		}
		next := r.ids[strings.TrimPrefix(stopsOf.attr["href"], "#")]
		if next == nil {
			break
		}
		stopsOf = next
	}
	var stops []render.Color
	for _, k := range stopsOf.kids {
		if k.name != "stop" {
			continue
		}
		p := props(k)
		c, _ := parseColor(cmp.Or(p["stop-color"], "black"))
		if o, ok := fraction(p["stop-opacity"]); ok {
			c.A = uint8(float32(c.A) * o)
		}
		stops = append(stops, c)
	}
	if len(stops) == 0 {
		return nil
	}
	g := &render.Gradient{From: stops[0], To: stops[len(stops)-1]}
	if n.name == "radialGradient" {
		g.From = mix(g.From, g.To)
		g.To = g.From
		return g
	}
	x1, y1 := gradCoord(n.attr["x1"], 0), gradCoord(n.attr["y1"], 0)
	x2, y2 := gradCoord(n.attr["x2"], 1), gradCoord(n.attr["y2"], 0)
	dx, dy := x2-x1, y2-y1
	g.Horizontal = math.Abs(float64(dx)) >= math.Abs(float64(dy))
	if g.Horizontal && dx < 0 || !g.Horizontal && dy < 0 {
		g.From, g.To = g.To, g.From
	}
	return g
}

func hasStops(n *node) bool {
	for _, k := range n.kids {
		if k.name == "stop" {
			return true
		}
	}
	return false
}

func gradCoord(s string, def float32) float32 {
	s = strings.TrimSpace(s)
	if s == "" {
		return def
	}
	if v, ok := strings.CutSuffix(s, "%"); ok {
		f, err := strconv.ParseFloat(v, 32)
		if err != nil {
			return def
		}
		return float32(f) / 100
	}
	f, err := strconv.ParseFloat(s, 32)
	if err != nil {
		return def
	}
	return float32(f)
}

// shapeOf is a basic shape's outline, and for a rect the rect and its corner radius.
func shapeOf(n *node) (*render.Path, *render.Rect, float32) {
	switch n.name {
	case "path":
		p := parsePath(n.attr["d"])
		if len(p.Segs) == 0 {
			return nil, nil, 0
		}
		return p, nil, 0
	case "rect":
		x, y, w, h := attr(n, "x"), attr(n, "y"), attr(n, "width"), attr(n, "height")
		if w <= 0 || h <= 0 {
			return nil, nil, 0
		}
		rx, rxok := length(n.attr["rx"])
		ry, ryok := length(n.attr["ry"])
		if !rxok {
			rx = ry
		}
		if !ryok {
			ry = rx
		}
		rx, ry = min(max(rx, 0), w/2), min(max(ry, 0), h/2)
		rect := render.XYWH(x, y, w, h)
		return roundRect(rect, rx, ry), &rect, min(rx, ry)
	case "circle":
		cx, cy, rr := attr(n, "cx"), attr(n, "cy"), attr(n, "r")
		if rr <= 0 {
			return nil, nil, 0
		}
		return render.Ellipse(render.XYWH(cx-rr, cy-rr, 2*rr, 2*rr)), nil, 0
	case "ellipse":
		cx, cy, rx, ry := attr(n, "cx"), attr(n, "cy"), attr(n, "rx"), attr(n, "ry")
		if rx <= 0 || ry <= 0 {
			return nil, nil, 0
		}
		return render.Ellipse(render.XYWH(cx-rx, cy-ry, 2*rx, 2*ry)), nil, 0
	case "line":
		p := new(render.Path).MoveTo(render.Point{X: attr(n, "x1"), Y: attr(n, "y1")}).LineTo(render.Point{X: attr(n, "x2"), Y: attr(n, "y2")})
		return p, nil, 0
	case "polyline", "polygon":
		v := numbers(n.attr["points"])
		if len(v) < 4 {
			return nil, nil, 0
		}
		p := new(render.Path).MoveTo(render.Point{X: v[0], Y: v[1]})
		for i := 2; i+1 < len(v); i += 2 {
			p.LineTo(render.Point{X: v[i], Y: v[i+1]})
		}
		if n.name == "polygon" {
			p.Close()
		}
		return p, nil, 0
	}
	return nil, nil, 0
}

func roundRect(r render.Rect, rx, ry float32) *render.Path {
	p := new(render.Path)
	if rx <= 0 || ry <= 0 {
		return p.MoveTo(r.Min).LineTo(render.Point{X: r.Max.X, Y: r.Min.Y}).LineTo(r.Max).LineTo(render.Point{X: r.Min.X, Y: r.Max.Y}).Close()
	}
	const k = 0.5523
	x0, y0, x1, y1 := r.Min.X, r.Min.Y, r.Max.X, r.Max.Y
	pt := func(x, y float32) render.Point { return render.Point{X: x, Y: y} }
	p.MoveTo(pt(x0+rx, y0)).LineTo(pt(x1-rx, y0))
	p.CubeTo(pt(x1-rx+rx*k, y0), pt(x1, y0+ry-ry*k), pt(x1, y0+ry)).LineTo(pt(x1, y1-ry))
	p.CubeTo(pt(x1, y1-ry+ry*k), pt(x1-rx+rx*k, y1), pt(x1-rx, y1)).LineTo(pt(x0+rx, y1))
	p.CubeTo(pt(x0+rx-rx*k, y1), pt(x0, y1-ry+ry*k), pt(x0, y1-ry)).LineTo(pt(x0, y0+ry))
	p.CubeTo(pt(x0, y0+ry-ry*k), pt(x0+rx-rx*k, y0), pt(x0+rx, y0))
	return p.Close()
}

func attr(n *node, k string) float32 {
	v, _ := length(n.attr[k])
	return v
}

// length is a number with an optional px, pt or em unit; a percentage or other unit isn't one.
func length(s string) (float32, bool) {
	s = strings.TrimSpace(s)
	mul := float32(1)
	switch {
	case strings.HasSuffix(s, "px"):
		s = s[:len(s)-2]
	case strings.HasSuffix(s, "pt"):
		s, mul = s[:len(s)-2], 4.0/3
	case strings.HasSuffix(s, "em"):
		s, mul = s[:len(s)-2], 16
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 32)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return float32(f) * mul, true
}

// fraction is an opacity, as a number or a percentage, clamped to 0…1.
func fraction(s string) (float32, bool) {
	s = strings.TrimSpace(s)
	div := 1.0
	if v, ok := strings.CutSuffix(s, "%"); ok {
		s, div = v, 100
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return float32(min(max(f/div, 0), 1)), true
}

// numbers are the numbers in a list separated by commas or spaces.
func numbers(s string) []float32 {
	var out []float32
	sc := scanner{s: s}
	for {
		v, ok := sc.number()
		if !ok {
			return out
		}
		out = append(out, v)
	}
}

func urlRef(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "url(") {
		return ""
	}
	s = strings.Trim(strings.TrimSuffix(strings.TrimPrefix(s, "url("), ")"), `'" `)
	return strings.TrimPrefix(s, "#")
}

func parsePaint(s string) (paint, string) {
	s = strings.TrimSpace(s)
	switch {
	case s == "none" || s == "transparent":
		return paint{set: true, none: true}, ""
	case strings.HasPrefix(s, "url("):
		ref := urlRef(s)
		fallback := strings.TrimSpace(s[strings.Index(s, ")")+1:])
		p, _ := parsePaint(fallback)
		if fallback == "" {
			p = paint{}
		}
		return p, ref
	}
	c, ok := parseColor(s)
	if !ok {
		return paint{set: true, c: render.Color{A: 255}}, ""
	}
	return paint{set: true, c: c}, ""
}

func mix(a, b render.Color) render.Color {
	return render.Color{R: uint8((int(a.R) + int(b.R)) / 2), G: uint8((int(a.G) + int(b.G)) / 2), B: uint8((int(a.B) + int(b.B)) / 2),
		A: uint8((int(a.A) + int(b.A)) / 2)}
}
