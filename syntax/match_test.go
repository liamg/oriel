package syntax

import (
	"testing"

	"github.com/liamg/oriel/text"
)

// probeLanguage is Go with a query kind of a plugin's own.
func probeLanguage(t *testing.T) *Language {
	t.Helper()
	g, _ := Builtin().Language("go")
	return &Language{Name: "go-probe", Grammar: g.Grammar, Queries: map[string]string{
		"probe": `((function_declaration name: (identifier) @fn.name) @fn (#match? @fn.name "^Test") (#set! kind "test"))`,
	}}
}

func TestParserQueryNamed(t *testing.T) {
	p, err := NewParser(probeLanguage(t))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	p.Parse([]byte("package x\n\nfunc helper() {}\n\nfunc TestA(t *T) {}\n"))
	ms, err := p.Query("probe")
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 1 {
		t.Fatalf("matches = %+v, want one", ms)
	}
	name, ok := ms[0].Get("fn.name")
	if !ok || name.Text != "TestA" || name.Start != (text.Pos{Line: 4, Col: 5}) || name.End != (text.Pos{Line: 4, Col: 10}) {
		t.Errorf("fn.name = %+v", name)
	}
	if fn, _ := ms[0].Get("fn"); fn.Range.Start != 29 || fn.Start.Line != 4 {
		t.Errorf("fn = %+v", fn)
	}
	if ms[0].Props["kind"] != "test" {
		t.Errorf("props = %v, want kind=test", ms[0].Props)
	}
	if none, err := p.Query("no-such-kind"); err != nil || none != nil {
		t.Errorf("unknown kind = %v, %v; want nothing", none, err)
	}
}

func TestDocumentQueryAfterChanges(t *testing.T) {
	buf := text.New([]byte("package x\n\nfunc TestA(t *T) {}\n"), text.Author{})
	d, err := NewDocument(probeLanguage(t), buf.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	end := buf.Snapshot().Len()
	ch, err := buf.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{Start: end, End: end}, Text: []byte("\nfunc TestB(t *T) {}\n")}}})
	if err != nil {
		t.Fatal(err)
	}
	d.Change(ch)
	ms, err := d.Query("probe")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range ms {
		c, _ := m.Get("fn.name")
		names = append(names, c.Text)
	}
	if len(names) != 2 || names[0] != "TestA" || names[1] != "TestB" {
		t.Errorf("names = %q, want TestA and TestB", names)
	}
	d.Close()
	if _, err := d.Query("probe"); err == nil {
		t.Error("query on a closed document: want an error")
	}
}

// TestTestsQueries checks each language's tests.scm compiles and finds a test, for testrun's discovery.
func TestTestsQueries(t *testing.T) {
	for _, c := range []struct{ lang, src, capture, name string }{
		{"go", "package x\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {}\n", "test.name", "TestA"},
		{"python", "def test_a():\n    pass\n", "test.name", "test_a"},
		{"typescript", "it('a', () => {})\n", "test.name", "'a'"},
		{"tsx", "it('a', () => { render(<A />) })\n", "test.name", "'a'"},
		{"javascript", "test.each([1])('a %s', () => {})\n", "each.name", "'a %s'"},
		{"rust", "#[test]\nfn a() {}\n", "test.name", "a"},
	} {
		l, _ := Builtin().Language(c.lang)
		p, err := NewParser(l)
		if err != nil {
			t.Fatal(err)
		}
		p.Parse([]byte(c.src))
		ms, err := p.Query("tests")
		p.Close()
		if err != nil {
			t.Errorf("%s: %v", c.lang, err)
			continue
		}
		found := false
		for _, m := range ms {
			if cp, ok := m.Get(c.capture); ok && cp.Text == c.name {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no @%s %q in %+v", c.lang, c.capture, c.name, ms)
		}
	}
}
