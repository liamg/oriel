package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// ED-15: the presenters for a file are those whose When takes it, by Order then name; one that panics is left out.
func TestPresentersForAFile(t *testing.T) {
	r := registry.New()
	isMD := func(p string) bool { return len(p) > 3 && p[len(p)-3:] == ".md" }
	r.Contribute(registry.BufferPresenter{Name: "slides", When: isMD, Order: 20})
	r.Contribute(registry.BufferPresenter{Name: "markdown", When: isMD, Order: 10})
	r.Contribute(registry.BufferPresenter{Name: "csv", When: func(p string) bool { return p == "a.csv" }})
	r.Contribute(registry.BufferPresenter{Name: "boom", When: func(string) bool { panic("boom") }})
	var got []string
	for _, p := range registry.Presenters(r, "README.md") {
		got = append(got, p.Name)
	}
	if len(got) != 2 || got[0] != "markdown" || got[1] != "slides" {
		t.Errorf("presenters for README.md = %v", got)
	}
	if p, ok := registry.PresenterNamed(r, "csv"); !ok || p.Name != "csv" {
		t.Errorf("PresenterNamed(csv) = %v, %v", p, ok)
	}
	if _, ok := registry.PresenterNamed(r, "nope"); ok {
		t.Error("found a presenter that isn't registered")
	}
}

// ED-15, the source map: each block knows its source lines, and a line no block maps belongs to the nearest block
// before it.
func TestPresentCtxMapsLinesToBlocks(t *testing.T) {
	ctx := &registry.PresentCtx{Lines: []string{"# T", "", "para", "more", "", "- item"}}
	body := func(*ui.Ctx, registry.PresentDraw) {}
	ctx.Block(0, 0, body)
	ctx.Block(2, 3, body).Space = 22
	ctx.Block(5, 5, body)
	bs := ctx.Blocks()
	if len(bs) != 3 || bs[1].From != 2 || bs[1].To != 3 || bs[1].Space != 22 {
		t.Fatalf("blocks = %+v", bs)
	}
	for line, want := range map[int]int{0: 0, 1: 0, 2: 1, 3: 1, 4: 1, 5: 2, 9: 2} {
		if got := ctx.BlockAt(line); got != want {
			t.Errorf("BlockAt(%d) = %d, want %d", line, got, want)
		}
	}
	if got := (&registry.PresentCtx{}).BlockAt(3); got != -1 {
		t.Errorf("BlockAt with no blocks = %d", got)
	}
}

// ED-15: an editor action shows LabelOn and IconOn while it's on, for the window it's drawn in.
func TestEditorActionLabelFollowsOn(t *testing.T) {
	a := registry.EditorAction{Name: "md", Label: "Preview", LabelOn: "Edit", Icon: "◱",
		On: func(w registry.EditorWindow) bool { return w.Presenter == "markdown" }}
	if on, icon, label := a.Shown(registry.EditorWindow{}); on || label != "Preview" || icon != "◱" {
		t.Errorf("off: %v %q %q", on, icon, label)
	}
	if on, icon, label := a.Shown(registry.EditorWindow{Presenter: "markdown"}); !on || label != "Edit" || icon != "◱" {
		t.Errorf("on: %v %q %q", on, icon, label)
	}
	b := registry.EditorAction{Name: "t", Label: "x", On: func(registry.EditorWindow) bool { panic("boom") }}
	if on, _, _ := b.Shown(registry.EditorWindow{}); on {
		t.Error("a panicking On is on")
	}
}
