package widget

import (
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var small = render.Point{X: 600, Y: 400}

func TestButton(t *testing.T) {
	var id ui.ID
	var clicks, disabledClicks int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{Pad: ui.Pad(10), Gap: 10}, func(c *ui.Ctx) {
			id = c.Next("save")
			if Button(c, "save", "Save", ButtonOpts{Primary: true, Help: "test/button"}) {
				clicks++
			}
			if Button(c, "off", "Off", ButtonOpts{Disabled: true, Help: "test/button"}) {
				disabledClicks++
			}
		})
	})
	x, y := mid(h.rect(id))
	h.click(x, y)
	if clicks != 1 {
		t.Fatalf("a click clicked %d times", clicks)
	}
	h.u.Focus(id)
	h.frame()
	h.keys("CR")
	h.keys(" ")
	if clicks != 3 {
		t.Errorf("Enter and Space: %d clicks, want 3", clicks)
	}
	h.click(x+90, y)
	if disabledClicks != 0 {
		t.Error("a disabled button clicked")
	}
}

func TestInput(t *testing.T) {
	text := ""
	var id ui.ID
	var res InputResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			id = c.Next("q")
			res = Input(c, "q", &text, InputOpts{Placeholder: "Ask…", Help: "test/input"})
		})
	})
	r := h.rect(id)
	h.click(r.Min.X+5, r.Min.Y+10)
	if h.u.Focused() != id {
		t.Fatal("a click didn't focus the input")
	}
	h.keys("h", "e", "l", "l", "o", "BS")
	if text != "hell" {
		t.Fatalf("text %q after typing hello and backspace", text)
	}
	h.keys("Home", "y", "End", "!")
	if text != "yhell!" {
		t.Fatalf("text %q after Home y End !", text)
	}
	h.keys("+Left", "+Left", "X")
	if text != "yhelX" {
		t.Errorf("text %q: shift-select two and type should replace them", text)
	}
	h.keys("Left", "Left", "Del")
	if text != "yheX" {
		t.Errorf("text %q after Left Left Del from the end of yhelX", text)
	}
	h.keys("CR")
	if !res.Submitted {
		t.Error("Enter didn't submit")
	}
	// A click at the start puts the cursor there.
	h.click(r.Min.X+10, r.Min.Y+10)
	h.keys("[")
	if text != "[yheX" {
		t.Errorf("text %q: the click should have put the cursor first", text)
	}
	h.keys("Esc")
	if !res.Cancelled || h.u.Focused() == id {
		t.Error("Escape didn't cancel and drop focus")
	}
}

func TestList(t *testing.T) {
	sel := 0
	var id ui.ID
	var res ListResult
	var built []int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		built = built[:0]
		id = c.Next("list")
		res = List(c, "list", 1000, &sel, ListOpts{Height: ui.Px(220), Help: "test/list"}, func(c *ui.Ctx, i int, selected bool) {
			built = append(built, i)
			c.Text("t", "row", ui.TextStyle{})
		})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+3*22+5)
	if sel != 3 || res.Activated != 3 || h.u.Focused() != id {
		t.Fatalf("click on row 3: sel %d, activated %d, focused %v", sel, res.Activated, h.u.Focused() == id)
	}
	h.keys("j", "j", "Down", "k")
	if sel != 5 {
		t.Errorf("j j Down k from 3: %d", sel)
	}
	h.keys("G")
	h.frame()
	if sel != 999 || !slices.Contains(built, 999) {
		t.Errorf("G: sel %d; the last row should be built and in view", sel)
	}
	h.keys("g", "PageDown")
	if sel != 9 {
		t.Errorf("g then PageDown: %d, want a page of 9 (220px / 22 less one)", sel)
	}
	h.keys("CR")
	if res.Activated != 9 {
		t.Errorf("Enter activated %d", res.Activated)
	}
}

func tree() []TreeItem {
	return []TreeItem{
		{ID: "internal", Label: "internal", Children: []TreeItem{
			{ID: "pricing", Label: "pricing", Children: []TreeItem{{ID: "round.go", Label: "round.go", Mark: "M"}, {ID: "tiers.go", Label: "tiers.go"}}},
		}},
		{ID: "go.mod", Label: "go.mod"},
	}
}

func TestTree(t *testing.T) {
	var id ui.ID
	var res TreeResult
	items := tree()
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree"})
	})
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+11)
	if res.Toggled != "internal" && res.Selected != "internal" {
		t.Fatalf("click on a folder: %+v", res)
	}
	h.keys("j", "l", "l")
	if res.Selected != "round.go" {
		t.Errorf("j l l: selected %q, want round.go (open pricing, step in)", res.Selected)
	}
	h.keys("CR")
	if res.Activated != "round.go" {
		t.Errorf("Enter on a leaf: %+v", res)
	}
	h.keys("h")
	if res.Selected != "pricing" {
		t.Errorf("h on a leaf goes to its parent: %q", res.Selected)
	}
	h.keys("h", "j")
	if res.Selected != "go.mod" {
		t.Errorf("h closes pricing, then j: %q", res.Selected)
	}
}

