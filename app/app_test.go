package app_test

import (
	"context"
	"fmt"
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
	h.Command(registry.Command{ID: "counter.add", Title: "Add one", Run: func(registry.Ctx, registry.Args) error {
		c.n++
		return nil
	}})
	h.Keymap(registry.Keymap{Mode: "n", Keys: "x", Command: "counter.add"})
	h.View(app.RootView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		rows := []ui.Node{ui.Text(fmt.Sprintf("count %d", c.n))}
		for _, s := range h.Sessions() {
			rows = append(rows, ui.Text(fmt.Sprintf("%s %s", s.ID(), s.Status())))
		}
		return ui.Column(rows...)
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	b := &booted{store: eventstore.New(), done: make(chan error, 1)}
	go func() { b.done <- app.Run(ctx, be, app.Config{Store: b.store, Extensions: exts}) }()
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
