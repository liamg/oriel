package scene

import "testing"

func TestLexGo(t *testing.T) {
	toks := LexGo(`	if x := f(1); x != "a" { return } // done`)
	var got []string
	for _, tk := range toks {
		got = append(got, tk.Text)
	}
	want := []string{"\t", "if", " ", "x", " ", ":=", " ", "f", "(", "1", "); ", "x", " ", "!=", " ", `"a"`, " { ", "return", " } ", "// done"}
	if len(got) != len(want) {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("token %d: got %q, want %q (all %q)", i, got[i], want[i], got)
		}
	}
	if toks[1].Color != synK || toks[7].Color != synF || toks[9].Color != synN || toks[15].Color != synS || !toks[19].Italic {
		t.Errorf("colours wrong: %+v", toks)
	}
}
