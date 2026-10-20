package ui

import "github.com/liamg/oriel/render/typeset"

// Elide is s cut to fit width when drawn in font, ending in "…"; s itself when it fits, "" when not even the
// ellipsis does.
func Elide(sh typeset.Shaper, s string, font typeset.FontSpec, width float32) string {
	return typeset.Elide(sh, s, font, width)
}
