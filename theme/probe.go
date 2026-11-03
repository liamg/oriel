package theme

import (
	"maps"
	"slices"
)

// Probe is r with every token and syntax group in an opaque colour of its own and the effects off. A frame painted
// with it tells what painted each pixel: Probed reads a colour back to its name.
func Probe(r *Resolved) *Resolved {
	p := &Resolved{Name: r.Name, Variant: r.Variant, byName: map[string]Color{}, palette: r.palette,
		syntax: map[string]Syntax{}, effects: map[string]float64{}, links: r.links, probes: map[Color]string{}}
	n := 0
	code := func(name string) Color {
		// A hash, not a linear code, so a blend of two probes (an antialiased edge) is rarely a third.
		for {
			n++
			v := uint32(n)
			v ^= v >> 16
			v *= 0x7feb352d
			v ^= v >> 15
			v *= 0x846ca68b
			v ^= v >> 16
			c := Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 255}
			if _, taken := p.probes[c]; !taken {
				p.probes[c] = name
				return c
			}
		}
	}
	for _, t := range Tokens() {
		c := code(t.Name)
		p.colors = append(p.colors, c)
		p.byName[t.Name] = c
	}
	for _, k := range slices.Sorted(maps.Keys(r.syntax)) {
		s := r.syntax[k]
		s.FG = code(k)
		if s.BG.A > 0 {
			s.BG = code(k + ".bg")
		}
		p.syntax[k] = s
	}
	for k := range r.effects {
		p.effects[k] = 0
	}
	return p
}

// Probed is the token or syntax group a probe theme painted c with.
func (r *Resolved) Probed(c Color) (string, bool) {
	name, ok := r.probes[c]
	return name, ok
}