func TestTabs(t *testing.T) {
	active := 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tabs")
		Tabs(c, "tabs", []string{"Agent", "Terminal", "Problems"}, &active, TabsOpts{Help: "test/tabs"})
	})
	second, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(second)
	h.click(x, y)
	if active != 1 {
		t.Fatalf("click on the second tab: %d", active)
	}
	h.keys("l", "l", "h")
	if active != 1 {
		t.Errorf("l l h from 1: %d, want 1 (l stops at the end)", active)
	}
}

var items = []MenuItem{
	{Label: "Open usage details"}, {Label: "Copy value", Shortcut: "y"}, {Separator: true},
	{Label: "Hide this indicator", Disabled: true}, {Label: "Move to statusline…"},
}

func TestMenu(t *testing.T) {
	var id ui.ID
	chosen, closed := -1, false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("menu")
		ch, cl := Menu(c, "menu", items, MenuOpts{Help: "test/menu"})
		if ch >= 0 {
			chosen = ch
		}
		closed = closed || cl
	})
	h.u.Focus(id)
	h.frame()
	h.keys("j", "j", "CR")
	if chosen != 4 {
		t.Errorf("j j Enter chose %d; the separator and the disabled item are skipped", chosen)
	}
	row, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(row)
	h.click(x, y)
	if chosen != 1 {
		t.Errorf("click chose %d", chosen)
	}
	h.keys("Esc")
	if !closed {
		t.Error("Escape didn't close")
	}
}

func TestContextMenu(t *testing.T) {
	var target ui.ID
	var res ContextResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		target = c.Box("indicator", ui.Style{W: ui.Px(80), H: ui.Px(20), Interactive: true, Help: "hud/usage#weekly-window"}, nil)
		res = ContextMenu(c, "ctx", target, items)
	})
	x, y := mid(h.rect(target))
	h.rightClick(x, y)
	h.frame()
	h.keys("k", "CR")
	if res.Help != "hud/usage#weekly-window" {
		t.Fatalf("k from the top wraps to the last item, Help: %+v", res)
	}
	h.rightClick(x, y)
	h.frame()
	h.keys("j", "CR")
	if res.Chosen != 1 {
		t.Errorf("j Enter: %+v", res)
	}
	h.rightClick(x, y)
	h.click(590, 390)
	h.keys0()
	if h.u.Focused() != 0 && h.u.KeyMode() == "menu" {
		t.Error("a click outside left the menu open")
	}
}

// keys0 runs a frame with no keys.
func (h *harness) keys0() { h.frame() }

func TestPopoverAndModal(t *testing.T) {
	var anchor, pop, modal, inner, cancel ui.ID
	pOpen, mOpen := false, false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		anchor = c.Box("anchor", ui.Style{W: ui.Px(40), H: ui.Px(20)}, nil)
		pop = c.Next("pop")
		Popover(c, "pop", anchor, &pOpen, PopoverOpts{Width: 200, Help: "test/popover"}, func(c *ui.Ctx) {
			c.Text(nil, "hello", ui.TextStyle{})
		})
		modal = c.Next("modal")
		Modal(c, "modal", &mOpen, "Settings", ModalOpts{Width: 300, Help: "test/modal"}, func(c *ui.Ctx) {
			inner = c.Next("ok")
			Button(c, "ok", "OK", ButtonOpts{Help: "test/button"})
			cancel = c.Next("cancel")
			Button(c, "cancel", "Cancel", ButtonOpts{Help: "test/button"})
		})
	})
	pOpen = true
	h.frame()
	h.frame()
	if r := h.rect(pop); r.Min.Y < 24 {
		t.Errorf("popover at %v, want below its anchor", r)
	}
	h.keys("Esc")
	if pOpen {
		t.Error("Escape didn't close the popover")
	}

	mOpen = true
	h.frame()
	h.frame()
	if h.u.Focused() != modal {
		t.Fatal("an opened modal doesn't have focus")
	}
	for range 5 {
		h.u.FocusNext(1)
		h.frame()
		if f := h.u.Focused(); f != modal && f != inner && f != cancel {
			t.Fatalf("focus left the modal: %v", f)
		}
	}
	h.u.Focus(modal)
	h.frame()
	h.keys("Esc")
	if mOpen {
		t.Error("Escape didn't close the modal")
	}
	mOpen = true
	h.frame()
	h.click(5, 395)
	if mOpen {
		t.Error("a click on the scrim didn't close the modal")
	}
}

