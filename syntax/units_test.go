package syntax

import (
	"strings"

	"github.com/liamg/oriel/text"
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

// RV-5, done when: "units are correct for fixtures in 4 languages". Each case is a line, found by its text, and the
// breadcrumb of explain units around it (ui.md 07b: "the smallest syntax node that owns a change: a function, a
// method, a case, an if, a loop, a closure, a type"), then the units owning a set of changed lines.
func TestUnitFixtures(t *testing.T) {
	type at struct{ line, want string }
	for _, c := range []struct {
		lang, file string
		at         []at
		changed    []string // lines changed
		owners     []string // the units owning them
	}{
		{"go", "round.go", []at{
			{"t := scaled.Truncate(0)", "func roundHalfEven"},
			{"\t\t\treturn t.Shift(-p)", "func roundHalfEven › switch diff.Cmp(half) › case 0 › if t.IntPart()%2 == 0"},
			{"\t\treturn t.Add(sign(v)).Shift(-p)\n\t}", "func roundHalfEven › switch diff.Cmp(half) › case 0"},
			{"\tcase 1:", "func roundHalfEven › switch diff.Cmp(half) › case 1"},
			{"Currency string", "type Money"},
			{"return Money{}, fmt.Errorf", "func RoundMinor › if !ok"},
			{"round := func", "func RoundMinor"}, // the line assigns the closure too
			{"=return roundHalfEven(v, p) }", "func RoundMinor › func(v Decimal) Decimal { return roundH…"},
			{"m.Lines[i] = round", "func RoundMinor › for i := range m.Lines"},
		}, []string{"diff := scaled", "\tcase 0:", "\t\t\treturn t.Shift(-p)", "\tfor i := range m.Lines", "V        Decimal"},
			[]string{"func roundHalfEven", "case 0", "if t.IntPart()%2 == 0", "type Money", "for i := range m.Lines"}},
		{"python", "round.py", []at{
			{"t = scaled.truncate(0)", "func round_half_even"},
			{"                return t.shift(-p)", "func round_half_even › match (scaled - t).compare(HALF) › case 0 › if t.int_part() % 2 == 0"},
			{"case 1:", "func round_half_even › match (scaled - t).compare(HALF) › case 1"},
			{"raise ValueError", "type Money › func round_minor › if p is None"},
			{"rnd = lambda", "type Money › func round_minor"},
			{"=v: round_half_even(v, p)", "type Money › func round_minor › lambda v: round_half_even(v, p)"},
			{"self.lines[i] = rnd(line)", "type Money › func round_minor › for i, line in enumerate(self.lines)"},
		}, []string{"scaled = v.shift(p)", "            return t.add(sign(v)).shift(-p)\n    return", "        rnd = lambda"},
			[]string{"func round_half_even", "case 0", "func round_minor"}},
		{"typescript", "round.ts", []at{
			{"const t = scaled.truncate(0);", "func roundHalfEven"},
			{"        return t.shift(-p);", "func roundHalfEven › switch (scaled.sub(t).abs().cmp(HALF)) › case 0 › if (t.intPart() % 2 === 0)"},
			{"case 1:", "func roundHalfEven › switch (scaled.sub(t).abs().cmp(HALF)) › case 1"},
			{"==> roundHalfEven(v, p)", "type Money › func roundMinor › func round › (v: Decimal) => roundHalfEven(v, p)"},
			{"throw new Error", "type Money › func roundMinor › if (p === undefined)"},
			{"this.lines[i] = round", "type Money › func roundMinor › for (let i = 0; i < this.lines.length; …"},
		}, []string{"const scaled", "      if (t.intPart()", "return new Money"},
			[]string{"func roundHalfEven", "if (t.intPart() % 2 === 0)", "func roundMinor"}},
		{"rust", "round.rs", []at{
			{"let t = scaled.trunc();", "func round_half_even"},
			{"                return t.shift(-p);", "func round_half_even › match (scaled - t).abs().cmp(&HALF) › Ordering::Equal => › if t.int_part() % 2 == 0"},
			{"Ordering::Less", "func round_half_even › match (scaled - t).abs().cmp(&HALF) › Ordering::Less => t.shift(-p),"},
			{"currency: String", "type Money"},
			{"*line = round", "type Money › func round_minor › for line in self.lines.iter_mut()"},
			{"let round = |v", "type Money › func round_minor"},
			{"=| round_half_even(v, p)", "type Money › func round_minor › |v: Decimal| round_half_even(v, p)"},
		}, []string{"let scaled", "Ordering::Greater", "            t.add(sign(v)).shift(-p)", "Ok(Money {"},
			[]string{"func round_half_even", "Ordering::Greater => t.add(sign(v)).shi…", "Ordering::Equal =>", "func round_minor"}},
	} {
		p, src := parse(t, c.lang, "testdata/units/"+c.file)
		all, err := p.Units()
		if err != nil {
			t.Fatal(err)
		}
		us := text.FilterUnits(all, text.Explainable)
		lineOf := func(needle string) text.Range {
			if exact, ok := strings.CutPrefix(needle, "="); ok { // just these bytes, not the whole line
				i := strings.Index(string(src), exact)
				return text.Range{Start: i, End: i + len(exact)}
			}
			i := strings.Index(string(src), needle)
			if i < 0 {
				t.Fatalf("%s: no %q", c.file, needle)
			}
			start := strings.LastIndexByte(string(src[:i]), '\n') + 1
			end := i + strings.IndexByte(string(src[i:]), '\n')
			for start < end && (src[start] == ' ' || src[start] == '\t') {
				start++
			}
			return text.Range{Start: start, End: end}
		}
		for _, a := range c.at {
			var crumbs []string
			for _, i := range text.UnitPath(us, text.Innermost(us, lineOf(a.line))) {
				crumbs = append(crumbs, us[i].Label(src))
			}
			if got := strings.Join(crumbs, " › "); got != a.want {
				t.Errorf("%s: at %q the units are %q, want %q", c.lang, a.line, got, a.want)
			}
		}
		var rs []text.Range
		for _, l := range c.changed {
			rs = append(rs, lineOf(l))
		}
		var got []string
		for _, i := range text.Owners(us, rs) {
			got = append(got, us[i].Label(src))
		}
		if strings.Join(got, "|") != strings.Join(c.owners, "|") {
			t.Errorf("%s: the changed lines' units are %q, want %q", c.lang, got, c.owners)
		}
	}
}
