package uireg_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// The presenters for a file are those whose When takes it, by Order then name; one that panics is left out.
func TestPresentersForAFile(t *testing.T) {
	r := uireg.New()
	isMD := func(p string) bool { return len(p) > 3 && p[len(p)-3:] == ".md" }
	r.Contribute(uireg.BufferPresenter{Name: "slides", When: isMD, Order: 20})
	r.Contribute(uireg.BufferPresenter{Name: "markdown", When: isMD, Order: 10})
	r.Contribute(uireg.BufferPresenter{Name: "csv", When: func(p string) bool { return p == "a.csv" }})
	r.Contribute(uireg.BufferPresenter{Name: "boom", When: func(string) bool { panic("boom") }})
	var got []string
	for _, p := range uireg.Presenters(r, "README.md") {
		got = append(got, p.Name)
	}
	if len(got) != 2 || got[0] != "markdown" || got[1] != "slides" {
		t.Errorf("presenters for README.md = %v", got)
	}
	if p, ok := uireg.PresenterNamed(r, "csv"); !ok || p.Name != "csv" {
		t.Errorf("PresenterNamed(csv) = %v, %v", p, ok)
	}
	if _, ok := uireg.PresenterNamed(r, "nope"); ok {
		t.Error("found a presenter that isn't registered")
	}
}

// The source map: each block knows its source lines, and a line no block maps belongs to the nearest block
// before it.
func TestPresentCtxMapsLinesToBlocks(t *testing.T) {
	ctx := &uireg.PresentCtx{Lines: []string{"# T", "", "para", "more", "", "- item"}}
	body := func(*ui.Ctx, uireg.PresentDraw) {}
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
	if got := (&uireg.PresentCtx{}).BlockAt(3); got != -1 {
		t.Errorf("BlockAt with no blocks = %d", got)
	}
}

// LineBlocks is BlockAt for every line at once, overlapping and out-of-order blocks included.
func TestPresentCtxLineBlocksAgreesWithBlockAt(t *testing.T) {
	body := func(*ui.Ctx, uireg.PresentDraw) {}
	ctx := &uireg.PresentCtx{}
	for _, b := range [][2]int{{3, 4}, {1, 1}, {6, 9}, {7, 7}, {2, 8}, {12, 12}, {-2, -1}, {20, 30}} {
		ctx.Block(b[0], b[1], body)
	}
	got := ctx.LineBlocks(15)
	for line := range 15 {
		if want := ctx.BlockAt(line); got[line] != want {
			t.Errorf("LineBlocks(15)[%d] = %d, BlockAt = %d", line, got[line], want)
		}
	}
	for _, at := range (&uireg.PresentCtx{}).LineBlocks(3) {
		if at != -1 {
			t.Errorf("LineBlocks with no blocks = %d", at)
		}
	}
}

// An editor action shows LabelOn and IconOn while it's on, for the window it's drawn in.
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