func TestToasts(t *testing.T) {
	var ts Toasts
	var stack ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		stack = c.Next("toasts")
		ShowToasts(c, "toasts", &ts)
	})
	ts.Push(Toast{Title: "Saved", Tone: render.Color{G: 200, A: 255}, TTL: 3 * time.Second, Help: "test/toast"})
	ts.Push(Toast{Title: "Push failed", Body: "git push origin main", Tone: render.Color{R: 255, A: 255}, Help: "test/toast"})
	h.frame()
	if ts.Len() != 2 {
		t.Fatalf("%d toasts", ts.Len())
	}
	if want := h.now.Add(3 * time.Second); !h.u.NextFrame().Equal(want) {
		t.Errorf("next frame %v, want the timeout at %v", h.u.NextFrame(), want)
	}
	h.now = h.now.Add(3 * time.Second)
	h.frame()
	if ts.Len() != 1 {
		t.Errorf("after its TTL: %d toasts", ts.Len())
	}
	h.u.FocusNext(1)
	h.frame()
	h.keys("x")
	if ts.Len() != 0 {
		t.Error("x didn't dismiss the focused toast")
	}
	ts.Push(Toast{Title: "Again", Help: "test/toast"})
	h.frame()
	h.frame()
	x, y := mid(h.rect(ui.Child(stack, 3)))
	h.click(x, y)
	if ts.Len() != 0 {
		t.Error("a click didn't dismiss the toast")
	}
}

func TestSlider(t *testing.T) {
	v := 0.5
	var id ui.ID
	changed := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("hue")
		if Slider(c, "hue", &v, SliderOpts{Min: 0, Max: 360, Step: 1, Width: ui.Px(200), Label: "Hue",
			Track: func(t float64) render.Color { return theme.FromOKLCH(theme.OKLCH{L: .7, C: .15, H: t * 360, A: 1}) }}) {
			changed++
		}
	})
	if v != 0.5 {
		t.Fatalf("building changed the value to %v", v)
	}
	track := h.rect(id)
	_, y := mid(track)
	h.click(track.Min.X+track.Dx()/4, y)
	if v < 89 || v > 91 || changed == 0 {
		t.Fatalf("a click a quarter along: %v (changed %d)", v, changed)
	}
	h.pointer(render.PointerPress, render.ButtonPrimary, track.Min.X+track.Dx()/4, y)
	h.frame()
	h.pointer(render.PointerMove, render.ButtonPrimary, track.Max.X+50, y)
	h.frame()
	h.pointer(render.PointerRelease, render.ButtonPrimary, track.Max.X+50, y)
	h.frame()
	if v != 360 {
		t.Fatalf("dragged past the end: %v", v)
	}
	h.keys("h", "Left")
	if v != 358 {
		t.Errorf("two steps down: %v", v)
	}
	h.keys("+Right")
	if v != 360 {
		t.Errorf("shift-right is ten steps, clamped: %v", v)
	}
}

func TestClickable(t *testing.T) {
	var id ui.ID
	var in, off ui.Interaction
	clicks := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{Pad: ui.Pad(10), Gap: 10}, func(c *ui.Ctx) {
			id = c.Next("row")
			in = Clickable(c, "row", ui.Style{W: ui.Px(80), H: ui.Px(24), Focusable: true, Help: "test/clickable"}, nil)
			if in.Clicked {
				clicks++
			}
			off = Clickable(c, "off", ui.Style{W: ui.Px(80), H: ui.Px(24), Disabled: true, Help: "test/clickable"}, nil)
		})
	})
	x, y := mid(h.rect(id))
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if !in.Hovered || h.u.Cursor() != render.CursorPointer {
		t.Errorf("under the pointer: hovered %v, cursor %v", in.Hovered, h.u.Cursor())
	}
	h.click(x, y)
	if clicks != 1 {
		t.Fatalf("a click clicked %d times", clicks)
	}
	h.u.Focus(id)
	h.frame()
	h.keys("CR")
	if clicks != 2 {
		t.Errorf("Enter on the focused row: %d clicks, want 2", clicks)
	}
	h.click(x+90, y)
	if off.Clicked {
		t.Error("a disabled one clicked")
	}
	if n := h.access(id); n == nil || n.Role != render.RoleButton {
		t.Errorf("its role: %+v", n)
	}
}

func TestSegmented(t *testing.T) {
	active, changes := 0, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("seg")
		if Segmented(c, "seg", []string{"DARK", "LIGHT"}, &active, SegmentedOpts{Help: "test/segmented"}) {
			changes++
		}
	})
	second, _ := h.u.Rect(ui.Child(id, 1))
	x, y := mid(second)
	h.click(x, y)
	h.click(x, y)
	if active != 1 || changes != 1 {
		t.Errorf("two clicks on the second: active %d, %d changes; want 1 and 1", active, changes)
	}
}

// AI-14: a segment can carry a badge after its label, a chip of its own: the account switcher's plan tier. A segment
// with none draws none, and the accessible name says the badge.
func TestSegmentedBadges(t *testing.T) {
	active := 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Segmented(c, "seg", []string{"WORK · CLAUDE MAX", "HOME · CLAUDE"}, &active, SegmentedOpts{Badges: []string{"Max 20×", ""},
			Help: "test/segmented"})
	})
	h.frame()
	if !h.drew("MAX 20×") || h.find("WORK · CLAUDE MAX, Max 20×") == nil || h.find("HOME · CLAUDE") == nil {
		t.Errorf("drew %q", h.texts())
	}
}

