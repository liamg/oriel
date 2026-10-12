package registry_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

func TestOverrideAndDispose(t *testing.T) {
	r := registry.New()
	var ran []string
	cmd := func(name string) registry.Command {
		return registry.Command{ID: "x.run", Run: func(registry.Ctx, registry.Args) error { ran = append(ran, name); return nil }}
	}
	base := r.Command(cmd("base"))
	over := r.Command(cmd("override"))
	ctx := context.Background()

	r.Run(ctx, "x.run", nil)
	over.Dispose()
	r.Run(ctx, "x.run", nil)
	over.Dispose()
	r.Run(ctx, "x.run", nil)
	base.Dispose()
	if err := r.Run(ctx, "x.run", nil); err == nil {
		t.Error("ran a disposed command")
	}
	if want := []string{"override", "base", "base"}; len(ran) != 3 || ran[0] != want[0] || ran[1] != want[1] || ran[2] != want[2] {
		t.Errorf("ran %q, want %q", ran, want)
	}
}

func TestDisposeUnderneathKeepsOverride(t *testing.T) {
	r := registry.New()
	base := r.View("v", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.Text("base") }))
	r.View("v", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.Text("override") }))
	base.Dispose()
	v, ok := r.ViewByID("v")
	if !ok || v.Build(registry.ViewCtx{}) != ui.Text("override") {
		t.Error("disposing the base view removed the override")
	}
}

func TestKeymapsArePerMode(t *testing.T) {
	r := registry.New()
	r.Keymap(registry.Keymap{Mode: "n", Keys: "x", Command: "a"})
	r.Keymap(registry.Keymap{Mode: "requests", Keys: "x", Command: "b"})
	if k, ok := r.Key("n", "x"); !ok || k.Command != "a" {
		t.Errorf("n x = %v, %v", k, ok)
	}
	if k, ok := r.Key("requests", "x"); !ok || k.Command != "b" {
		t.Errorf("requests x = %v, %v", k, ok)
	}
	if _, ok := r.Key("i", "x"); ok {
		t.Error("i x is bound")
	}
}
