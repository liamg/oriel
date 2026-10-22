package typeset

import "sort"

// minEm is the narrowest a glyph is taken to be, in ems, to guess how much of a long text can fit a width.
const minEm = 0.25

// Elide is s cut with "…" to fit width in font, or s when it fits. Only as much of s as can reach width is
// shaped, so a long text costs what fits of it.
func Elide(sh Shaper, s string, font FontSpec, width float32) string {
	r := []rune(s)
	n := len(r)
	if font.Size > 0 && width > 0 {
		n = min(n, max(16, int(width/(font.Size*minEm))+2))
	}
	run := sh.Shape(string(r[:n]), font)
	for run.Advance <= width && n < len(r) {
		n = min(len(r), n*2)
		run = sh.Shape(string(r[:n]), font)
	}
	if run.Advance <= width {
		return s
	}
	ell := sh.Shape("…", font).Advance
	xs := PenXs(run, n)
	fits := func(k int) bool { return sh.Shape(string(r[:k])+"…", font).Advance <= width }
	k := sort.Search(n+1, func(k int) bool { return xs[k]+ell > width }) - 1
	for k > 0 && !fits(k) {
		k--
	}
	for k+1 < n && fits(k+1) {
		k++
	}
	if k <= 0 {
		if ell > width {
			return ""
		}
		return "…"
	}
	return string(r[:k]) + "…"
}

// PenXs is the pen position before each rune of a run of n runes, and after the last; a rune inside a cluster
// takes its cluster's start.
func PenXs(run *Run, n int) []float32 {
	xs := make([]float32, n+1)
	set := make([]bool, n+1)
	for _, g := range run.Glyphs {
		if g.Rune >= 0 && g.Rune < n && !set[g.Rune] {
			xs[g.Rune], set[g.Rune] = g.X, true
		}
	}
	xs[n], set[n] = run.Advance, true
	for i := 1; i < n; i++ {
		if !set[i] || xs[i] < xs[i-1] {
			xs[i] = xs[i-1]
		}
	}
	return xs
}