// An icon button in a row shows while the pointer's in the row, stays while it moves onto the button, and clicks.
// A primary button with a Tone is filled with it rather than the accent.
func TestButtonTone(t *testing.T) {
	violet := color.NRGBA{R: 0xb9, G: 0x8a, B: 0xff, A: 0xff}
	clicks := 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("fwd")
		if Button(c, "fwd", "Forward", ButtonOpts{Primary: true, Tone: violet, Help: "test/button"}) {
			clicks++
		}
	})
	filled := false
	for _, op := range h.list.Ops {
		filled = filled || op.Kind == render.OpRect && op.Style.Fill == violet
	}
	if !filled {
		t.Error("no rect was filled with the tone")
	}
	h.click(mid(h.rect(id)))
	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}
}

func TestIconButtonInARow(t *testing.T) {
	var row ui.ID
	shown, clicks := false, 0
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		row = c.Next("row")
		Clickable(c, "row", ui.Style{Dir: ui.Row, W: ui.Px(200), H: ui.Px(30), Cross: ui.Center, Help: "test/row"}, func(c *ui.Ctx) {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			shown = PointerIn(c)
			if shown && IconButton(c, "x", "✕", IconButtonOpts{Label: "Forget", Help: "test/row"}) {
				clicks++
			}
		})
	})
	r := h.rect(row)
	h.pointer(render.PointerMove, 0, r.Min.X+20, r.Min.Y+15)
	h.frame()
	h.frame()
	if !shown {
		t.Fatal("the button isn't shown with the pointer in the row")
	}
	x, y := r.Max.X-10, r.Min.Y+15
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.frame()
	if !shown {
		t.Fatal("the button went when the pointer moved onto it")
	}
	h.click(x, y)
	if clicks != 1 {
		t.Errorf("%d clicks", clicks)
	}
	h.pointer(render.PointerMove, 0, r.Max.X+50, y)
	h.frame()
	h.frame()
	if shown {
		t.Error("the button stays with the pointer gone")
	}
}

// texts are the runs of text the last frame drew, in order.
func (h *harness) texts() []string {
	var out []string
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs {
			out = append(out, op.Run.Text)
		}
	}
	return out
}

func (h *harness) drew(s string) bool {
	for _, t := range h.texts() {
		if t == s {
			return true
		}
	}
	return false
}

// A tree's rows can carry a note after the label, a tint, a strike-through and content at their right; items
// that say Open start expanded; and the tree takes keys in the mode its owner names.
func TestTreeRowsAndOptions(t *testing.T) {
	var id ui.ID
	var res TreeResult
	red := render.Color{R: 255, A: 255}
	items := []TreeItem{
		{ID: "staged", Label: "STAGED", Section: true, Open: true, Children: []TreeItem{
			{ID: "round.go", Label: "round.go", Note: "← amount.go", Tone: red, Strike: true},
		}},
		{ID: "closed", Label: "closed", Children: []TreeItem{{ID: "hidden.go", Label: "hidden.go"}}},
	}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree", KeyMode: "explorer",
			Trailing: func(c *ui.Ctx, it TreeItem, selected bool) {
				c.Text("n", "+31 -9 "+it.ID, ui.TextStyle{})
			}})
	})
	h.frame()
	for _, want := range []string{"STAGED", "round.go", "← amount.go", "+31 -9 round.go", "+31 -9 staged"} {
		if !h.drew(want) {
			t.Errorf("no %q in %q", want, h.texts())
		}
	}
	if h.drew("hidden.go") {
		t.Error("an item without Open started expanded")
	}
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+33)
	if res.Activated != "round.go" {
		t.Errorf("click on the open section's child: %+v", res)
	}
	if m := h.u.KeyMode(); m != "explorer" {
		t.Errorf("the focused tree takes keys in %q", m)
	}
	h.keys("k", "CR")
	if h.drew("round.go") {
		t.Error("Enter on an open section didn't close it")
	}
}

// A diff stat leaves out a side with nothing in it, and its bar splits between adds and deletes.
func TestDiffStat(t *testing.T) {
	var id ui.ID
	add, del := 31, 9
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("ds")
		DiffStat(c, "ds", add, del, DiffStatOpts{Bar: true, Scale: 40, Width: 40})
	})
	h.frame()
	if !h.drew("+31") || !h.drew("-9") {
		t.Errorf("drew %q", h.texts())
	}
	a, d := h.rect(ui.Child(ui.Child(id, "bar"), "a")), h.rect(ui.Child(ui.Child(id, "bar"), "d"))
	if a.Dx() != 31 || d.Dx() != 9 {
		t.Errorf("bar split %v / %v, want 31 / 9", a.Dx(), d.Dx())
	}
	add, del = 0, 62
	h.frame()
	if h.drew("+0") || !h.drew("-62") {
		t.Errorf("a delete-only change drew %q", h.texts())
	}
}

