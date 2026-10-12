package app

import (
	"testing"

	"github.com/liamg/oriel/render"
)

func TestNotation(t *testing.T) {
	for _, tt := range []struct {
		ev   render.KeyEvent
		want string
	}{
		{render.KeyEvent{Key: "x"}, "x"},
		{render.KeyEvent{Key: "X", Mods: render.ModShift}, "X"},
		// ref:   intro.txt *<C-*
		// quote: "<C-...>		control-key"
		{render.KeyEvent{Key: "w", Mods: render.ModCtrl}, "<C-w>"},
		// ref:   intro.txt *<M-*
		// quote: "<M-...>		alt-key or meta-key"
		{render.KeyEvent{Key: "a", Mods: render.ModAlt}, "<M-a>"},
		// ref:   intro.txt *<D-*
		// quote: "<D-...>		command-key (Mac) / super (GTK)"
		{render.KeyEvent{Key: "s", Mods: render.ModSuper}, "<D-s>"},
	} {
		if got := notation(tt.ev); got != tt.want {
			t.Errorf("notation(%+v) = %q, want %q", tt.ev, got, tt.want)
		}
	}
}
