package widget

import (
	"slices"
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
	if !h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionClick}) {
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
	h.u.Act(ui.AccessAction{ID: uint64(input), Action: ui.ActionFocus})
	h.frame()
	h.keys("x")
	next := h.u.AccessTree()
	up := render.AccessDiff(first, next)
	if up.Focus != uint64(input) || len(up.Changed) != 1 || up.Changed[0].Value != "x" {
		t.Errorf("after focusing and typing: focus %v, changed %d (%+v)", up.Focus == uint64(input), len(up.Changed), up.Changed)
	}
}

// A reader setting a field's text edits it as typing would: undo takes it back.
func TestAccessSetValue(t *testing.T) {
	text := "hello"
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("q")
		Input(c, "q", &text, InputOpts{Label: "Name", Help: "test/input"})
	})
	if !h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionSetValue, Value: "world"}) {
		t.Fatal("the field refused a value")
	}
	h.frame()
	if text != "world" {
		t.Fatalf("after setting the value: %q", text)
	}
	if n := h.access(id); n.Value != "world" {
		t.Errorf("the tree's value: %q", n.Value)
	}
	h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionFocus})
	h.frame()
	if !h.u.Key(render.KeyEvent{Key: "z", Mods: platform.primary}) {
		t.Fatal("nothing took undo")
	}
	h.frame()
	if text != "hello" {
		t.Errorf("undo after a reader's edit: %q", text)
	}
	h.u.Key(render.KeyEvent{Key: "Z", Mods: platform.primary | render.ModShift})
	h.frame()
	if text != "world" {
		t.Errorf("redo: %q", text)
	}
}

// A reader's selection is the field's, and the next update says so.
func TestAccessSelect(t *testing.T) {
	text := "hello world"
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("q")
		Input(c, "q", &text, InputOpts{Label: "Name", Help: "test/input"})
	})
	before := h.u.AccessTree()
	if !h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionSelect, Selection: ui.TextRange{Start: 6, End: 11}}) {
		t.Fatal("the field refused a selection")
	}
	h.frame()
	up := render.AccessDiff(before, h.u.AccessTree())
	if len(up.Changed) != 1 || up.Changed[0].ID != uint64(id) || up.Changed[0].Selection != (ui.TextRange{Start: 6, End: 11}) {
		t.Fatalf("the update after selecting: %+v", up.Changed)
	}
	h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionFocus})
	h.frame()
	h.keys("t", "h", "e", "r", "e")
	if text != "hello there" {
		t.Errorf("typing over the reader's selection: %q", text)
	}
}

// Check boxes and toggles are check boxes to a reader, with their state; a reader's press flips them.
func TestAccessCheckBox(t *testing.T) {
	var on, wrap bool
	var check, toggle ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		st := CheckOff
		if on {
			st = CheckOn
		}
		check = c.Next("c")
		if Check(c, "c", st, CheckOpts{Text: "Spell", Help: "test/check"}) {
			on = !on
		}
		toggle = c.Next("t")
		if Toggle(c, "t", wrap, ToggleOpts{Label: "Wrap", Help: "test/toggle"}) {
			wrap = !wrap
		}
	})
	for _, id := range []ui.ID{check, toggle} {
		if n := h.access(id); n.Role != ui.RoleCheckBox || n.Checked == nil || *n.Checked {
			t.Errorf("before: %+v", n)
		}
		h.u.Act(ui.AccessAction{ID: uint64(id), Action: ui.ActionClick})
		h.frame()
		h.frame()
		if n := h.access(id); n.Checked == nil || !*n.Checked {
			t.Errorf("after a reader's press: %+v", n)
		}
	}
	if !on || !wrap {
		t.Errorf("the values: check %v, toggle %v", on, wrap)
	}
}

// A reader steps a slider, and a number field, by its step.
func TestAccessIncrement(t *testing.T) {
	v := 5.0
	n := "41"
	var slider, field ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		slider = c.Next("s")
		Slider(c, "s", &v, SliderOpts{Min: 0, Max: 10, Step: 1, Width: ui.Px(100), Label: "Size", Help: "test/slider"})
		field = c.Next("n")
		Input(c, "n", &n, InputOpts{Label: "Port", Help: "test/input", Accept: Digits, Step: 1})
	})
	if got := h.access(slider).Actions; !slices.Contains(got, ui.ActionIncrement) || !slices.Contains(got, ui.ActionDecrement) {
		t.Errorf("the slider's actions: %v", got)
	}
	h.u.Act(ui.AccessAction{ID: uint64(slider), Action: ui.ActionIncrement})
	h.frame()
	if v != 6 {
		t.Errorf("after incrementing the slider: %v", v)
	}
	h.u.Act(ui.AccessAction{ID: uint64(slider), Action: ui.ActionDecrement})
	h.u.Act(ui.AccessAction{ID: uint64(slider), Action: ui.ActionDecrement})
	h.frame()
	if v != 4 {
		t.Errorf("after decrementing it twice: %v", v)
	}
	h.u.Act(ui.AccessAction{ID: uint64(field), Action: ui.ActionIncrement})
	h.frame()
	if n != "42" {
		t.Errorf("after incrementing the number field: %q", n)
	}
	if h.u.Act(ui.AccessAction{ID: uint64(slider), Action: ui.ActionSetValue, Value: "9"}) {
		t.Error("the slider took a text value")
	}
}
