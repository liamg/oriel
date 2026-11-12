package theme

import "testing"

// With is the theme with some tokens' colours changed: looked up by id or by name, they're the new colours; every
// other token, and the theme it came from, is as it was.
func TestWithChangesOnlyTheTokensGiven(t *testing.T) {
	r := resolve(t, Dark, oriel(t))
	red := Color{R: 255, A: 255}
	w := r.With(map[TokenID]Color{Line: red})
	if w.C(Line) != red {
		t.Errorf("line is %v, want %v", w.C(Line), red)
	}
	if c, _ := w.Color("line"); c != red {
		t.Errorf("line by name is %v, want %v", c, red)
	}
	if w.C(Fg) != r.C(Fg) {
		t.Errorf("fg moved: %v, was %v", w.C(Fg), r.C(Fg))
	}
	if r.C(Line) == red {
		t.Error("the original theme changed")
	}
}
