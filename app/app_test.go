package app_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/tools/perf"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// counter is a plugin built only on the public API: a command, a keymap and a view. Its view fills the
// window, or with tab set, opens as a tab in the layout.
type counter struct {
	n   int
	tab bool
}

func (c *counter) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.counter"} }

func (c *counter) Activate(_ context.Context, h plugin.Host) error {
	h.Command(registry.Command{ID: "counter.add", Title: "Add one", Run: func(_ registry.Ctx, args registry.Args) error {
		if n, ok := args["n"].(int); ok {
			c.n += n
		} else {
			c.n++
		}
		return nil
	}})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "x", Command: "counter.add"})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "g", Command: "counter.add"})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "gx", Command: "counter.add", Args: registry.Args{"n": 10}})
	if err := h.Options().Set("keymap.timeoutlen", registry.LayerUser, registry.At{}, 50*time.Millisecond); err != nil {
		return err
	}
	view := kit.RootView
	if c.tab {
		view = "counter"
	}
	h.View(view, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		rows := []ui.Node{ui.TextNode(fmt.Sprintf("count %d", c.n))}
		for _, s := range h.Sessions() {
			rows = append(rows, ui.TextNode(fmt.Sprintf("%s %s", s.ID(), s.Status())))
		}
		return ui.ColumnNode(rows...)
	}))
	if c.tab {
		return h.Run(context.Background(), "view.open", registry.Args{"id": view})
	}
	return nil
}

func (c *counter) Deactivate(context.Context) error { return nil }

type booted struct {
	win   *headless.Window
	store core.Store
	done  chan error
}

func boot(t *testing.T, plugins ...plugin.Plugin) *booted {
	t.Helper()
	return bootIn(t, t.TempDir(), plugins...)
}

func bootIn(t *testing.T, configDir string, plugins ...plugin.Plugin) *booted {
	t.Helper()
	isolate.Test(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	b := &booted{store: eventstore.New(), done: make(chan error, 1)}
	go func() {
		// A directory is open: the start view keeps to its own keys (TestStartViewKeepsToItsOwnKeys).
		b.done <- app.Run(ctx, be, app.Config{Store: b.store, Plugins: plugins, ConfigDir: configDir, StateDir: t.TempDir(), WorkDir: t.TempDir()})
	}()
	select {
	case b.win = <-be.Windows():
	case err := <-b.done:
		t.Fatalf("app exited before opening a window: %v", err)
	case <-ctx.Done():
		t.Fatal("no window opened")
	}
	return b
}

func (b *booted) waitText(t *testing.T, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), want) })
	if err != nil {
		t.Fatalf("no frame shows %q; last frame:\n%s", want, f.Text())
	}
}

func (b *booted) close(t *testing.T) {
	t.Helper()
	b.win.Close()
	if err := apptest.Recv(t, "the app to exit after its window closed", b.done); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestEmptyAppBoots(t *testing.T) {
	b := boot(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.win.WaitFrame(ctx, func(headless.Frame) bool { return true }); err != nil {
		t.Fatalf("no frame drawn: %v", err)
	}
	b.close(t)
}

func TestKeymapRunsCommand(t *testing.T) {
	b := boot(t, &counter{})
	b.waitText(t, "count 0")
	b.win.Send(render.KeyEvent{Key: "x"})
	b.waitText(t, "count 1")
	b.win.Send(render.KeyEvent{Key: "x"})
	b.waitText(t, "count 2")
	b.close(t)
}

func TestEventUpdatesView(t *testing.T) {
	b := boot(t, &counter{})
	b.waitText(t, "count 0")
	if err := b.store.Apply(core.SessionOpened{ID: "s1", Kind: core.KindWork, Status: core.StatusQuiet}); err != nil {
		t.Fatal(err)
	}
	b.waitText(t, "s1 quiet")
	if err := b.store.Apply(core.SessionStatusChanged{ID: "s1", Status: core.StatusWorking}); err != nil {
		t.Fatal(err)
	}
	b.waitText(t, "s1 working")
	b.close(t)
}

func TestChordsWaitForTheirTimeout(t *testing.T) {
	b := boot(t, &counter{})
	b.waitText(t, "count 0")
	b.win.Send(render.KeyEvent{Key: "g"})
	b.waitText(t, "count 1") // g alone, once keymap.timeoutlen passes
	b.win.Send(render.KeyEvent{Key: "g"})
	b.win.Send(render.KeyEvent{Key: "x"})
	b.waitText(t, "count 11")
	b.close(t)
}

// lister binds keys to the built-in :Keys and :Commands, and says when its command runs.
type lister struct{ ran chan struct{} }

func (lister) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.lister"} }
func (l lister) Activate(_ context.Context, h plugin.Host) error {
	h.Command(registry.Command{ID: "lister.noop", Title: "Do nothing", Run: func(registry.Ctx, registry.Args) error {
		if l.ran != nil {
			l.ran <- struct{}{}
		}
		return nil
	}})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "<C-k>", Command: "Keys"})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "<C-c>", Command: "Commands"})
	return nil
}
func (lister) Deactivate(context.Context) error { return nil }

