package svg

import (
	"math"
	"strconv"
	"strings"

	"github.com/liamg/oriel/render"
)

// matrix is an affine transform: x' = a·x + c·y + e, y' = b·x + d·y + f.
type matrix struct{ a, b, c, d, e, f float32 }

var identity = matrix{a: 1, d: 1}

func translate(x, y float32) matrix { return matrix{a: 1, d: 1, e: x, f: y} }

// mul is m then n applied first: a child's transform inside its parent's.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: m.a*n.a + m.c*n.b, b: m.b*n.a + m.d*n.b,
		c: m.a*n.c + m.c*n.d, d: m.b*n.c + m.d*n.d,
		e: m.a*n.e + m.c*n.f + m.e, f: m.b*n.e + m.d*n.f + m.f,
	}
}

func (m matrix) apply(p render.Point) render.Point {
	return render.Point{X: m.a*p.X + m.c*p.Y + m.e, Y: m.b*p.X + m.d*p.Y + m.f}
}

// scale is how much m grows a length, on average over its axes.
func (m matrix) scale() float32 {
	return float32(math.Sqrt(math.Abs(float64(m.a*m.d - m.b*m.c))))
}

func (m matrix) axisAligned() bool { return m.b == 0 && m.c == 0 && m.a > 0 && m.d > 0 }

func (m matrix) rect(r render.Rect) render.Rect {
	return render.Rect{Min: m.apply(r.Min), Max: m.apply(r.Max)}
}

func (m matrix) path(p *render.Path) *render.Path {
	out := &render.Path{Segs: make([]render.Seg, len(p.Segs))}
	for i, s := range p.Segs {
		for j := range s.Pts {
			s.Pts[j] = m.apply(s.Pts[j])
		}
		out.Segs[i] = s
	}
	return out
}

// parseTransform reads a transform list: matrix, translate, scale, rotate, skewX and skewY.
func parseTransform(s string) matrix {
	m := identity
	for {
		open := strings.IndexByte(s, '(')
		end := strings.IndexByte(s, ')')
		if open < 0 || end < open {
			return m
		}
		name := strings.TrimSpace(strings.Trim(strings.TrimSpace(s[:open]), ","))
		v := numbers(s[open+1 : end])
		s = s[end+1:]
		arg := func(i int, def float32) float32 {
			if i < len(v) {
				return v[i]
			}
			return def
		}
		var t matrix
		switch name {
		case "matrix":
			if len(v) != 6 {
				continue
			}
			t = matrix{v[0], v[1], v[2], v[3], v[4], v[5]}
		case "translate":
			t = translate(arg(0, 0), arg(1, 0))
		case "scale":
			sx := arg(0, 1)
			t = matrix{a: sx, d: arg(1, sx)}
		case "rotate":
			rad := float64(arg(0, 0)) * math.Pi / 180
			sin, cos := float32(math.Sin(rad)), float32(math.Cos(rad))
			cx, cy := arg(1, 0), arg(2, 0)
			t = translate(cx, cy).mul(matrix{a: cos, b: sin, c: -sin, d: cos}).mul(translate(-cx, -cy))
		case "skewX":
			t = matrix{a: 1, c: float32(math.Tan(float64(arg(0, 0)) * math.Pi / 180)), d: 1}
		case "skewY":
			t = matrix{a: 1, b: float32(math.Tan(float64(arg(0, 0)) * math.Pi / 180)), d: 1}
		default:
			continue
		}
		m = m.mul(t)
	}
}

// scanner reads the numbers and flags of path data and lists.
type scanner struct {
	s string
	i int
}

func (sc *scanner) skip() {
	for sc.i < len(sc.s) && (sc.s[sc.i] == ' ' || sc.s[sc.i] == ',' || sc.s[sc.i] == '\t' || sc.s[sc.i] == '\n' || sc.s[sc.i] == '\r') {
		sc.i++
	}
}