// A binary change has no lines to count: it's marked "bin", not "+0".
func TestDiffStatBinary(t *testing.T) {
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		DiffStat(c, "ds", 0, 0, DiffStatOpts{Bar: true, Binary: true})
	})
	h.frame()
	if h.drew("+0") || !h.drew("bin") {
		t.Errorf("a binary change drew %q", h.texts())
	}
}

// A toggle chip flips on a click and on Space while focused.
func TestToggleChip(t *testing.T) {
	var id ui.ID
	on := false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("chip")
		on = ToggleChip(c, "chip", "± lines", on, ToggleChipOpts{Help: "test/chip"})
	})
	h.click(mid(h.rect(id)))
	if !on {
		t.Fatal("a click didn't turn it on")
	}
	h.frame()
	if n := h.access(id); n == nil || !n.Selected {
		t.Errorf("its accessible state: %+v", n)
	}
	h.keys(" ")
	if on {
		t.Error("Space didn't turn it off")
	}
}

// A toned toggle chip (a severity filter) draws its label in its tone, tinted more strongly while on.
func TestToggleChipTone(t *testing.T) {
	red := color.NRGBA{R: 0xff, G: 0x5f, B: 0x6d, A: 0xff}
	var id ui.ID
	on := false
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("chip")
		on = ToggleChip(c, "chip", "1 high", on, ToggleChipOpts{Help: "test/chip", Tone: red})
	})
	fill := func() render.Color {
		for _, op := range h.list.Ops {
			if op.Kind == render.OpRect && op.Rect == h.rect(id) {
				return op.Style.Fill
			}
		}
		return render.Color{}
	}
	label := false
	for _, op := range h.list.Ops {
		label = label || op.Kind == render.OpGlyphs && op.Run.Text == "1 high" && op.Color == red
	}
	if !label {
		t.Error("the label isn't drawn in the chip's tone")
	}
	off := fill()
	h.click(mid(h.rect(id)))
	h.frame()
	if !on || fill().A <= off.A {
		t.Errorf("on: %v, fill %v, off %v", on, fill(), off)
	}
}

// When the rows change under it (a file staged moves to another section), the selection stays on the same item.
func TestTreeSelectionFollowsItsItem(t *testing.T) {
	var res TreeResult
	items := []TreeItem{{ID: "a", Label: "a"}, {ID: "b", Label: "b"}, {ID: "c", Label: "c"}}
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("tree")
		res = Tree(c, "tree", items, TreeOpts{Height: ui.Px(300), Help: "test/tree"})
	})
	r := h.rect(id)
	h.click(r.Min.X+40, r.Min.Y+11+22)
	if res.Selected != "b" {
		t.Fatalf("selected %q", res.Selected)
	}
	items = []TreeItem{{ID: "b", Label: "b"}, {ID: "a", Label: "a"}, {ID: "c", Label: "c"}}
	h.frame()
	if res.Selected != "b" {
		t.Errorf("after b moved up, selected %q", res.Selected)
	}
	items = []TreeItem{{ID: "a", Label: "a"}, {ID: "c", Label: "c"}}
	h.frame()
	if res.Selected != "a" {
		t.Errorf("after b went, selected %q, want the row in its place", res.Selected)
	}
}

// A menu's headings name its groups and can't be chosen; items can carry an icon and a detail at the right.
func TestMenuHeadingsAndDetails(t *testing.T) {
	chosen := -1
	items := []MenuItem{
		{Label: "Quick fix", Heading: true},
		{Label: "Use %s", Icon: "●", Detail: "gopls"},
		{Label: "Refactor", Heading: true},
		{Label: "Extract to variable", Icon: "↗", Detail: "gopls"},
	}
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("m")
		if i, _ := Menu(c, "m", items, MenuOpts{Help: "test/menu"}); i >= 0 {
			chosen = i
		}
	})
	h.u.Focus(id)
	h.frame()
	for _, want := range []string{"QUICK FIX", "Use %s", "●", "gopls", "REFACTOR"} {
		if !h.drew(want) {
			t.Errorf("no %q in %q", want, h.texts())
		}
	}
	h.keys("j", "CR")
	if chosen != 3 {
		t.Errorf("j from the first item skipped the heading to %d, want 3", chosen)
	}
}

// An input that selects on focus has all its text selected when it gets focus, so typing replaces it.
func TestInputSelectOnFocus(t *testing.T) {
	text := "greeting"
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("in")
		Input(c, "in", &text, InputOpts{Label: "New name", SelectOnFocus: true, Help: "test/input"})
	})
	h.u.Focus(id)
	h.frame()
	h.frame()
	h.keys("s", "a", "l")
	if text != "sal" {
		t.Errorf("typing over the selected name gave %q", text)
	}
}

