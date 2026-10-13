package app

import "testing"

func TestKeyLabel(t *testing.T) {
	for _, tt := range []struct{ keys, want string }{
		{"<D-E>", "⇧⌘E"},
		{"<D-k>", "⌘K"},
		{"<D-,>", "⌘,"},
		{"<C-w>c", "⌃W c"},
		{"<Space><Space>", "␣ ␣"},
		{"<S-CR>", "⇧↵"},
		{"gx", "g x"},
	} {
		if got := keyLabel(tt.keys); got != tt.want {
			t.Errorf("keyLabel(%q) = %q, want %q", tt.keys, got, tt.want)
		}
	}
}
