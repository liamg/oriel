package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

type memClip struct{ text string }

func (c *memClip) SetClipboard(s string)         { c.text = s }
func (c *memClip) ReadClipboard() (string, bool) { return c.text, c.text != "" }

// selScene is a Selectable column of two lines, the second of two text nodes with a gap between, and a button under
// them; the headless shaper is fixed-pitch, 6 wide a character at size 10.
func selScene(t *testing.T) (*UI, *memClip, func(), *selClicks) {
	t.Helper()
	u := newUI()
	st, err := theme.Stack(theme.Builtins(), "oriel")
	if err != nil {
		t.Fatal(err)
	}
	th, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		t.Fatal(err)
	}
	u.SetTheme(th)
	clip := &memClip{}
	u.SetClipboard(clip)
	clicked := &selClicks{}
	frame := func() {
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Column("page", Style{W: Px(300), Selectable: true}, func(c *Ctx) {
				c.Text("l1", "round half even", body)
				c.Row("l2", Style{Gap: 6}, func(c *Ctx) {
					c.Text("a", "second", body)
					c.Text("b", "line", body)
				})
				if c.Interaction(c.Box("button", Style{W: Px(60), H: Px(20), Interactive: true}, nil)).Clicked {
					clicked.button = true
				}
				// A focusable control, as widget.Button is, with a label of its own.
				fb := c.Next("focusable")
				if c.Interaction(fb).Clicked {
					clicked.focusable = true
				}
				c.Row("focusable", Style{W: Px(60), H: Px(20), Focusable: true, KeyMode: "button"}, func(c *Ctx) { c.Text("t", "Hide", body) })
			})
		})
	}
	frame()
	return u, clip, frame, clicked
}

// selClicks are which of selScene's controls got their click: the button, the focusable control.
type selClicks struct{ button, focusable bool }

func press(u *UI, frame func(), p render.Point, mods render.Mods) {
	u.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary, Mods: mods})
	frame()
}

func release(u *UI, frame func(), p render.Point) {
	u.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary})
	frame()
}

// Text in a Selectable box selects with a drag across nodes and lines, a double click selects a word, and ⌘C or ⌃⇧C
// copies; a press on a control in it is the control's, and a press elsewhere ends the selection.
func TestSelectableText(t *testing.T) {
	u, clip, frame, clicked := selScene(t)
	// "half" starts at x 36 on the first line (y 0–14); "line" sits after "second" and the gap, at x 42 on the second.
	press(u, frame, render.Point{X: 37, Y: 7}, 0)
	u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 66, Y: 21}, Buttons: render.ButtonPrimary})
	frame()
	release(u, frame, render.Point{X: 66, Y: 21})
	if got, want := u.SelectedText(), "half even\nsecond line"; got != want {
		t.Errorf("a drag selected %q, want %q", got, want)
	}
	if !u.Key(render.KeyEvent{Key: "c", Mods: render.ModSuper}) || clip.text != "half even\nsecond line" {
		t.Errorf("⌘C copied %q", clip.text)
	}

	at := render.Point{X: 50, Y: 7}
	press(u, frame, at, 0)
	release(u, frame, at)
	press(u, frame, at, 0)
	release(u, frame, at)
	if got := u.SelectedText(); got != "half" {
		t.Errorf("a double click selected %q", got)
	}
	if !u.Key(render.KeyEvent{Key: "C", Mods: render.ModCtrl | render.ModShift}) || clip.text != "half" {
		t.Errorf("⌃⇧C copied %q", clip.text)
	}
	press(u, frame, render.Point{X: at.X + 2, Y: at.Y + 1}, 0)
	release(u, frame, at)
	if got := u.SelectedText(); got != "round half even" {
		t.Errorf("a triple click, the pointer moved a little, selected %q", got)
	}
	for range 2 {
		press(u, frame, render.Point{X: 87, Y: 7}, 0)
		release(u, frame, render.Point{X: 87, Y: 7})
	}
	if got := u.SelectedText(); got != "even" {
		t.Errorf("a double click on another word selected %q", got)
	}

	button := render.Point{X: 10, Y: 38}
	press(u, frame, button, 0)
	release(u, frame, button)
	if !clicked.button {
		t.Error("the button in the selectable box didn't get its click")
	}
	if u.SelectedText() != "" {
		t.Error("a press on a control kept the selection")
	}
	if u.Key(render.KeyEvent{Key: "c", Mods: render.ModSuper}) {
		t.Error("⌘C was taken with nothing selected")
	}
	label := render.Point{X: 8, Y: 55}
	press(u, frame, label, 0)
	release(u, frame, label)
	press(u, frame, label, 0)
	release(u, frame, label)
	if !clicked.focusable || u.SelectedText() != "" {
		t.Errorf("a focusable control's label selected %q (clicked %v)", u.SelectedText(), clicked.focusable)
	}
}

// ⌘A selects all the text of the Selectable box last pressed in, but its controls' labels, and ⌃A does too; with
// nothing pressed in one, it's left to whatever has focus.
func TestSelectAll(t *testing.T) {
	u, _, frame, _ := selScene(t)
	if u.Key(render.KeyEvent{Key: "a", Mods: render.ModSuper}) {
		t.Fatal("⌘A was taken before any text was pressed")
	}
	at := render.Point{X: 20, Y: 7}
	press(u, frame, at, 0)
	release(u, frame, at)
	if !u.Key(render.KeyEvent{Key: "a", Mods: render.ModSuper}) {
		t.Fatal("⌘A wasn't taken")
	}
	frame()
	if got, want := u.SelectedText(), "round half even\nsecond line"; got != want {
		t.Errorf("⌘A selected %q, want %q", got, want)
	}
}

