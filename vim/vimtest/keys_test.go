package vimtest

import (
	"reflect"
	"testing"
)

// Notation from :help key-notation (spec/vim/intro.txt).
func TestParseKeys(t *testing.T) {
	tests := []struct {
		in   string
		want []Key
	}{
		{"dw", []Key{{Rune: 'd'}, {Rune: 'w'}}},
		{"<Esc>", []Key{{Name: "Esc"}}},
		{"<esc><CR><Enter><Return>", []Key{{Name: "Esc"}, {Name: "CR"}, {Name: "CR"}, {Name: "CR"}}},
		{"<C-x>", []Key{{Rune: 'x', Mods: Ctrl}}},
		{"<c-W>", []Key{{Rune: 'W', Mods: Ctrl}}},
		{"<S-Up>", []Key{{Name: "Up", Mods: Shift}}},
		{"<M-a><A-a>", []Key{{Rune: 'a', Mods: Alt}, {Rune: 'a', Mods: Alt}}},
		{"<C-S-F1>", []Key{{Name: "F1", Mods: Ctrl | Shift}}},
		{"<D-s>", []Key{{Rune: 's', Mods: Cmd}}},
		{"<lt><Bar><Bslash><Space>", []Key{{Rune: '<'}, {Rune: '|'}, {Rune: '\\'}, {Rune: ' '}}},
		{"<", []Key{{Rune: '<'}}},
		{"<a>", []Key{{Rune: '<'}, {Rune: 'a'}, {Rune: '>'}}},
		{"<Foo>", []Key{{Rune: '<'}, {Rune: 'F'}, {Rune: 'o'}, {Rune: 'o'}, {Rune: '>'}}},
		{"<>", []Key{{Rune: '<'}, {Rune: '>'}}},
		{"é", []Key{{Rune: 'é'}}},
	}
	for _, tt := range tests {
		if got := ParseKeys(tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseKeys(%q) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}

func TestNotation(t *testing.T) {
	tests := []struct{ in, want string }{
		{"cwxy<Esc>", "cwxy<Esc>"},
		{"<c-x><esc>", "<C-x><Esc>"},
		{"<lt>a|\\", "<lt>a<Bar><Bslash>"},
		{"<S-C-Up>", "<S-C-Up>"},
		{"<Space>", " "},
	}
	for _, tt := range tests {
		if got := Notation(ParseKeys(tt.in)); got != tt.want {
			t.Errorf("Notation(ParseKeys(%q)) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
