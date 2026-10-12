package render

// Path is an outline in logical pixels.
type Path struct {
	Segs []Seg
}

type SegOp uint8

const (
	MoveTo SegOp = iota
	LineTo
	QuadTo
	CubeTo
	Close
)

// Seg is one segment: Pts holds its control points then its end point.
type Seg struct {
	Op  SegOp
	Pts [3]Point
}

func (p *Path) MoveTo(to Point) *Path {
	p.Segs = append(p.Segs, Seg{Op: MoveTo, Pts: [3]Point{to}})
	return p
}
func (p *Path) LineTo(to Point) *Path {
	p.Segs = append(p.Segs, Seg{Op: LineTo, Pts: [3]Point{to}})
	return p
}
func (p *Path) QuadTo(c, to Point) *Path {
	p.Segs = append(p.Segs, Seg{Op: QuadTo, Pts: [3]Point{c, to}})
	return p
}
func (p *Path) CubeTo(c1, c2, to Point) *Path {
	p.Segs = append(p.Segs, Seg{Op: CubeTo, Pts: [3]Point{c1, c2, to}})
	return p
}
func (p *Path) Close() *Path { p.Segs = append(p.Segs, Seg{Op: Close}); return p }

// Ellipse is the ellipse inscribed in r, as four cubics.
func Ellipse(r Rect) *Path {
	const k = 0.5523
	cx, cy := (r.Min.X+r.Max.X)/2, (r.Min.Y+r.Max.Y)/2
	rx, ry := r.Dx()/2, r.Dy()/2
	p := new(Path).MoveTo(Point{cx + rx, cy})
	p.CubeTo(Point{cx + rx, cy + ry*k}, Point{cx + rx*k, cy + ry}, Point{cx, cy + ry})
	p.CubeTo(Point{cx - rx*k, cy + ry}, Point{cx - rx, cy + ry*k}, Point{cx - rx, cy})
	p.CubeTo(Point{cx - rx, cy - ry*k}, Point{cx - rx*k, cy - ry}, Point{cx, cy - ry})
	p.CubeTo(Point{cx + rx*k, cy - ry}, Point{cx + rx, cy - ry*k}, Point{cx + rx, cy})
	return p.Close()
}