func (sc *scanner) number() (float32, bool) {
	sc.skip()
	start := sc.i
	if sc.i < len(sc.s) && (sc.s[sc.i] == '+' || sc.s[sc.i] == '-') {
		sc.i++
	}
	digits, dot := false, false
	for sc.i < len(sc.s) {
		c := sc.s[sc.i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case c == '.' && !dot:
			dot = true
		default:
			goto exp
		}
		sc.i++
	}
exp:
	if digits && sc.i < len(sc.s) && (sc.s[sc.i] == 'e' || sc.s[sc.i] == 'E') {
		j := sc.i + 1
		if j < len(sc.s) && (sc.s[j] == '+' || sc.s[j] == '-') {
			j++
		}
		if j < len(sc.s) && sc.s[j] >= '0' && sc.s[j] <= '9' {
			sc.i = j
			for sc.i < len(sc.s) && sc.s[sc.i] >= '0' && sc.s[sc.i] <= '9' {
				sc.i++
			}
		}
	}
	if !digits {
		sc.i = start
		return 0, false
	}
	f, err := strconv.ParseFloat(sc.s[start:sc.i], 32)
	if err != nil {
		sc.i = start
		return 0, false
	}
	return float32(f), true
}

// flag is an arc's flag: one 0 or 1, which may run into what follows.
func (sc *scanner) flag() (bool, bool) {
	sc.skip()
	if sc.i < len(sc.s) && (sc.s[sc.i] == '0' || sc.s[sc.i] == '1') {
		sc.i++
		return sc.s[sc.i-1] == '1', true
	}
	return false, false
}

// parsePath reads path data, stopping at the first error as SVG does.
func parsePath(d string) *render.Path {
	p := new(render.Path)
	sc := scanner{s: d}
	var cur, start, ctrl render.Point
	var cmd, prev byte
	for {
		sc.skip()
		if sc.i >= len(sc.s) {
			return p
		}
		if c := sc.s[sc.i]; strings.IndexByte("MmLlHhVvCcSsQqTtAaZz", c) >= 0 {
			cmd = c
			sc.i++
		} else if cmd == 0 {
			return p
		}
		rel := cmd >= 'a'
		at := func(x, y float32) render.Point {
			if rel {
				return render.Point{X: cur.X + x, Y: cur.Y + y}
			}
			return render.Point{X: x, Y: y}
		}
		nums := func(n int) ([]float32, bool) {
			v := make([]float32, n)
			for i := range v {
				f, ok := sc.number()
				if !ok {
					return nil, false
				}
				v[i] = f
			}
			return v, true
		}
		up := cmd &^ 0x20
		if len(p.Segs) == 0 && up != 'M' {
			return p
		}
		switch up {
		case 'Z':
			p.Close()
			cur = start
			prev = 'Z'
			cmd = 0
			continue
		case 'M', 'L', 'T':
			v, ok := nums(2)
			if !ok {
				return p
			}
			to := at(v[0], v[1])
			switch up {
			case 'M':
				p.MoveTo(to)
				start = to
				if rel {
					cmd = 'l'
				} else {
					cmd = 'L'
				}
			case 'L':
				p.LineTo(to)
			case 'T':
				c := cur
				if prev == 'Q' || prev == 'T' {
					c = reflect(ctrl, cur)
				}
				p.QuadTo(c, to)
				ctrl = c
			}
			cur = to
		case 'H', 'V':
			v, ok := nums(1)
			if !ok {
				return p
			}
			to := cur
			switch {
			case up == 'H' && rel:
				to.X += v[0]
			case up == 'H':
				to.X = v[0]
			case rel:
				to.Y += v[0]
			default:
				to.Y = v[0]
			}
			p.LineTo(to)
			cur = to
		case 'C':
			v, ok := nums(6)
			if !ok {
				return p
			}
			c1, c2, to := at(v[0], v[1]), at(v[2], v[3]), at(v[4], v[5])
			p.CubeTo(c1, c2, to)
			ctrl, cur = c2, to
		case 'S':
			v, ok := nums(4)
			if !ok {
				return p
			}
			c1 := cur
			if prev == 'C' || prev == 'S' {
				c1 = reflect(ctrl, cur)
			}
			c2, to := at(v[0], v[1]), at(v[2], v[3])
			p.CubeTo(c1, c2, to)
			ctrl, cur = c2, to
		case 'Q':
			v, ok := nums(4)
			if !ok {
				return p
			}
			c, to := at(v[0], v[1]), at(v[2], v[3])
			p.QuadTo(c, to)
			ctrl, cur = c, to
		case 'A':
			v, ok := nums(3)
			if !ok {
				return p
			}
			large, ok1 := sc.flag()
			sweep, ok2 := sc.flag()
			xy, ok3 := nums(2)
			if !ok1 || !ok2 || !ok3 {
				return p
			}
			to := at(xy[0], xy[1])
			arc(p, cur, to, v[0], v[1], v[2], large, sweep)
			cur = to
		}
		prev = up
	}
}

