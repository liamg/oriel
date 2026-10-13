package syntax

import (
	"os"
	"testing"
)

func parse(t *testing.T, lang, file string) (*Parser, []byte) {
	t.Helper()
	l, ok := Builtin().Language(lang)
	if !ok {
		t.Fatalf("no %s", lang)
	}
	src, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewParser(l)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	p.Parse(src)
	return p, src
}

func TestGoUnitsAndOutline(t *testing.T) {
	p, src := parse(t, "go", "testdata/highlight/fixture.go")
	outline, err := p.Outline()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, u := range outline {
		got = append(got, u.Kind+" "+u.Name)
	}
	want := []string{"type Shape", "type Circle", "method Area", "function Total"}
	if len(got) != len(want) {
		t.Fatalf("outline = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("outline = %q, want %q", got, want)
		}
	}
	units, _ := p.Units()
	for i, u := range units {
		if u.Kind == "if" {
			parent := units[u.Parent]
			if parent.Kind != "block" || units[parent.Parent].Kind != "loop" {
				t.Errorf("if %q nests in %s, want a loop's block", src[u.Range.Start:u.Range.End], parent.Kind)
			}
		}
		if u.Parent >= i {
			t.Errorf("unit %d's parent %d isn't before it", i, u.Parent)
		}
	}
}

func TestFolds(t *testing.T) {
	for _, c := range []struct {
		lang, file string
		want       Fold // one fold that must be there
	}{
		{"go", "testdata/highlight/fixture.go", Fold{24, 39}},    // func Total
		{"python", "testdata/highlight/fixture.py", Fold{8, 12}}, // class Circle
		{"lua", "testdata/highlight/fixture.lua", Fold{11, 29}},  // function M:total
	} {
		p, _ := parse(t, c.lang, c.file)
		folds, err := p.Folds()
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, f := range folds {
			found = found || f == c.want
		}
		if !found {
			t.Errorf("%s: folds %v, want one of them to be %v", c.lang, folds, c.want)
		}
	}
}

// TestRegisterLanguage registers a language the way a plugin would, under a name of its own, and parses with it.
func TestRegisterLanguage(t *testing.T) {
	g, _ := Builtin().Language("go")
	set := NewSet(append(Builtin().Languages(), &Language{Name: "plugin-go", Grammar: g.Grammar, Extensions: []string{".gox"},
		Queries: map[string]string{
			"highlights": `(identifier) @variable ((identifier) @constant (#lua-match? @constant "^[A-Z][A-Z_]+$"))`,
		}})...)
	l, ok := set.ForFile("x.gox", nil)
	if !ok || l.Name != "plugin-go" {
		t.Fatalf("ForPath = %v, %v", l, ok)
	}
	p, err := NewParser(l)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	src := []byte("package x\nvar MAX_N, other = 1, 2\n")
	p.Parse(src)
	spans, _ := p.Highlights(0, len(src))
	got := map[string]string{}
	for _, s := range spans {
		got[string(src[s.Range.Start:s.Range.End])] = s.Capture
	}
	if got["MAX_N"] != "constant" || got["other"] != "variable" {
		t.Fatalf("captures = %v", got)
	}
}