func (b *booted) waitLine(t *testing.T, parts ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool {
		for _, line := range strings.Split(f.Text(), "\n") {
			if containsAll(line, parts) {
				return true
			}
		}
		return false
	})
	if err != nil {
		t.Fatalf("no frame has a line with %q; last frame:\n%s", parts, f.Text())
	}
}

func containsAll(s string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

func (b *booted) typeText(s string) {
	for _, r := range s {
		b.win.Send(render.KeyEvent{Key: string(r)})
	}
}

// :Keys and :Commands are pickers: they list with sources, filter, and run on Enter.
func TestKeysAndCommandsArePickers(t *testing.T) {
	l := lister{ran: make(chan struct{}, 1)}
	b := boot(t, l)
	b.win.Send(render.KeyEvent{Key: "k", Mods: render.ModCtrl})
	b.typeText("Keys <C-k>")
	b.waitLine(t, "Keys", "<C-k>")
	b.waitLine(t, "test.lister")
	b.win.Send(render.KeyEvent{Key: "Esc"})
	b.win.Send(render.KeyEvent{Key: "c", Mods: render.ModCtrl})
	b.typeText("do nothing")
	b.waitText(t, "Do nothing")
	b.waitText(t, "lister.noop")
	b.win.Send(render.KeyEvent{Key: "CR"})
	apptest.Recv(t, "Enter in :Commands to run the command", l.ran)
	b.close(t)
}

// A view in the layout closes from the keyboard: q in a view with no keys of its own, and ⌃W c.
func TestViewsCloseFromTheKeyboard(t *testing.T) {
	b := boot(t, &counter{tab: true}, opener{})
	b.waitText(t, "count 0")
	b.win.Send(render.KeyEvent{Key: "q"})
	b.waitGone(t, "count 0")
	b.win.Send(render.KeyEvent{Key: "F2"})
	b.waitText(t, "count 0")
	b.win.Send(render.KeyEvent{Key: "w", Mods: render.ModCtrl})
	b.win.Send(render.KeyEvent{Key: "c"})
	b.waitGone(t, "count 0")
	b.close(t)
}

// opener opens the counter's view on F2.
type opener struct{}

func (opener) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.opener"} }
func (opener) Activate(_ context.Context, h plugin.Host) error {
	h.Keymap(registry.Keymap{Mode: "global", Keys: "<F2>", Command: "view.open", Args: registry.Args{"id": "counter"}})
	return nil
}
func (opener) Deactivate(context.Context) error { return nil }

func (b *booted) waitGone(t *testing.T, s string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return !strings.Contains(f.Text(), s) })
	if err != nil {
		t.Fatalf("%q never went; last frame:\n%s", s, f.Text())
	}
}

// pointy is a plugin view with a box that asks for the pointer cursor.
type pointy struct{}

func (pointy) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.pointy"} }
func (pointy) Activate(_ context.Context, h plugin.Host) error {
	h.View(kit.RootView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Box{Key: "link", Style: ui.Style{W: ui.Px(100), H: ui.Px(20), Cursor: render.CursorPointer}}
	}))
	return nil
}
func (pointy) Deactivate(context.Context) error { return nil }

func TestPointerSetsTheCursor(t *testing.T) {
	b := boot(t, pointy{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.win.WaitFrame(ctx, func(headless.Frame) bool { return true })
	// The root view sits under the 38px titlebar, right of the 44px rail.
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 74, Y: 48}})
	apptest.Poll(t, "the pointer cursor over the link", func() bool { return b.win.Cursor() == render.CursorPointer })
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 300}})
	apptest.Poll(t, "the default cursor off the link", func() bool { return b.win.Cursor() == render.CursorDefault })
	b.close(t)
}