// With no Selectable box, text selects by default, within the nearest box that scrolls or floats, else the window;
// never in a control, a NoSelect box or a popover.
func TestTextSelectsByDefault(t *testing.T) {
	u := newUI()
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	th, _ := theme.Resolve(st, theme.Dark)
	u.SetTheme(th)
	clip := &memClip{}
	u.SetClipboard(clip)
	frame := func() {
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Column("page", Style{W: Px(300)}, func(c *Ctx) {
				c.Column("log", Style{W: Px(300), H: Px(40), ScrollY: true}, func(c *Ctx) {
					c.Text("l1", "first message", body)
					c.Text("l2", "second message", body)
				})
				c.Text("outside", "under the log", body)
				c.Box("quiet", Style{NoSelect: true}, func(c *Ctx) { c.Text("t", "no select", body) })
				c.Box("tip", Style{Z: LayerPopup, Float: &Float{Point: &render.Point{X: 200, Y: 200}}}, func(c *Ctx) { c.Text("t", "a popover", body) })
			})
		})
	}
	frame()
	drag := func(from, to render.Point) string {
		press(u, frame, from, 0)
		u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: to})
		frame()
		release(u, frame, to)
		clip.text = ""
		u.Key(render.KeyEvent{Key: "c", Mods: render.ModSuper})
		return clip.text
	}
	if got := drag(render.Point{X: 0, Y: 7}, render.Point{X: 290, Y: 70}); got != "first message\nsecond message" {
		t.Errorf("a drag down the log copied %q, want the log's text and no more", got)
	}
	if got := drag(render.Point{X: 0, Y: 47}, render.Point{X: 54, Y: 47}); got != "under the" {
		t.Errorf("a drag on the window's own text copied %q", got)
	}
	if got := drag(render.Point{X: 0, Y: 67}, render.Point{X: 50, Y: 67}); got != "" {
		t.Errorf("a drag on NoSelect text copied %q", got)
	}
	if got := drag(render.Point{X: 201, Y: 207}, render.Point{X: 250, Y: 207}); got != "" {
		t.Errorf("a drag on a popover copied %q", got)
	}
}

// A drag held past the bottom of a scrolling box scrolls it, and the selection follows the pointer down.
func TestSelectionScrollsItsBox(t *testing.T) {
	u := newUI()
	frame := func() {
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Column("box", Style{W: Px(300), H: Px(70), ScrollY: true, Selectable: true}, func(c *Ctx) {
				for i := range 40 {
					c.Text(i, fmt.Sprintf("line %02d", i), body)
				}
			})
		})
	}
	frame()
	press(u, frame, render.Point{X: 2, Y: 7}, 0)
	u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 2, Y: 120}, Buttons: render.ButtonPrimary})
	for range 10 {
		frame()
	}
	if u.Scroll(u.byIDFor("box")).Y == 0 {
		t.Fatal("the box didn't scroll under the drag")
	}
	got := u.SelectedText()
	if !strings.HasPrefix(got, "line 00") || strings.Count(got, "\n") < 6 {
		t.Errorf("the selection didn't follow the scroll: %q", got)
	}
}

// byIDFor is the id of the root's child keyed key.
func (u *UI) byIDFor(key string) ID { return childID(0, key) }

// A line that wraps on from the one before (WrappedText) copies joined to it by its join, not a newline.
func TestWrappedTextCopiesJoined(t *testing.T) {
	u := newUI()
	frame := func() {
		u.Frame(new(render.List).Record(1), render.Point{X: 400, Y: 300}, func(c *Ctx) {
			c.Column("page", Style{W: Px(300), Selectable: true}, func(c *Ctx) {
				c.Text("a", "round half", body)
				c.WrappedText("b", "even", body, " ")
				c.WrappedText("c", "-up", body, "")
				c.Text("d", "next", body)
			})
		})
	}
	frame()
	press(u, frame, render.Point{X: 1, Y: 2}, 0)
	u.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 290, Y: 290}, Buttons: render.ButtonPrimary})
	frame()
	if got, want := u.SelectedText(), "round half even-up\nnext"; got != want {
		t.Errorf("copied %q, want %q", got, want)
	}
}

// Clicks count by when they happened, not by when a busy loop got to them: a triple click whose presses are taken
// a second apart, on a machine slow to draw, still selects the line; and presses that really were far apart don't.
func TestATripleClickTakenLateStillSelectsTheLine(t *testing.T) {
	u, _, frame, _ := selScene(t)
	clk := &clock{t: time.Unix(1000, 0)}
	u.SetClock(clk.now)
	at, when := render.Point{X: 50, Y: 7}, time.Unix(1000, 0)
	for i := range 3 {
		ev := render.PointerEvent{Pos: at, Buttons: render.ButtonPrimary, At: when.Add(time.Duration(i) * 50 * time.Millisecond)}
		ev.Kind = render.PointerPress
		u.Pointer(ev)
		frame()
		clk.advance(time.Second) // the loop was busy between this press and its release
		ev.Kind = render.PointerRelease
		u.Pointer(ev)
		frame()
	}
	if got := u.SelectedText(); got != "round half even" {
		t.Errorf("a triple click taken late selected %q, want the line", got)
	}
	for i := range 3 {
		ev := render.PointerEvent{Pos: at, Buttons: render.ButtonPrimary, At: when.Add(10*time.Second + time.Duration(i)*time.Second)}
		ev.Kind = render.PointerPress
		u.Pointer(ev)
		frame()
		ev.Kind = render.PointerRelease
		u.Pointer(ev)
		frame()
	}
	if got := u.SelectedText(); got != "" {
		t.Errorf("three clicks a second apart selected %q, want nothing", got)
	}
}
