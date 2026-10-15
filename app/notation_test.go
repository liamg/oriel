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
		{render.KeyEvent{Key: " "}, " "},
		// ref:   intro.txt *<C-*
		// quote: "<C-...>		control-key"
		{render.KeyEvent{Key: "w", Mods: render.ModCtrl}, "<C-w>"},
		{render.KeyEvent{Key: " ", Mods: render.ModCtrl}, "<C-Space>"},
		{render.KeyEvent{Key: " ", Mods: render.ModShift}, " "},
		// Space's shift isn't in the character, so ⌃⇧Space (dictation) is a key of its own.
		{render.KeyEvent{Key: " ", Mods: render.ModCtrl | render.ModShift}, "<C-S-Space>"},
		// ref:   intro.txt *<M-*
		// quote: "<M-...>		alt-key or meta-key"
		{render.KeyEvent{Key: "a", Mods: render.ModAlt}, "<M-a>"},
		// ref:   intro.txt *<D-*
		// quote: "<D-...>		command-key (Mac) / super (GTK)"
		{render.KeyEvent{Key: "s", Mods: render.ModSuper}, "<D-s>"},
		{render.KeyEvent{Key: "Esc"}, "<Esc>"},
		{render.KeyEvent{Key: "CR", Mods: render.ModShift}, "<S-CR>"},
	} {
		if got := notation(tt.ev); got != tt.want {
			t.Errorf("notation(%+v) = %q, want %q", tt.ev, got, tt.want)
		}
	}
}

func TestFromNotationUndoesNotation(t *testing.T) {
	for _, ev := range []render.KeyEvent{
		{Key: "x"}, {Key: "X"}, {Key: " "}, {Key: "<"}, {Key: "CR"}, {Key: "Esc"}, {Key: "BS"},
		{Key: "w", Mods: render.ModCtrl}, {Key: " ", Mods: render.ModCtrl}, {Key: "s", Mods: render.ModSuper},
		{Key: "CR", Mods: render.ModShift}, {Key: "Left", Mods: render.ModShift}, {Key: " ", Mods: render.ModCtrl | render.ModShift},
	} {
		want := ev
		if len(ev.Key) == 1 && ev.Mods != render.ModCtrl|render.ModShift {
			want.Mods &^= render.ModShift
		}
		if got := fromNotation(notation(ev)); got != want {
			t.Errorf("fromNotation(notation(%+v)) = %+v", ev, got)
		}
	}
}
