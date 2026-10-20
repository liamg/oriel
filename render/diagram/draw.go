package diagram

import (
	"math"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Palette is the colours a diagram draws in; the app fills it from theme tokens.
type Palette struct {
	NodeFill, NodeStroke, NodeText       render.Color
	Edge, EdgeText                       render.Color
	LabelFill, LabelStroke               render.Color // an edge label's pill
	GroupFill, GroupStroke, GroupText    render.Color // a subgraph's box
	NoteFill, NoteStroke, NoteText       render.Color
	Lifeline                             render.Color
	BlockStroke, BlockText, BlockTabFill render.Color // a sequence's loop / alt / opt boxes
	Activation                           render.Color
}

type role uint8

const (
	roleNone role = iota
	roleNodeFill
	roleNodeStroke
	roleNodeText
	roleEdge
	roleEdgeText
	roleLabelFill
	roleLabelStroke
	roleGroupFill
	roleGroupStroke
	roleGroupText
	roleNoteFill
	roleNoteStroke
	roleNoteText
	roleLifeline
	roleBlockStroke
	roleBlockText
	roleBlockTab
	roleActivation
)

func (p *Palette) of(r role) render.Color {
	switch r {
	case roleNodeFill:
		return p.NodeFill
	case roleNodeStroke:
		return p.NodeStroke
	case roleNodeText:
		return p.NodeText
	case roleEdge:
		return p.Edge
	case roleEdgeText:
		return p.EdgeText
	case roleLabelFill:
		return p.LabelFill
	case roleLabelStroke:
		return p.LabelStroke
	case roleGroupFill:
		return p.GroupFill
	case roleGroupStroke:
		return p.GroupStroke
	case roleGroupText:
		return p.GroupText
	case roleNoteFill:
		return p.NoteFill
	case roleNoteStroke:
		return p.NoteStroke
	case roleNoteText:
		return p.NoteText
	case roleLifeline:
		return p.Lifeline
	case roleBlockStroke:
		return p.BlockStroke
	case roleBlockText:
		return p.BlockText
	case roleBlockTab:
		return p.BlockTabFill
	case roleActivation:
		return p.Activation
	}
	return render.Color{}
}

type primKind uint8

const (
	primRect primKind = iota
	primPath
	primDashed // a polyline drawn in dashes
	primText
)

// prim is one thing a layout draws, in its own logical pixels.
type prim struct {
	kind         primKind
	rect         render.Rect
	radius       float32
	path         *render.Path
	pts          []render.Point
	dash         [2]float32
	fill, stroke role
	width        float32
	text         string
	font         typeset.FontSpec
	at           render.Point // a text's pen on its baseline; its centre when centre is set
	centre       bool
}

// Draw paints the diagram scaled to fit r's width, its height kept to its shape.
func (l *Layout) Draw(f render.Frame, r render.Rect, p Palette) {
	if l.Size.X <= 0 {
		return
	}
	k := r.Dx() / l.Size.X
	tr := func(q render.Point) render.Point { return render.Point{X: r.Min.X + q.X*k, Y: r.Min.Y + q.Y*k} }
	width := func(w float32) float32 { return max(w*k, .6) }
	for i := range l.prims {
		pr := &l.prims[i]
		switch pr.kind {
		case primRect:
			s := render.Style{Fill: p.of(pr.fill), Radius: pr.radius * k}
			if pr.stroke != roleNone {
				s.Stroke, s.Width = p.of(pr.stroke), width(pr.width)
			}
			f.Rect(render.Rect{Min: tr(pr.rect.Min), Max: tr(pr.rect.Max)}, s)
		case primPath:
			var path render.Path
			path.Segs = make([]render.Seg, len(pr.path.Segs))
			for j, sg := range pr.path.Segs {
				for n := range sg.Pts {
					sg.Pts[n] = tr(sg.Pts[n])
				}
				path.Segs[j] = sg
			}
			s := render.Style{Fill: p.of(pr.fill)}
			if pr.stroke != roleNone {
				s.Stroke, s.Width = p.of(pr.stroke), width(pr.width)
			}
			f.Path(&path, s)
		case primDashed:
			pts := make([]render.Point, len(pr.pts))
			for j, q := range pr.pts {
				pts[j] = tr(q)
			}
			f.Path(render.Dashed(pts, pr.dash[0]*k, pr.dash[1]*k), render.Style{Stroke: p.of(pr.stroke), Width: width(pr.width)})
		case primText:
			if pr.text == "" {
				continue
			}
			font := pr.font
			font.Size = max(float32(math.Round(float64(font.Size*k*4)))/4, 1)
			run := l.sh.Shape(pr.text, font)
			at := tr(pr.at)
			if pr.centre {
				at.X -= run.Advance / 2
			}
			f.Glyphs(run, at, p.of(pr.fill))
		}
	}
}

// Path helpers, in layout pixels.

func pt(x, y float32) render.Point { return render.Point{X: x, Y: y} }

func add(a, b render.Point) render.Point { return render.Point{X: a.X + b.X, Y: a.Y + b.Y} }
func sub(a, b render.Point) render.Point { return render.Point{X: a.X - b.X, Y: a.Y - b.Y} }
func mul(a render.Point, k float32) render.Point {
	return render.Point{X: a.X * k, Y: a.Y * k}
}
func length(a render.Point) float32 { return float32(math.Hypot(float64(a.X), float64(a.Y))) }
func unit(a render.Point) render.Point {
	if l := length(a); l > 0 {
		return mul(a, 1/l)
	}
	return render.Point{Y: 1}
}

// roundedPolygon is a closed polygon with its corners rounded by about r.
func roundedPolygon(pts []render.Point, r float32) *render.Path {
	p := new(render.Path)
	n := len(pts)
	for i := range n {
		prev, at, next := pts[(i+n-1)%n], pts[i], pts[(i+1)%n]
		a := add(at, mul(unit(sub(prev, at)), min(r, length(sub(prev, at))/2)))
		b := add(at, mul(unit(sub(next, at)), min(r, length(sub(next, at))/2)))
		if i == 0 {
			p.MoveTo(a)
		} else {
			p.LineTo(a)
		}
		p.QuadTo(at, b)
	}
	return p.Close()
}

// quarter adds a quarter of the ellipse at c with radii (rx, ry), from angle a0 to a1 (multiples of π/2).
func quarter(p *render.Path, c render.Point, rx, ry float32, a0, a1 float64) {
	const k = 0.5523
	s0, c0 := math.Sincos(a0)
	s1, c1 := math.Sincos(a1)
	dir := float32(1)
	if a1 < a0 {
		dir = -1
	}
	from := pt(c.X+rx*float32(c0), c.Y+ry*float32(s0))
	to := pt(c.X+rx*float32(c1), c.Y+ry*float32(s1))
	// tangents at each end
	t0 := pt(-rx*float32(s0)*dir, ry*float32(c0)*dir)
	t1 := pt(-rx*float32(s1)*dir, ry*float32(c1)*dir)
	p.CubeTo(add(from, mul(t0, k)), sub(to, mul(t1, k)), to)
}

// curve is the smooth path through pts: it leaves the first and enters the last mostly along the rank axis, and flows
// through the points between (a Catmull-Rom spline as cubics).
func curve(pts []render.Point, vertical bool) *render.Path {
	n := len(pts)
	tan := make([]render.Point, n)
	// an end leaves mostly along the rank axis, leaning towards where it goes
	axis := func(d render.Point) render.Point {
		if vertical {
			return pt(d.X*.6, d.Y*1.5)
		}
		return pt(d.X*1.5, d.Y*.6)
	}
	tan[0], tan[n-1] = axis(sub(pts[1], pts[0])), axis(sub(pts[n-1], pts[n-2]))
	for i := 1; i < n-1; i++ {
		tan[i] = mul(sub(pts[i+1], pts[i-1]), .5)
	}
	p := new(render.Path).MoveTo(pts[0])
	for i := 1; i < n; i++ {
		p.CubeTo(add(pts[i-1], mul(tan[i-1], 1./3)), sub(pts[i], mul(tan[i], 1./3)), pts[i])
	}
	return p
}

// flatten is a path's curves as a polyline, for dashing.
func flatten(p *render.Path) []render.Point {
	var out []render.Point
	var at render.Point
	for _, s := range p.Segs {
		switch s.Op {
		case render.MoveTo:
			at = s.Pts[0]
			out = append(out, at)
		case render.LineTo:
			at = s.Pts[0]
			out = append(out, at)
		case render.QuadTo:
			out = append(out, s.Pts[1])
			at = s.Pts[1]
		case render.CubeTo:
			c1, c2, to := s.Pts[0], s.Pts[1], s.Pts[2]
			n := max(4, int(length(sub(to, at))/3))
			for i := 1; i <= n; i++ {
				t := float32(i) / float32(n)
				u := 1 - t
				out = append(out, pt(u*u*u*at.X+3*u*u*t*c1.X+3*u*t*t*c2.X+t*t*t*to.X, u*u*u*at.Y+3*u*u*t*c1.Y+3*u*t*t*c2.Y+t*t*t*to.Y))
			}
			at = to
		}
	}
	return out
}

// endTangent is the direction a path arrives at its last point in.
func endTangent(p *render.Path) render.Point {
	s := p.Segs[len(p.Segs)-1]
	switch s.Op {
	case render.CubeTo:
		if d := sub(s.Pts[2], s.Pts[1]); length(d) > .01 {
			return unit(d)
		}
		if d := sub(s.Pts[2], s.Pts[0]); length(d) > .01 {
			return unit(d)
		}
	case render.LineTo:
		if len(p.Segs) > 1 {
			prev := p.Segs[len(p.Segs)-2]
			last := prev.Pts[0]
			switch prev.Op {
			case render.CubeTo:
				last = prev.Pts[2]
			case render.QuadTo:
				last = prev.Pts[1]
			}
			return unit(sub(s.Pts[0], last))
		}
	}
	return render.Point{Y: 1}
}

// trimEnd moves a path's last point back by d along its arrival direction.
func trimEnd(p *render.Path, d float32) {
	t := endTangent(p)
	s := &p.Segs[len(p.Segs)-1]
	switch s.Op {
	case render.CubeTo:
		s.Pts[2] = sub(s.Pts[2], mul(t, d))
		s.Pts[1] = sub(s.Pts[1], mul(t, d))
	case render.LineTo:
		s.Pts[0] = sub(s.Pts[0], mul(t, d))
	}
}

// reversed is the path run backwards (lines and cubics only).
func reversed(p *render.Path) *render.Path {
	type seg struct {
		op   render.SegOp
		from render.Point
		pts  [3]render.Point
	}
	var segs []seg
	var at render.Point
	for _, s := range p.Segs {
		switch s.Op {
		case render.MoveTo:
			at = s.Pts[0]
		case render.LineTo:
			segs = append(segs, seg{render.LineTo, at, s.Pts})
			at = s.Pts[0]
		case render.CubeTo:
			segs = append(segs, seg{render.CubeTo, at, s.Pts})
			at = s.Pts[2]
		}
	}
	out := new(render.Path).MoveTo(at)
	for i := len(segs) - 1; i >= 0; i-- {
		s := segs[i]
		if s.op == render.LineTo {
			out.LineTo(s.from)
		} else {
			out.CubeTo(s.pts[1], s.pts[0], s.from)
		}
	}
	return out
}

const arrowLen, arrowHalf = 8, 4

// marker adds an end's marker at tip, arriving in direction d, and says how far back the line should stop.
func markerPrims(m Marker, tip, d render.Point, w float32) ([]prim, float32) {
	n := pt(-d.Y, d.X)
	switch m {
	case ArrowHead:
		base := sub(tip, mul(d, arrowLen))
		notch := add(base, mul(d, 2))
		var p render.Path
		p.MoveTo(tip).LineTo(add(base, mul(n, arrowHalf))).LineTo(notch).LineTo(sub(base, mul(n, arrowHalf))).Close()
		return []prim{{kind: primPath, path: &p, fill: roleEdge, stroke: roleEdge, width: .6}}, arrowLen - 2.5
	case CrossMark:
		c := sub(tip, mul(d, 5))
		var p render.Path
		a, b := add(mul(d, 3.5), mul(n, 3.5)), sub(mul(d, 3.5), mul(n, 3.5))
		p.MoveTo(sub(c, a)).LineTo(add(c, a)).MoveTo(sub(c, b)).LineTo(add(c, b))
		return []prim{{kind: primPath, path: &p, stroke: roleEdge, width: max(w, 1.4)}}, 0
	case CircleMark:
		c := sub(tip, mul(d, 4))
		return []prim{{kind: primPath, path: render.Ellipse(render.XYWH(c.X-3.5, c.Y-3.5, 7, 7)), fill: roleNodeFill, stroke: roleEdge, width: max(w, 1.2)}}, 7.5
	}
	return nil, 0
}

// line adds an edge's line along path with its markers, solid, dotted or thick.
func linePrims(path *render.Path, style LineStyle, start, end Marker) []prim {
	w := float32(1.3)
	if style == Thick {
		w = 2.6
	}
	var out []prim
	n := len(path.Segs)
	if n < 2 {
		return nil
	}
	endPt := path.Segs[n-1].Pts[0]
	switch path.Segs[n-1].Op {
	case render.CubeTo:
		endPt = path.Segs[n-1].Pts[2]
	}
	mk, back := markerPrims(end, endPt, endTangent(path), w)
	if back > 0 {
		trimEnd(path, back)
	}
	if start != NoMarker {
		rp := reversed(path)
		smk, sback := markerPrims(start, path.Segs[0].Pts[0], endTangent(rp), w)
		if sback > 0 {
			trimEnd(rp, sback)
			path = reversed(rp)
		}
		mk = append(mk, smk...)
	}
	if style == Dotted {
		out = append(out, prim{kind: primDashed, pts: flatten(path), dash: [2]float32{3, 3.5}, stroke: roleEdge, width: w})
	} else {
		out = append(out, prim{kind: primPath, path: path, stroke: roleEdge, width: w})
	}
	return append(out, mk...)
}

// shapePrims draws a node's outline in r.
func shapePrims(s Shape, r render.Rect) []prim {
	x, y, w, h := r.Min.X, r.Min.Y, r.Dx(), r.Dy()
	box := func(rad float32) prim {
		return prim{kind: primRect, rect: r, radius: rad, fill: roleNodeFill, stroke: roleNodeStroke, width: 1}
	}
	poly := func(pts ...render.Point) []prim {
		return []prim{{kind: primPath, path: roundedPolygon(pts, 4), fill: roleNodeFill, stroke: roleNodeStroke, width: 1}}
	}
	switch s {
	case Round:
		return []prim{box(min(h/2, 12))}
	case Stadium:
		return []prim{box(h / 2)}
	case Subroutine:
		var p render.Path
		p.MoveTo(pt(x+7, y)).LineTo(pt(x+7, y+h)).MoveTo(pt(x+w-7, y)).LineTo(pt(x+w-7, y+h))
		return []prim{box(4), {kind: primPath, path: &p, stroke: roleNodeStroke, width: 1}}
	case Cylinder:
		ry := float32(6)
		rx := w / 2
		top, bot := pt(x+rx, y+ry), pt(x+rx, y+h-ry)
		var body render.Path
		body.MoveTo(pt(x, y+ry))
		quarter(&body, top, rx, ry, math.Pi, 1.5*math.Pi)
		quarter(&body, top, rx, ry, 1.5*math.Pi, 2*math.Pi)
		body.LineTo(pt(x+w, y+h-ry))
		quarter(&body, bot, rx, ry, 0, math.Pi/2)
		quarter(&body, bot, rx, ry, math.Pi/2, math.Pi)
		body.Close()
		var rim render.Path
		rim.MoveTo(pt(x, y+ry))
		quarter(&rim, top, rx, ry, math.Pi, math.Pi/2)
		quarter(&rim, top, rx, ry, math.Pi/2, 0)
		return []prim{{kind: primPath, path: &body, fill: roleNodeFill, stroke: roleNodeStroke, width: 1}, {kind: primPath, path: &rim, stroke: roleNodeStroke, width: 1}}
	case Circle:
		return []prim{{kind: primPath, path: render.Ellipse(r), fill: roleNodeFill, stroke: roleNodeStroke, width: 1}}
	case DoubleCircle:
		return []prim{{kind: primPath, path: render.Ellipse(r), stroke: roleNodeStroke, width: 1},
			{kind: primPath, path: render.Ellipse(inset(r, 4, 4)), fill: roleNodeFill, stroke: roleNodeStroke, width: 1}}
	case Diamond:
		return poly(pt(x+w/2, y), pt(x+w, y+h/2), pt(x+w/2, y+h), pt(x, y+h/2))
	case Hexagon:
		i := h * .25
		return poly(pt(x+i, y), pt(x+w-i, y), pt(x+w, y+h/2), pt(x+w-i, y+h), pt(x+i, y+h), pt(x, y+h/2))
	case Lean:
		k := h * .35
		return poly(pt(x+k, y), pt(x+w, y), pt(x+w-k, y+h), pt(x, y+h))
	case LeanLeft:
		k := h * .35
		return poly(pt(x, y), pt(x+w-k, y), pt(x+w, y+h), pt(x+k, y+h))
	case Trapezoid:
		k := h * .35
		return poly(pt(x+k, y), pt(x+w-k, y), pt(x+w, y+h), pt(x, y+h))
	case TrapezoidAlt:
		k := h * .35
		return poly(pt(x, y), pt(x+w, y), pt(x+w-k, y+h), pt(x+k, y+h))
	case Flag:
		k := h * .35
		return poly(pt(x, y), pt(x+w, y), pt(x+w, y+h), pt(x, y+h), pt(x+k, y+h/2))
	}
	return []prim{box(5)}
}

// labelCentre is where a node's label sits in its outline.
func labelCentre(s Shape, r render.Rect) render.Point {
	c := pt((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2)
	switch s {
	case Cylinder:
		c.Y += 3
	case Flag:
		c.X += r.Dy() * .35 / 2
	}
	return c
}

// pillPrims draws a label's pill and its text.
func pillPrims(r render.Rect, b block) []prim {
	out := []prim{{kind: primRect, rect: r, radius: min(r.Dy()/2, 8), fill: roleLabelFill, stroke: roleLabelStroke, width: 1}}
	return append(out, b.at((r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2, roleEdgeText)...)
}

// prims draws a placed flowchart: subgraphs, then edges, then nodes, then edge labels on top.
func (g *flowLayout) prims(l *Layout) []prim {
	var out []prim
	depth := func(gi int) int { return len(g.chains[gi]) }
	for d := 1; d <= len(g.f.Groups); d++ {
		for gi, gr := range l.Groups {
			if depth(gi) != d || gr.Rect.Dx() <= 0 {
				continue
			}
			out = append(out, prim{kind: primRect, rect: gr.Rect, radius: 8, fill: roleGroupFill, stroke: roleGroupStroke, width: 1})
			title := g.titles[gi]
			if room := gr.Rect.Dx() - 20; title.w > room && len(title.lines) == 1 {
				title.lines, title.full = []string{typeset.Elide(g.m.sh, title.lines[0], title.font, room)}, g.f.Groups[gi].Title
			}
			out = append(out, title.left(gr.Rect.Min.X+10, gr.Rect.Min.Y+6, roleGroupText)...)
			l.cut(title, render.XYWH(gr.Rect.Min.X+10, gr.Rect.Min.Y+6, min(title.w, gr.Rect.Dx()-20), title.h))
		}
	}
	var labels []prim
	for _, e := range l.Edges {
		if e.Line == Invisible {
			continue
		}
		var path *render.Path
		if e.Loop {
			a, z := e.Points[0], e.Points[1]
			path = new(render.Path).MoveTo(a)
			if e.Vertical {
				path.CubeTo(pt(a.X+loopReach, a.Y-14), pt(z.X+loopReach, z.Y+14), z)
			} else {
				path.CubeTo(pt(a.X-14, a.Y+loopReach), pt(z.X+14, z.Y+loopReach), z)
			}
		} else {
			path = curve(e.Points, e.Vertical)
		}
		out = append(out, linePrims(path, e.Line, e.StartMark, e.EndMark)...)
		if e.Label.Dx() > 0 {
			labels = append(labels, pillPrims(e.Label, g.elabels[e.Edge])...)
			l.cut(g.elabels[e.Edge], e.Label)
		}
	}
	for i, n := range l.Nodes {
		out = append(out, shapePrims(n.Shape, n.Rect)...)
		c := labelCentre(n.Shape, n.Rect)
		out = append(out, g.labels[i].at(c.X, c.Y, roleNodeText)...)
		l.cut(g.labels[i], n.Rect)
	}
	return append(out, labels...)
}
