package widget

import "github.com/liamg/oriel/render/typeset"

// Elide is s cut to fit width when drawn in font, ending in "…"; s itself when it fits, "" when not even the
// ellipsis does.
func Elide(sh typeset.Shaper, s string, font typeset.FontSpec, width float32) string {
	if sh.Shape(s, font).Advance <= width {
		return s
	}
	r := []rune(s)
	lo, hi := 0, len(r) // the longest prefix that fits with the ellipsis is in [lo, hi)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if sh.Shape(string(r[:mid])+"…", font).Advance <= width {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 && sh.Shape("…", font).Advance > width {
		return ""
	}
	return string(r[:lo]) + "…"
}
