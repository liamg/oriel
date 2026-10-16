package help

import (
	"strings"
	"testing"
	"testing/fstest"
)

func searchIndex(t *testing.T) *Index {
	t.Helper()
	ix, errs := Load([]Source{{"test", fstest.MapFS{
		"panes.md": {Data: []byte("---\nid: layout/panes\n---\n# Panes\n\nPanes hold views.\n\n## Splits {#splits}\n\n" +
			"Split the editor with `:vsplit`, or drag a tab to an edge.\n\n{{keys \"pane.*\"}}\n\n## Zoom\n\nZoom makes one pane fill the window.\n")},
		"terminal.md": {Data: []byte("---\nid: terminal\n---\n# Terminal\n\nThe terminal runs a shell in the editor's directory.\n")},
	}}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return ix
}

// "How do I split the editor?" finds the section on splits first, with the line that says how; the words of the
// question that every page has ("how", "do", "the") count for nothing.
func TestSearch(t *testing.T) {
	ix := searchIndex(t)
	got := ix.Search("how do I split the editor?", 5, nil)
	if len(got) == 0 || got[0].ID != "layout/panes#splits" || got[0].Title != "Splits" ||
		got[0].Snippet != "Split the editor with `:vsplit`, or drag a tab to an edge." {
		t.Fatalf("split the editor: %+v", got)
	}
	if len(got) < 2 || got[1].ID != "terminal" {
		t.Errorf("the terminal mentions the editor, so it comes after: %+v", got)
	}
	if r := ix.Search("how do I", 5, nil); len(r) != 0 {
		t.Errorf("a query of stop words found %+v", r)
	}
	if r := ix.Search("zoom", 5, nil); len(r) != 1 || r[0].ID != "layout/panes#zoom" {
		t.Errorf("zoom: %+v", r)
	}
}

// A directive is searched and read as it expands: the keys a {{keys}} block lists now.
func TestSearchExpands(t *testing.T) {
	ix := searchIndex(t)
	expand := func(b Block) string {
		if b.Kind == "keys" && b.Text == "pane.*" {
			return "Keys:\n- ⌃w v: split vertically"
		}
		return ""
	}
	if r := ix.Search("vertically", 5, expand); len(r) != 1 || r[0].ID != "layout/panes#splits" || r[0].Snippet != "- ⌃w v: split vertically" {
		t.Errorf("vertically: %+v", r)
	}
	title, text, ok := ix.SectionText("layout/panes#splits", expand)
	if !ok || title != "Splits" || !strings.Contains(text, "drag a tab to an edge.\n\nKeys:\n- ⌃w v") || strings.Contains(text, "Zoom") {
		t.Errorf("the section: %q %q", title, text)
	}
	if _, text, _ := ix.SectionText("layout/panes", nil); !strings.Contains(text, "## Zoom") {
		t.Errorf("the page: %q", text)
	}
	if _, _, ok := ix.SectionText("nope", nil); ok {
		t.Error("read a page that isn't there")
	}
}
