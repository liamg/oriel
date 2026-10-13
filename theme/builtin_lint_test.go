package theme

import "testing"

// The built-in theme passes its own lint in both variants: every declared pair at its contrast (WCAG 2, as
// contrast.go's tests cite), and no two status colours too close to tell apart.
func TestBuiltinPassesLint(t *testing.T) {
	for _, v := range []Variant{Dark, Light} {
		st, err := Stack(Builtins(), "oriel")
		if err != nil {
			t.Fatal(err)
		}
		r, err := Resolve(st, v)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range Lint(r) {
			t.Errorf("%s: %s (fix: %s)", v, f.Message, f.Fix)
		}
	}
}