func reflect(c, about render.Point) render.Point {
	return render.Point{X: 2*about.X - c.X, Y: 2*about.Y - c.Y}
}

// arc adds an elliptical arc from a to b as cubics (SVG 2, implementation notes B.2.4).
func arc(p *render.Path, a, b render.Point, rx, ry, rot float32, large, sweep bool) {
	if a == b {
		return
	}
	if rx == 0 || ry == 0 {
		p.LineTo(b)
		return
	}
	x1, y1, x2, y2 := float64(a.X), float64(a.Y), float64(b.X), float64(b.Y)
	r1, r2 := math.Abs(float64(rx)), math.Abs(float64(ry))
	phi := float64(rot) * math.Pi / 180
	sin, cos := math.Sin(phi), math.Cos(phi)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	xp, yp := cos*dx+sin*dy, -sin*dx+cos*dy
	if l := xp*xp/(r1*r1) + yp*yp/(r2*r2); l > 1 {
		r1, r2 = r1*math.Sqrt(l), r2*math.Sqrt(l)
	}
	num := r1*r1*r2*r2 - r1*r1*yp*yp - r2*r2*xp*xp
	den := r1*r1*yp*yp + r2*r2*xp*xp
	k := math.Sqrt(max(num/den, 0))
	if large == sweep {
		k = -k
	}
	cxp, cyp := k*r1*yp/r2, -k*r2*xp/r1
	cx, cy := cos*cxp-sin*cyp+(x1+x2)/2, sin*cxp+cos*cyp+(y1+y2)/2
	angle := func(ux, uy, vx, vy float64) float64 {
		return math.Atan2(ux*vy-uy*vx, ux*vx+uy*vy)
	}
	t1 := angle(1, 0, (xp-cxp)/r1, (yp-cyp)/r2)
	dt := angle((xp-cxp)/r1, (yp-cyp)/r2, (-xp-cxp)/r1, (-yp-cyp)/r2)
	if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	} else if sweep && dt < 0 {
		dt += 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dt) / (math.Pi / 2)))
	step := dt / float64(n)
	alpha := 4.0 / 3 * math.Tan(step/4)
	pt := func(t float64) (float64, float64) {
		return cx + r1*math.Cos(t)*cos - r2*math.Sin(t)*sin, cy + r1*math.Cos(t)*sin + r2*math.Sin(t)*cos
	}
	deriv := func(t float64) (float64, float64) {
		return -r1*math.Sin(t)*cos - r2*math.Cos(t)*sin, -r1*math.Sin(t)*sin + r2*math.Cos(t)*cos
	}
	t := t1
	for i := range n {
		sx, sy := pt(t)
		dsx, dsy := deriv(t)
		ex, ey := pt(t + step)
		dex, dey := deriv(t + step)
		end := render.Point{X: float32(ex), Y: float32(ey)}
		if i == n-1 {
			end = b
		}
		p.CubeTo(render.Point{X: float32(sx + alpha*dsx), Y: float32(sy + alpha*dsy)},
			render.Point{X: float32(ex - alpha*dex), Y: float32(ey - alpha*dey)}, end)
		t += step
	}
}