// themePicker sets ui.theme, as a user's settings would.
type themePicker string

func (p themePicker) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.theme"} }
func (p themePicker) Activate(_ context.Context, h plugin.Host) error {
	return h.Options().Set("ui.theme", registry.LayerUser, registry.At{}, string(p))
}
func (themePicker) Deactivate(context.Context) error { return nil }

func background(f headless.Frame) render.Color {
	if len(f.Ops) == 0 || f.Ops[0].Kind != render.OpRect {
		return render.Color{}
	}
	return f.Ops[0].Style.Fill
}

func TestThemeFileReloadsLive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "themes", "mine.toml")
	os.MkdirAll(filepath.Dir(path), 0o755)
	write := func(bg string) {
		src := "name = \"mine\"\nextends = \"oriel\"\n[semantic]\n\"bg.base\" = \"" + bg + "\"\n"
		if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("#102030")
	b := bootIn(t, dir, themePicker("mine"))
	wait := func(want render.Color) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return background(f) == want }); err != nil {
			t.Fatalf("background %v, want %v", background(f), want)
		}
	}
	wait(render.Color{R: 0x10, G: 0x20, B: 0x30, A: 255})
	write("#405060")
	wait(render.Color{R: 0x40, G: 0x50, B: 0x60, A: 255})
	b.close(t)
}

// ui.variant "system", the default, follows the OS's preference as the window reports it; dark until it does.
func TestVariantFollowsTheOS(t *testing.T) {
	b := boot(t)
	wait := func(want render.Color) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return background(f) == want }); err != nil {
			t.Fatalf("background %v, want %v", background(f), want)
		}
	}
	dark, light := render.Color{R: 6, G: 6, B: 6, A: 255}, render.Color{R: 0xfb, G: 0xfa, B: 0xf7, A: 255}
	wait(dark)
	b.win.Send(render.AppearanceEvent{Dark: false})
	wait(light)
	b.win.Send(render.AppearanceEvent{Dark: true})
	wait(dark)
	b.close(t)
}

func TestBrokenThemeFallsBackToTheBuiltIn(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "themes"), 0o755)
	os.WriteFile(filepath.Join(dir, "themes", "bad.toml"), []byte("name = \"bad\"\n[semantic]\naccent = \"nope\"\n"), 0o644)
	b := bootIn(t, dir, themePicker("bad"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return background(f) == render.Color{R: 6, G: 6, B: 6, A: 255} }); err != nil {
		t.Fatalf("background %v; want the built-in bg.base", background(f))
	}
	b.close(t)
}

// form is a native plugin whose view uses widgets: an input, and a global chord that works while it's focused.
type form struct {
	text  string
	saved int
}

func (*form) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.form"} }
func (f *form) Activate(_ context.Context, h plugin.Host) error {
	h.Command(registry.Command{ID: "form.save", Run: func(registry.Ctx, registry.Args) error { f.saved++; return nil }})
	h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-s>", Command: "form.save"})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "h", Command: "form.save"}) // vim's h mustn't fire inside the input
	h.View(kit.RootView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "form", Fn: func(c *ui.Ctx) {
			widget.Input(c, "name", &f.text, widget.InputOpts{Width: ui.Px(300), Help: "test/form"})
			c.Text("echo", "typed:"+f.text, ui.TextStyle{})
			c.Text("saved", fmt.Sprintf("saved:%d", f.saved), ui.TextStyle{})
		}}
	}))
	return nil
}
func (*form) Deactivate(context.Context) error { return nil }

func TestWidgetsTakeKeysInTheirOwnMode(t *testing.T) {
	b := boot(t, &form{})
	b.waitText(t, "typed:")
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 84, Y: 50}})
	b.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 84, Y: 50}, Buttons: render.ButtonPrimary})
	b.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: 84, Y: 50}, Buttons: render.ButtonPrimary})
	b.waitText(t, "typed:")
	for _, k := range []string{"h", "i"} {
		b.win.Send(render.KeyEvent{Key: k})
	}
	b.waitText(t, "typed:hi")
	b.win.Send(render.KeyEvent{Key: "s", Mods: render.ModSuper})
	b.waitText(t, "saved:1")
	b.close(t)
}