// A sparkline's bars stand on the same baseline, as tall as their values.
func TestSpark(t *testing.T) {
	var id ui.ID
	red := render.Color{R: 255, A: 255}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("s")
		Spark(c, "s", []SparkBar{{H: 1, Tone: red}, {H: .5, Tone: red}, {H: 0, Tone: red}}, "3 runs")
	})
	h.frame()
	a, b, z := h.rect(ui.Child(id, 0)), h.rect(ui.Child(id, 1)), h.rect(ui.Child(id, 2))
	if a.Dy() != 12 || b.Dy() != 6 || z.Dy() != 2 || a.Max.Y != b.Max.Y || b.Max.Y != z.Max.Y {
		t.Errorf("bars %v %v %v", a, b, z)
	}
}

// The wheel scrolls a list away from its selection; the list only brings the selection back into view when it
// moves.
func TestListWheelScrollsAwayFromTheSelection(t *testing.T) {
	sel := 0
	var id ui.ID
	var built []int
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		built = built[:0]
		id = c.Next("list")
		List(c, "list", 1000, &sel, ListOpts{Height: ui.Px(220), Help: "test/list"}, func(c *ui.Ctx, i int, selected bool) {
			built = append(built, i)
			c.Text("t", "row", ui.TextStyle{})
		})
	})
	h.frame()
	r := h.rect(id)
	h.click(r.Min.X+20, r.Min.Y+5) // row 0: selected and focused
	x, y := mid(r)
	h.pointer(render.PointerMove, 0, x, y)
	h.frame()
	h.u.Pointer(render.PointerEvent{Kind: render.PointerScroll, Pos: render.Point{X: x, Y: y}, Scroll: render.Point{Y: 44 * 22}})
	h.frame()
	h.frame()
	if slices.Contains(built, 0) || !slices.Contains(built, 44) {
		t.Fatalf("after scrolling 44 rows the list builds %v", built)
	}
	h.keys("j")
	h.frame()
	if sel != 1 || !slices.Contains(built, 1) {
		t.Errorf("j moved the selection to %d but it isn't in view: %v", sel, built)
	}
}

// A toolbar fits its width: chips that don't fit go behind "…", whose menu toggles them the same.
func TestToolbarOverflows(t *testing.T) {
	a, b, cc, d := true, false, true, false
	width := float32(400)
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Row("box", ui.Style{W: ui.Px(width)}, func(c *ui.Ctx) {
			Toolbar(c, "bar", []ToolItem{{Key: "a", Label: "status", On: &a}, {Key: "b", Label: "± lines", On: &b},
				{Key: "c", Label: "diag", On: &cc}, {Key: "d", Label: "⌥C changed-only", On: &d}}, ToolbarOpts{Help: "test/toolbar", Label: "Columns"})
		})
	})
	h.frame()
	text := func() string { return strings.Join(h.texts(), "\n") }
	if got := text(); !strings.Contains(got, "changed-only") || strings.Contains(got, "…") {
		t.Fatalf("at 400 wide everything fits:\n%s", got)
	}
	width = 150
	h.frame()
	h.frame()
	got := text()
	if strings.Contains(got, "changed-only") || !strings.Contains(got, "…") || !strings.Contains(got, "status") {
		t.Fatalf("at 150 wide the last chips go behind …:\n%s", got)
	}
	more := h.find("More")
	if more == nil {
		t.Fatal("no More button")
	}
	h.click(mid(more.Bounds))
	if got := text(); !strings.Contains(got, "changed-only") {
		t.Fatalf("the menu doesn't list what's hidden:\n%s", got)
	}
	item := h.find("⌥C changed-only")
	if item == nil {
		t.Fatalf("no menu item for changed-only:\n%s", text())
	}
	h.click(mid(item.Bounds))
	if !d {
		t.Error("choosing changed-only in the menu didn't toggle it")
	}
}

// find is the accessible node named name, or nil.
func (h *harness) find(name string) *render.AccessNode {
	var walk func(n *render.AccessNode) *render.AccessNode
	walk = func(n *render.AccessNode) *render.AccessNode {
		if n == nil || n.Name == name {
			return n
		}
		for _, k := range n.Children {
			if f := walk(k); f != nil {
				return f
			}
		}
		return nil
	}
	return walk(h.u.AccessTree())
}

// Choices: a click picks a card and says so once; with the strip focused, l and h move; each card is a tab with its
// title as its name, its description wrapped under it.
func TestChoices(t *testing.T) {
	items := []Choice{{Title: "Plan", Desc: "docs first, Start work later"}, {Title: "Work", Desc: "straight to the worktree"}, {Title: "Tour"}}
	active, changes := 0, 0
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("modes")
		if Choices(c, "modes", items, &active, ChoicesOpts{Help: "test/choices", Label: "Mode"}) {
			changes++
		}
	})
	x, y := mid(h.rect(ui.Child(id, 1)))
	h.click(x, y)
	h.click(x, y)
	if active != 1 || changes != 1 {
		t.Errorf("two clicks on the second: active %d, %d changes; want 1 and 1", active, changes)
	}
	h.keys("l", "l", "h")
	if active != 1 {
		t.Errorf("l l h from 1: %d, want 1 (l stops at the end)", active)
	}
	if n := h.access(ui.Child(id, 1)); n == nil || n.Name != "Work" || !n.Selected {
		t.Errorf("the second card's node = %+v", n)
	}
	if !h.drew("straight to the worktree") {
		t.Errorf("no description: %q", h.texts())
	}
}

