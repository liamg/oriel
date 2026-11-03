package theme

import (
	"slices"
	"testing"
)

// variantsOf are the variants a built-in resolves at: both for one with [dark]/[light] tables, else its own.
func variantsOf(st []*Spec) []Variant {
	return slices.Compact([]Variant{Effective(st, Dark), Effective(st, Light)})
}

// Every built-in theme passes its own lint at each variant it has: every declared pair at its contrast (WCAG 2, as
// contrast.go's tests cite), and no two status colours too close to tell apart.
func TestBuiltinPassesLint(t *testing.T) {
	for _, src := range Builtins() {
		st, err := Stack(Builtins(), src.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range variantsOf(st) {
			r, err := Resolve(st, v)
			if err != nil {
				t.Fatal(err)
			}
			for _, f := range Lint(r) {
				t.Errorf("%s %s: %s (fix: %s)", src.Name(), v, f.Message, f.Fix)
			}
		}
	}
}

// Every built-in sets every palette colour oriel does, so none falls through to oriel's.
func TestBuiltinsSetTheWholePalette(t *testing.T) {
	base := oriel(t)
	for _, src := range Builtins() {
		s, err := src.Load()
		if err != nil {
			t.Fatal(err)
		}
		if s.Name == "oriel" {
			continue
		}
		for _, v := range []Variant{Dark, Light} {
			for k := range base.variant(v).Palette {
				_, shared := s.Palette[k]
				_, own := s.variant(v).Palette[k]
				if !shared && !own {
					t.Errorf("%s doesn't set %s (%s)", s.Name, k, v)
				}
			}
		}
	}
}