// panes is a plugin with two views and keys that put them on screen through the layout's commands.
type panes struct{}

func (panes) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.panes"} }
func (panes) Activate(_ context.Context, h plugin.Host) error {
	for _, name := range []string{"alpha", "beta"} {
		h.View(name, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("content of " + name) }))
	}
	h.Keymap(registry.Keymap{Mode: "n", Keys: "a", Command: "view.open", Args: registry.Args{"id": "alpha"}})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "b", Command: "view.open", Args: registry.Args{"id": "beta", "place": "split", "side": "right"}})
	return nil
}
func (panes) Deactivate(context.Context) error { return nil }

func TestLayoutCommandsAndKeys(t *testing.T) {
	b := boot(t, panes{})
	b.win.Send(render.KeyEvent{Key: "a"})
	b.waitText(t, "content of alpha")
	b.win.Send(render.KeyEvent{Key: "b"})
	b.waitText(t, "content of beta")
	b.waitLine(t, "content of alpha")
	// ⌃W Z zooms beta, the focused view: alpha goes.
	b.win.Send(render.KeyEvent{Key: "w", Mods: render.ModCtrl})
	b.win.Send(render.KeyEvent{Key: "Z", Mods: render.ModShift})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool {
		return strings.Contains(f.Text(), "content of beta") && !strings.Contains(f.Text(), "content of alpha")
	}); err != nil {
		t.Fatalf("after ⌃W Z:\n%s", f.Text())
	}
	seen := len(b.win.Frames())
	b.win.Send(render.KeyEvent{Key: "w", Mods: render.ModCtrl})
	b.win.Send(render.KeyEvent{Key: "Z", Mods: render.ModShift})
	n := 0
	if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool {
		n++
		return n > seen && strings.Contains(f.Text(), "content of alpha") && strings.Contains(f.Text(), "content of beta")
	}); err != nil {
		t.Fatalf("after ⌃W Z again, both should be back:\n%s", f.Text())
	}
	b.close(t)
}

// The window's accessibility tree: the app's main regions, as a screen reader meets them, and a reader's
// click running a widget's action.
func TestAccessTree(t *testing.T) {
	b := boot(t, panes{})
	b.win.Send(render.KeyEvent{Key: "a"})
	b.waitText(t, "content of alpha")
	var dump string
	apptest.Poll(t, "alpha in the accessibility tree", func() bool {
		if tr := b.win.Access(); tr != nil {
			dump = tr.Dump()
		}
		return strings.Contains(dump, `group "alpha"`)
	})
	for _, want := range []string{`window "Oriel"`, `region "Main"`, `tablist`, `tab "alpha" selected`, `group "alpha"`} {
		if !strings.Contains(dump, want) {
			t.Errorf("no %s in the tree:\n%s", want, dump)
		}
	}
	b.close(t)
}

// A hand edit applies while Oriel runs, as a GUI change does.
func TestSettingsFileReloadsLive(t *testing.T) {
	dir := t.TempDir()
	b := bootIn(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return background(f) == render.Color{R: 6, G: 6, B: 6, A: 255} }); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "settings.toml"), []byte("[ui]\nvariant = \"light\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if f, err := b.win.WaitFrame(ctx, func(f headless.Frame) bool { return background(f) == render.Color{R: 0xfb, G: 0xfa, B: 0xf7, A: 255} }); err != nil {
		t.Fatalf("background %v after the file said light", background(f))
	}
	b.close(t)
}

// Moving the pointer over nothing that reacts to it builds no frames. Reduced motion keeps what loops on its own
// (Ori's blink on the rail) from waking frames of its own while it's counted.
func TestPointerMovesOverNothingDrawNothing(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "init.lua"), []byte(`oriel.option["ui.reduced_motion"] = true`), 0o644); err != nil {
		t.Fatal(err)
	}
	b := bootIn(t, dir, pointy{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	b.win.WaitFrame(ctx, func(headless.Frame) bool { return true })
	settled := func() int {
		n := len(b.win.Frames())
		for {
			time.Sleep(200 * time.Millisecond)
			if m := len(b.win.Frames()); m == n || ctx.Err() != nil {
				return m
			}
			n = len(b.win.Frames())
		}
	}
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 300}})
	before := settled()
	for i := range 100 {
		b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300 + float32(i), Y: 300}})
	}
	if after := settled(); after != before {
		t.Errorf("100 moves over an empty view built %d frames", after-before)
	}
	b.close(t)
}

