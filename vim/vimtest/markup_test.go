package vimtest

import (
	"reflect"
	"testing"
)

func TestParseBuffer(t *testing.T) {
	tests := []struct {
		in   string
		want Buffer
	}{
		{"foo |bar", Buffer{Lines: []string{"foo bar"}, Cursor: Pos{0, 4}}},
		{"|", Buffer{Lines: []string{""}, Cursor: Pos{0, 0}}},
		{"a\n|b", Buffer{Lines: []string{"a", "b"}, Cursor: Pos{1, 0}}},
		{"xy|", Buffer{Lines: []string{"xy"}, Cursor: Pos{0, 2}}},
		{`a\|b|c`, Buffer{Lines: []string{"a|bc"}, Cursor: Pos{0, 3}}},
		{"é|x", Buffer{Lines: []string{"éx"}, Cursor: Pos{0, 2}}},
		{"[fo|o] bar", Buffer{Lines: []string{"foo bar"}, Cursor: Pos{0, 2},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 2}, Anchor: Pos{0, 0}}}},
		{"[foo] bar", Buffer{Lines: []string{"foo bar"}, Cursor: Pos{0, 2},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 2}, Anchor: Pos{0, 0}}}},
		{"[|foo] bar", Buffer{Lines: []string{"foo bar"}, Cursor: Pos{0, 0},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 2}, Anchor: Pos{0, 2}}}},
		{"[|on^e]", Buffer{Lines: []string{"one"}, Cursor: Pos{0, 0},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 2}, Anchor: Pos{0, 2}, AnchorSet: true}}},
		{"[ab\n]c", Buffer{Lines: []string{"ab", "c"}, Cursor: Pos{0, 2},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 2}, Anchor: Pos{0, 0}}}},
		{"[é]", Buffer{Lines: []string{"é"}, Cursor: Pos{0, 0},
			Visual: &Visual{Start: Pos{0, 0}, End: Pos{0, 0}, Anchor: Pos{0, 0}}}},
	}
	for _, tt := range tests {
		got, err := ParseBuffer(tt.in)
		if err != nil {
			t.Errorf("ParseBuffer(%q): %v", tt.in, err)
			continue
		}
		if !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ParseBuffer(%q) = %+v %+v, want %+v %+v", tt.in, got, got.Visual, tt.want, tt.want.Visual)
		}
	}
}

func TestParseBufferErrors(t *testing.T) {
	for _, in := range []string{"foo", "|a|b", "[ab", "ab]", "a]b[", "[]a|", "^a|", "[a][b]"} {
		if _, err := ParseBuffer(in); err == nil {
			t.Errorf("ParseBuffer(%q): want an error", in)
		}
	}
}

func TestBufferRoundTrip(t *testing.T) {
	for _, in := range []string{
		"foo |bar", "|", "a\n|b", "xy|", `a\|b|c`, `\[x\]|\^\\`, "é|x",
		"[fo|o] bar", "[|foo] bar", "[|on^e] two", "[ab|\n]c", "x[|é]y", "[^foo |b]ar",
	} {
		b, err := ParseBuffer(in)
		if err != nil {
			t.Errorf("ParseBuffer(%q): %v", in, err)
			continue
		}
		if got := b.String(); got != in {
			t.Errorf("round trip %q: got %q", in, got)
		}
	}
}
