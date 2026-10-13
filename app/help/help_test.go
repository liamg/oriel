package help

import (
	"testing"
	"testing/fstest"
)

func TestParse(t *testing.T) {
	p, err := Parse(`---
id: hud/usage
covers: [hud.tat.usage.*, hud.panel.usage]
related: [hud/usage#five-hour, session/tally]
---

# Usage windows

## Weekly usage window {#weekly-window}

{{live "usage.week"}}

Your plan allows a fixed amount
of model use per rolling week.

- one
  continued
- two

`+"```lua\noriel.usage.window()\n```"+`

## Five-hour window
`, "builtin", "hud/usage.md")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID != "hud/usage" || p.Title != "Usage windows" || len(p.Covers) != 2 || p.Related[0] != "hud/usage#five-hour" {
		t.Errorf("page = %+v", p)
	}
	kinds := ""
	for _, b := range p.Blocks {
		kinds += b.Kind + " "
	}
	if kinds != "heading live para list code heading " {
		t.Errorf("blocks: %s", kinds)
	}
	if p.Blocks[2].Text != "Your plan allows a fixed amount of model use per rolling week." || p.Blocks[3].Items[0] != "one continued" {
		t.Errorf("para %q, list %q", p.Blocks[2].Text, p.Blocks[3].Items)
	}
	if got := p.Anchors(); len(got) != 2 || got[0] != "weekly-window" || got[1] != "five-hour-window" {
		t.Errorf("anchors %v", got)
	}
	head, blocks, ok := p.Section("weekly-window")
	if !ok || head.Text != "Weekly usage window" || len(blocks) != 4 {
		t.Errorf("section: %+v %d", head, len(blocks))
	}
	if _, err := Parse("# no front matter", "x", "x.md"); err == nil {
		t.Error("a page without an id parsed")
	}
}

func TestIndex(t *testing.T) {
	mine := fstest.MapFS{"errors.md": {Data: []byte("---\nid: errors\n---\n# Mine\n## Toasts {#toasts}\n")}}
	ix, errs := Load([]Source{{"builtin", Builtin()}, {"config", mine}})
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	if p, _, ok := ix.For("errors#toasts"); !ok || p.Title != "Mine" || p.Source != "config" {
		t.Errorf("a later source's page should win: %+v %v", p, ok)
	}
	if _, _, ok := ix.For("shell/omnibox"); !ok {
		t.Error("no shell/omnibox")
	}
	if _, _, ok := ix.For("layout/panes#nope"); ok {
		t.Error("a missing anchor resolved")
	}
	if len(ix.Topics()) < 10 {
		t.Errorf("%d topics", len(ix.Topics()))
	}
}

// Oriel's own pages all parse.
func TestBuiltinPagesParse(t *testing.T) {
	ix, errs := Load([]Source{{"builtin", Builtin()}})
	for _, err := range errs {
		t.Error(err)
	}
	for _, id := range []string{"errors", "shell/omnibox", "shell/pickers", "shell/tabs", "editor/pane", "layout/panes", "ui/context-menu", "shell/help"} {
		if _, _, ok := ix.For(id); !ok {
			t.Errorf("no page %s", id)
		}
	}
}
