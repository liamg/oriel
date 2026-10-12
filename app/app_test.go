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
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui"
)

// counter is a plugin built only on the public API: a command, a keymap and a view.
type counter struct{ n int }

func (c *counter) Manifest() ext.Manifest { return ext.Manifest{ID: "test.counter"} }

func (c *counter) Activate(_ context.Context, h ext.Host) error {
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
	h.View(app.RootView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.TextNode(fmt.Sprintf("count %d", c.n))}
		for _, s := range h.Sessions() {
			rows = append(rows, ui.TextNode(fmt.Sprintf("%s %s", s.ID(), s.Status())))
		}
		return ui.ColumnNode(rows...)
	}))
	return nil
}

func (c *counter) Deactivate(context.Context) error { return nil }

type booted struct {
	win   *headless.Window
	store core.Store
	done  chan error
}

func boot(t *testing.T, exts ...ext.Extension) *booted {
	t.Helper()
	return bootIn(t, t.TempDir(), exts...)
}

func bootIn(t *testing.T, configDir string, exts ...ext.Extension) *booted {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	b := &booted{store: eventstore.New(), done: make(chan error, 1)}
	go func() {
		b.done <- app.Run(ctx, be, app.Config{Store: b.store, Extensions: exts, ConfigDir: configDir})
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
	select {
	case err := <-b.done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("app did not exit after its window closed")
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

// lister binds keys to the built-in :Keys and :Commands.
type lister struct{}

func (lister) Manifest() ext.Manifest { return ext.Manifest{ID: "test.lister"} }
func (lister) Activate(_ context.Context, h ext.Host) error {
	h.Command(registry.Command{ID: "lister.noop", Title: "Do nothing", Run: func(registry.Ctx, registry.Args) error { return nil }})
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

func TestKeysAndCommandsListSources(t *testing.T) {
	b := boot(t, lister{})
	b.win.Send(render.KeyEvent{Key: "k", Mods: render.ModCtrl})
	b.waitLine(t, "n", "<C-k>", "Keys", "test.lister")
	b.win.Send(render.KeyEvent{Key: "c", Mods: render.ModCtrl})
	b.waitLine(t, "lister.noop", "Do nothing", "test.lister")
	b.waitLine(t, "Keys", "builtin:introspect")
	b.close(t)
}

// pointy is a plugin view with a box that asks for the pointer cursor.
type pointy struct{}

func (pointy) Manifest() ext.Manifest { return ext.Manifest{ID: "test.pointy"} }
func (pointy) Activate(_ context.Context, h ext.Host) error {
	h.View(app.RootView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
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
	// The root view sits inside the app's 16px padding.
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 30, Y: 25}})
	for b.win.Cursor() != render.CursorPointer {
		if ctx.Err() != nil {
			t.Fatalf("cursor is %v over the link", b.win.Cursor())
		}
		time.Sleep(time.Millisecond)
	}
	b.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 300, Y: 300}})
	for b.win.Cursor() != render.CursorDefault {
		if ctx.Err() != nil {
			t.Fatalf("cursor is %v off the link", b.win.Cursor())
		}
		time.Sleep(time.Millisecond)
	}
	b.close(t)
}

// themePicker sets ui.theme, as a user's settings would.
type themePicker string

func (p themePicker) Manifest() ext.Manifest { return ext.Manifest{ID: "test.theme"} }
func (p themePicker) Activate(_ context.Context, h ext.Host) error {
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
