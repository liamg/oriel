package widget

import (
	"strings"

	"github.com/liamg/oriel/render"
	"testing"

	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The gallery as a screen reader meets it: every widget's role, name, value and state.
func TestGalleryAccessTree(t *testing.T) {
	h, g := setup(t, theme.Dark)
	g.modalOpen = true
	h.frame()
	h.frame()
	snapshot.Golden(t, "testdata/gallery.a11y", []byte(h.u.AccessTree().Dump()))
}

// A reader's click is a click, and its focus is focus: the same paths as the mouse and keyboard.
func TestAccessActions(t *testing.T) {
	clicks := 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("save")
		if Button(c, "save", "Save", ButtonOpts{Help: "test/button"}) {
			clicks++
		}
	})
	if !h.u.Act(id, ui.ActionClick) {
		t.Fatal("the button refused a click")
	}
	h.frame()
	if clicks != 1 || h.u.Focused() != id {
		t.Errorf("after a reader's click: %d clicks, focused %v", clicks, h.u.Focused() == id)
	}
	tree := h.u.AccessTree()
	if btn := tree.Children[0]; btn.Role != ui.RoleButton || btn.Name != "Save" || !btn.Focused || !strings.Contains(strings.Join(actions(btn), ","), "click") {
		t.Errorf("button node %+v", btn)
	}
}

func actions(n *ui.AccessNode) []string {
	var out []string
	for _, a := range n.Actions {
		out = append(out, string(a))
	}
	return out
}

// Updates carry only what changed, and the focus.
func TestAccessDiff(t *testing.T) {
	text := ""
	var input ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Button(c, "a", "A", ButtonOpts{Help: "test/button"})
		input = c.Next("q")
		Input(c, "q", &text, InputOpts{Label: "Search", Help: "test/input"})
	})
	first := h.u.AccessTree()
	if up := render.AccessDiff(nil, first); len(up.Changed) != 3 || len(up.Removed) != 0 {
		t.Fatalf("the first update has everything: %d changed, %d removed", len(up.Changed), len(up.Removed))
	}
	h.u.Act(input, ui.ActionFocus)
	h.frame()
	h.keys("x")
	next := h.u.AccessTree()
	up := render.AccessDiff(first, next)
	if up.Focus != uint64(input) || len(up.Changed) != 1 || up.Changed[0].Value != "x" {
		t.Errorf("after focusing and typing: focus %v, changed %d (%+v)", up.Focus == uint64(input), len(up.Changed), up.Changed)
	}
}
