package kit

import (
	"testing"
)

func TestKeyLabel(t *testing.T) {
	for _, tt := range []struct{ keys, want string }{
		{"<D-E>", "⇧⌘E"},
		{"<D-k>", "⌘K"},
		{"<D-,>", "⌘,"},
		{"<C-w>c", "⌃W c"},
		{"<Space><Space>", "␣ ␣"},
		{"<S-CR>", "⇧↵"},
		{"gx", "g x"},
		{"<X1Mouse>", "mouse back"},
		{"<S-X2Mouse>", "⇧mouse forward"},
	} {
		if got := KeyLabel(tt.keys); got != tt.want {
			t.Errorf("KeyLabel(%q) = %q, want %q", tt.keys, got, tt.want)
		}
	}
}

// Every key vim's notation names reads as a keyboard shows it, a glyph where there's one, its modifiers as ⌃⌥⇧⌘ in
// the order macOS writes them; an alias reads as the key it stands for.
//
// ref:   nvim/intro.txt *<A-*
// quote: "same as <M-…>"
func TestKeyLabelReadsVimsNotation(t *testing.T) {
	for _, tt := range []struct{ keys, want string }{
		{"<A-x>", "⌥X"},
		{"<M-C-t>", "⌃⌥T"},
		{"<Return>", "↵"},
		{"<Enter>", "↵"},
		{"<cr>", "↵"},
		{"<c-W>", "⌃W"},
		{"<Del>", "⌦"},
		{"<Home>", "↖"},
		{"<End>", "↘"},
		{"<PageUp>", "⇞"},
		{"<PageDown>", "⇟"},
		{"<S-Tab>", "⇧⇥"},
		{"<Bar>", "|"},
		{"<Bslash>", `\`},
		{"<lt>", "<"},
		{"<F5>", "F5"},
		{"<D-S-F5>", "⇧⌘F5"},
		{"<Leader>f", "leader f"},
		{"<Esc>", "esc"},
		{"<C-w>J", "⌃W J"},
		{"<C-`>", "⌃`"},
	} {
		if got := KeyLabel(tt.keys); got != tt.want {
			t.Errorf("KeyLabel(%q) = %q, want %q", tt.keys, got, tt.want)
		}
	}
}