// Rich text wraps across its spans at word boundaries, never wider than its width; a code span is one piece in
// mono on its own tint, and bold text is its own run.
func TestRichWraps(t *testing.T) {
	var id ui.ID
	var lines int
	spans := []Span{{Text: "Ties go to even, but what about negative values? "}, {Text: "-2.5", Code: true},
		{Text: " should give "}, {Text: "-2", Code: true}, {Text: ", and "}, {Text: "Floor", Bold: true}, {Text: " goes the wrong way."}}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("rich")
		lines = Rich(c, "rich", spans, 220, uiText(c, 12.5, typeset.Normal, col(c, tInputFg)), RichOpts{})
	})
	h.frame()
	if lines < 2 {
		t.Fatalf("%d lines at 220px", lines)
	}
	if r := h.rect(id); r.Dx() > 220 {
		t.Errorf("rich text is %v wide, over its 220", r.Dx())
	}
	for _, want := range []string{"-2.5", "-2", "Floor"} {
		if !h.drew(want) {
			t.Errorf("%q isn't a run of its own: %q", want, h.texts())
		}
	}
	mono := 0
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && (op.Run.Text == "-2.5" || op.Run.Text == "-2") && op.Run.Font.Family == typeset.Mono {
			mono++
		}
	}
	if mono != 2 {
		t.Errorf("%d code spans in mono, want 2", mono)
	}
	for _, txt := range h.texts() {
		if strings.HasPrefix(txt, " ") || strings.HasSuffix(txt, " ") && txt != " " {
			continue
		}
		if strings.Contains(txt, "valu") && !strings.Contains(txt, "values?") {
			t.Errorf("a word was split: %q", txt)
		}
	}
}

// A struck span is its own run, in the strike colour, with a line through its middle as wide as it is: what an
// edit took out of a paragraph.
func TestRichStrikesThrough(t *testing.T) {
	spans := []Span{{Text: "Currency precision comes from "}, {Text: "a hard-coded table", Strike: true}, {Text: " currency.go"}}
	var strike render.Color
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		strike = col(c, tStrikeFg)
		Rich(c, "rich", spans, 500, uiText(c, 13, typeset.Normal, col(c, tInputFg)), RichOpts{})
	})
	h.frame()
	var run render.Op
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "a hard-coded table" {
			run = op
		}
	}
	if run.Kind != render.OpGlyphs {
		t.Fatalf("the struck words aren't a run of their own: %q", h.texts())
	}
	if run.Color != strike {
		t.Errorf("struck text is %v, want %v", run.Color, strike)
	}
	found := false
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && op.Rect.Dy() <= 1.5 && op.Style.Fill == strike && op.Rect.Dx() >= run.Run.Advance-1 &&
			op.Rect.Min.Y < run.At.Y && op.Rect.Min.Y > run.At.Y-run.Run.Ascent {
			found = true
		}
	}
	if !found {
		t.Error("no line through the struck run")
	}
}

// A code span with a tone is in that colour on a tint of it: a repo named in a prompt, in its project colour.
func TestRichTonedCode(t *testing.T) {
	tone := render.Color{R: 0x9b, G: 0x7b, B: 0xff, A: 0xff}
	spans := []Span{{Text: "In "}, {Text: "api", Code: true, Tone: tone}, {Text: " and "}, {Text: "web", Code: true}}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		Rich(c, "rich", spans, 500, uiText(c, 13, typeset.Normal, col(c, tInputFg)), RichOpts{})
	})
	h.frame()
	var api, web render.Op
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "api" {
			api = op
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == "web" {
			web = op
		}
	}
	if api.Color != tone || web.Color == tone {
		t.Errorf("api is %v, web %v; want api in %v", api.Color, web.Color, tone)
	}
	tinted := false
	for _, op := range h.list.Ops {
		if op.Kind == render.OpRect && op.Style.Fill == alpha(tone, .14) && op.Rect.Min.X <= api.At.X && op.Rect.Max.X >= api.At.X+api.Run.Advance {
			tinted = true
		}
	}
	if !tinted {
		t.Error("no tint of its tone behind the toned span")
	}
}