// menuView is a plugin view showing a menu at the root view's corner.
type menuView struct{}

func (menuView) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.menu"} }
func (menuView) Activate(_ context.Context, h plugin.Host) error {
	items := []widget.MenuItem{{Label: "Close"}, {Label: "Close Others"}, {Label: "Close to the Right"}, {Label: "Close All"}}
	h.View(kit.RootView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "menu", Fn: func(c *ui.Ctx) { widget.Menu(c, "menu", items, widget.MenuOpts{}) }}
	}))
	return nil
}
func (menuView) Deactivate(context.Context) error { return nil }

// fillAt is the colour of the last rect drawn under p that isn't fill, or zero.
func fillAt(f headless.Frame, p render.Point, not render.Color) render.Color {
	var got render.Color
	for _, op := range f.Ops {
		r := op.Rect
		if op.Kind == render.OpRect && op.Style.Fill.A > 0 && op.Style.Fill != not && p.X >= r.Min.X && p.X < r.Max.X && p.Y >= r.Min.Y && p.Y < r.Max.Y {
			got = op.Style.Fill
		}
	}
	return got
}

// Sweeping down a menu, each row's highlight eases in while the pointer goes on moving over it: moves that change
// nothing build no frame, but mustn't hold back the frames the easing needs.
func TestMenuHoverWhileThePointerMovesBudget(t *testing.T) {
	perf.Timed(t)
	b := boot(t, menuView{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b.win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), "Close All") })
	for n := len(b.win.Frames()); ; {
		time.Sleep(200 * time.Millisecond)
		if m := len(b.win.Frames()); m == n || ctx.Err() != nil {
			break
		} else {
			n = m
		}
	}
	// The root view sits under the 38px titlebar, right of the 44px rail; the menu's rows are 30px under 4px of pad.
	row := func(i int) render.Point { return render.Point{X: 44 + 100, Y: 38 + 4 + 15 + 30*float32(i)} }
	frames := b.win.Frames()
	last := frames[len(frames)-1]
	bg := fillAt(last, row(2), render.Color{})
	hover := fillAt(last, row(0), bg) // the first row starts highlighted
	if hover.A == 0 {
		t.Fatal("no highlight on the first row")
	}
	near := func(a, b render.Color) bool {
		d := func(x, y uint8) bool { return max(x, y)-min(x, y) <= 2 }
		return d(a.R, b.R) && d(a.G, b.G) && d(a.B, b.B) && d(a.A, b.A)
	}
	const stay, every = 400 * time.Millisecond, 2 * time.Millisecond
	for i := 1; i < 4; i++ {
		start := len(b.win.Frames())
		for j := range int(stay / every) {
			b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: row(i).X + float32(j%20), Y: row(i).Y}})
			time.Sleep(every)
		}
		moving := b.win.Frames()[start:]
		var got render.Color
		if len(moving) > 0 {
			got = fillAt(moving[len(moving)-1], row(i), bg)
		}
		if !near(got, hover) {
			t.Errorf("after %v of moves over row %d it's drawn %v in %d frames, not the highlight %v", stay, i, got, len(moving), hover)
		}
	}
	b.close(t)
}

// With no screen reader reading the window, no accessibility tree is built; once one starts, the tree it gets is
// the screen's as it is now.
func TestAccessTreeOnlyWhileAScreenReaderReads(t *testing.T) {
	b := boot(t, panes{})
	b.waitText(t, "ORIEL")
	b.win.SetScreenReader(false)
	b.win.Send(render.KeyEvent{Key: "a"})
	b.waitText(t, "content of alpha")
	time.Sleep(100 * time.Millisecond)
	if tr := b.win.Access(); tr != nil && strings.Contains(tr.Dump(), `group "alpha"`) {
		t.Errorf("the tree was built with no screen reader reading:\n%s", tr.Dump())
	}
	b.win.SetScreenReader(true)
	apptest.Poll(t, "the screen's tree for a screen reader starting", func() bool {
		tr := b.win.Access()
		return tr != nil && strings.Contains(tr.Dump(), `group "alpha"`)
	})
	b.close(t)
}
