package theme

import (
	"fmt"
	"math"
	"strings"
	"sync"
)

// Luminance is WCAG 2's relative luminance of an opaque sRGB colour.
func Luminance(c Color) float64 {
	ch := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// ContrastRatio is WCAG 2's contrast ratio of two opaque colours, 1 to 21.
func ContrastRatio(a, b Color) float64 {
	l1, l2 := Luminance(a), Luminance(b)
	if l2 > l1 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// Pair is a token drawn on another that has to stay readable: Min is the WCAG ratio it needs, 4.5 for text and 3
// for large text and non-text marks.
type Pair struct {
	FG, On string
	Min    float64
}

var (
	pairsMu sync.RWMutex
	pairs   []Pair
)

// Readable declares that fg is drawn on on and needs ratio min, for the theme lint. Whoever paints the pair declares
// it, as with tokens.
func Readable(fg, on string, min float64) {
	pairsMu.Lock()
	defer pairsMu.Unlock()
	for _, p := range pairs {
		if p.FG == fg && p.On == on {
			return
		}
	}
	pairs = append(pairs, Pair{fg, on, min})
}

// Pairs lists every declared pair.
func Pairs() []Pair {
	pairsMu.RLock()
	defer pairsMu.RUnlock()
	return append([]Pair(nil), pairs...)
}

func init() {
	for _, on := range []string{"bg.base", "bg.surface", "bg.raised"} {
		Readable("fg", on, 4.5)
		Readable("fg.muted", on, 4.5)
		Readable("fg.faint", on, 3)
	}
	Readable("accent.ink", "accent", 4.5)
	for _, s := range statusTokens {
		Readable(s, "bg.surface", 3)
	}
}

var statusTokens = []string{"status.needs_you", "status.permission", "status.working", "status.ci", "status.reviewer", "status.quiet"}

// apart is how far apart (deltaE OK) two status colours have to be to read as different at a glance: five JNDs.
const apart = 0.1

// Finding is something the lint found.
type Finding struct {
	Kind    string // "contrast", "status"
	Token   string // the token to change
	Message string
	Fix     string // an expression that fixes it, if there's one
}

// Lint checks r: declared pairs under their ratio, and status colours too close to tell apart.
func Lint(r *Resolved) []Finding {
	var out []Finding
	for _, p := range Pairs() {
		fg, ok1 := r.Color(p.FG)
		bg, ok2 := r.Color(p.On)
		if !ok1 || !ok2 {
			continue
		}
		c := ContrastRatio(fg, bg)
		if c >= p.Min {
			continue
		}
		f := Finding{Kind: "contrast", Token: p.FG,
			Message: fmt.Sprintf("%s on %s is %.2f:1, under %.3g:1", p.FG, p.On, c, p.Min)}
		if fix, d := r.fixContrast(p.FG); fix {
			verb := "lighten"
			if d < 0 {
				verb = "darken"
			}
			f.Fix = fmt.Sprintf("%s(%s, %d%%)", verb, r.exprOf(p.FG), int(math.Abs(d)*100+0.5))
		}
		out = append(out, f)
	}
	for i, a := range statusTokens {
		for _, b := range statusTokens[i+1:] {
			ca, _ := r.Color(a)
			cb, _ := r.Color(b)
			if d := deltaEOK(srgbToLab(rgb01(ca)), srgbToLab(rgb01(cb))); d < apart {
				out = append(out, Finding{Kind: "status", Token: b,
					Message: fmt.Sprintf("%s and %s are ΔE %.0f apart, hard to tell apart", min(a, b), max(a, b), d*100)})
			}
		}
	}
	return out
}

// fixContrast finds the smallest lightness step, in whole percent, that takes name over the ratio of every pair it's
// drawn in: lighter on a dark background, darker on a light one.
func (r *Resolved) fixContrast(name string) (bool, float64) {
	fg, _ := r.Color(name)
	bg, _ := r.Color("bg.base")
	dir := 0.01
	if Luminance(bg) > 0.18 {
		dir = -0.01
	}
	for i := 1; i <= 100; i++ {
		c, ok := Lighten(fg, dir*float64(i)), true
		for _, p := range Pairs() {
			if on, found := r.Color(p.On); p.FG == name && found && ContrastRatio(c, on) < p.Min {
				ok = false
			}
		}
		if ok {
			return true, dir * float64(i)
		}
	}
	return false, 0
}

// exprOf is how a fix refers to name's current value: its expression, unless that names something else, which a fix
// can't refer to without changing it too.
func (r *Resolved) exprOf(name string) string {
	if l, ok := r.links[name]; ok && !strings.ContainsAny(l.Expr, "()") {
		return l.Expr
	}
	c, _ := r.Color(name)
	return Hex(c)
}

func rgb01(c Color) [3]float64 {
	return [3]float64{float64(c.R) / 255, float64(c.G) / 255, float64(c.B) / 255}
}