// ED-15: a hover card shows while the pointer is on its anchor, stays while the pointer moves into it to read it, and
// goes when the pointer leaves both; it never takes focus, so keys still go where they went.
func TestHoverCardFollowsThePointer(t *testing.T) {
	var anchor, field ui.ID
	text := ""
	var shown bool
	h := newHarness(t, theme.Dark, render.Point{X: 600, Y: 400}, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Pad(20), Gap: 20}, func(c *ui.Ctx) {
			field = c.Next("field")
			Input(c, "field", &text, InputOpts{Label: "Field", Help: "test/hover"})
			anchor = c.Next("sign")
			Clickable(c, "sign", ui.Style{W: ui.Px(16), H: ui.Px(16), Label: "2 threads", Help: "test/hover"}, nil)
			shown = HoverCard(c, "card", anchor, HoverCardOpts{Label: "Thread", Help: "test/hover", Width: 240}, func(c *ui.Ctx) {
				c.Text("t", "the card's text", ui.TextStyle{})
			})
		})
	})
	h.u.Focus(field)
	h.frame()
	if shown || h.drew("the card's text") {
		t.Fatal("the card shows before the pointer is on its anchor")
	}
	ax, ay := mid(h.rect(anchor))
	h.pointer(render.PointerMove, 0, ax, ay)
	h.frame()
	h.frame()
	if !shown || !h.drew("the card's text") {
		t.Fatal("no card with the pointer on its anchor")
	}
	card := h.find("Thread")
	if card == nil {
		t.Fatal("the card has no accessible name")
	}
	a := h.rect(anchor)
	for _, p := range []render.Point{{X: (a.Max.X + card.Bounds.Min.X) / 2, Y: (a.Min.Y + a.Max.Y) / 2}, {X: card.Bounds.Min.X + 30, Y: card.Bounds.Min.Y + 12},
		{X: card.Bounds.Max.X - 10, Y: card.Bounds.Max.Y - 4}} {
		h.pointer(render.PointerMove, 0, p.X, p.Y)
		h.frame()
		h.frame()
		if !shown {
			t.Fatalf("the card went with the pointer at %v, on the way into it or in it", p)
		}
	}
	if h.u.Focused() != field {
		t.Error("the card took focus")
	}
	h.keys("x")
	if text != "x" {
		t.Errorf("a key with the card up went elsewhere: the field has %q", text)
	}
	h.pointer(render.PointerMove, 0, 580, 380)
	h.frame()
	h.frame()
	if shown || h.drew("the card's text") {
		t.Error("the card stays with the pointer gone")
	}
}

// SE-5: "widget.Input gains Lines: wraps, grows to a max height then scrolls, ⏎ a new line, ⌘⏎ submits".
func TestInputLines(t *testing.T) {
	text := ""
	var id ui.ID
	var res InputResult
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(300)}, func(c *ui.Ctx) {
			id = c.Next("p")
			res = Input(c, "p", &text, InputOpts{Label: "Prompt", Lines: 4, Help: "test/input"})
		})
	})
	one := h.rect(id).Dy()
	h.click(mid(h.rect(id)))
	if h.u.Focused() != id {
		t.Fatal("a click didn't focus the input")
	}
	h.keys("a", "b", "c", "CR")
	if res.Submitted || text != "abc\n" {
		t.Fatalf("⏎ in a multi-line input: text %q, submitted %v; want a new line", text, res.Submitted)
	}
	h.keys("a", "b", "c")
	two := h.rect(id).Dy()
	if two <= one {
		t.Errorf("two lines are %v high, one %v: it should grow", two, one)
	}
	h.keys("Up", "X")
	if text != "abcX\nabc" {
		t.Errorf("Up from the end of the second line, then X: %q", text)
	}
	h.keys("Down", "Y")
	if text != "abcX\nabcY" {
		t.Errorf("Down, then Y: %q", text)
	}
	if !h.u.Key(render.KeyEvent{Key: "CR", Mods: platform.primary}) {
		t.Fatal("nothing takes ⌘⏎")
	}
	h.frame()
	if !res.Submitted || text != "abcX\nabcY" {
		t.Errorf("⌘⏎: submitted %v, text %q", res.Submitted, text)
	}
	for range 6 {
		h.keys("CR", "z")
	}
	if most := h.rect(id).Dy(); most <= two || most > one+3*(two-one)+1 {
		t.Errorf("eight lines are %v high: it stops growing at four lines (one %v, two %v)", most, one, two)
	}
	if n := h.access(id); n == nil || n.Value != text {
		t.Errorf("its accessible value isn't the text: %+v", n)
	}
	// A long line wraps at the field's width rather than running on.
	text = strings.Repeat("word ", 30)
	h.frame()
	h.frame()
	if got := h.rect(id).Dy(); got <= two {
		t.Errorf("a line of 150 characters in a 280px field is %v high: it should wrap", got)
	}
}

// FG-6: a plain checkbox ticks and unticks on a click on it or its text, and with Space while focused.
func TestCheckPlain(t *testing.T) {
	on := true
	var id ui.ID
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("cb")
		st := CheckOff
		if on {
			st = CheckOn
		}
		if Check(c, "cb", st, CheckOpts{Text: "handler.go", Plain: true, Help: "test/checkbox"}) {
			on = !on
		}
	})
	r := h.rect(id)
	h.click(r.Max.X-4, (r.Min.Y+r.Max.Y)/2)
	if on {
		t.Fatal("a click on the text didn't untick it")
	}
	h.frame()
	if n := h.access(id); n == nil || n.Name != "handler.go" || n.Selected {
		t.Errorf("its node: %+v", n)
	}
	h.keys(" ")
	if !on {
		t.Error("Space didn't tick it")
	}
}
