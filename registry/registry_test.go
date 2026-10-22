package registry_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
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

func TestKeymapsArePerMode(t *testing.T) {
	r := registry.New()
	r.Keymap(registry.Keymap{Mode: "n", Keys: "x", Command: "a"})
	r.Keymap(registry.Keymap{Mode: "requests", Keys: "x", Command: "b"})
	if k, _ := r.Lookup([]string{"n"}, []string{"x"}); k == nil || k.Command != "a" {
		t.Errorf("n x = %v", k)
	}
	if k, _ := r.Lookup([]string{"requests"}, []string{"x"}); k == nil || k.Command != "b" {
		t.Errorf("requests x = %v", k)
	}
	if k, _ := r.Lookup([]string{"i"}, []string{"x"}); k != nil {
		t.Error("i x is bound")
	}
}

func TestKeymapsFollowAddAndDispose(t *testing.T) {
	r := registry.New()
	r.Keymap(registry.Keymap{Mode: "n", Keys: "b", Command: "b"})
	if got := r.Keymaps(); len(got) != 1 {
		t.Fatalf("Keymaps = %v", got)
	}
	d := r.Keymap(registry.Keymap{Mode: "n", Keys: "a", Command: "a"})
	if got := r.Keymaps(); len(got) != 2 || got[0].Command != "a" {
		t.Fatalf("after adding a: %v", got)
	}
	d.Dispose()
	r.Keymaps()[0].Command = "changed"
	if got := r.Keymaps(); len(got) != 1 || got[0].Command != "b" {
		t.Errorf("after disposing a, and changing a copy: %v", got)
	}
}

func TestDisposedKeymapStopsWaiting(t *testing.T) {
	r := registry.New()
	d := r.Keymap(registry.Keymap{Mode: "n", Keys: "qq", Command: "twice"})
	if _, more := r.Lookup([]string{"n"}, []string{"q"}); !more {
		t.Fatal("q isn't a prefix of qq")
	}
	d.Dispose()
	if _, more := r.Lookup([]string{"n"}, []string{"q"}); more {
		t.Error("q is still a prefix after qq was disposed")
	}
}

func TestThemes(t *testing.T) {
	r := registry.New()
	builtin := theme.Builtins()[0]
	_, gen0 := r.Themes()
	d := r.Theme(builtin)
	ts, gen1 := r.Themes()
	if len(ts) != 1 || ts[0].Name() != builtin.Name() {
		t.Errorf("themes %v", ts)
	}
	d.Dispose()
	ts, gen2 := r.Themes()
	if len(ts) != 0 {
		t.Errorf("a disposed theme is still listed: %v", ts)
	}
	if gen0 == gen1 || gen1 == gen2 {
		t.Errorf("the generation didn't move: %d, %d, %d", gen0, gen1, gen2)
	}
}

// A language registered later under a name shadows the earlier one until it's disposed.
func TestLanguages(t *testing.T) {
	r := registry.New()
	r.Language(registry.Language{Name: "go", Source: "builtin"})
	r.Language(registry.Language{Name: "lua", Source: "builtin"})
	_, gen0 := r.Languages()
	d := r.Language(registry.Language{Name: "go", Source: "pack"})
	ls, gen1 := r.Languages()
	if len(ls) != 2 || ls[1].Name != "go" || ls[1].Source != "pack" || gen1 == gen0 {
		t.Fatalf("with the pack: %+v (gen %d → %d)", ls, gen0, gen1)
	}
	d.Dispose()
	ls, _ = r.Languages()
	if len(ls) != 2 || ls[0].Name != "go" || ls[0].Source != "builtin" {
		t.Errorf("after disposing the pack: %+v", ls)
	}
}

func TestArgs(t *testing.T) {
	a := registry.Args{"s": "x", "i": 3, "f": 4.0, "b": true}
	if a.String("s") != "x" || a.String("i") != "" || a.Int("i") != 3 || a.Int("f") != 4 || a.Int("s") != 0 || !a.Bool("b") || a.Bool("s") {
		t.Errorf("%v", a)
	}
}

func TestKeymapsForFollowsAddAndDispose(t *testing.T) {
	r := registry.New()
	r.Keymap(registry.Keymap{Mode: "n", Keys: "b", Command: "open"})
	d := r.Keymap(registry.Keymap{Mode: "global", Keys: "a", Command: "open"})
	r.Keymap(registry.Keymap{Mode: "n", Keys: "c", Command: "close"})
	if got := r.KeymapsFor("open"); len(got) != 2 || got[0].Keys != "a" || got[1].Keys != "b" {
		t.Fatalf("KeymapsFor(open) = %v", got)
	}
	d.Dispose()
	if got := r.KeymapsFor("open"); len(got) != 1 || got[0].Keys != "b" {
		t.Errorf("after disposing a: %v", got)
	}
	if got := r.KeymapsFor("missing"); len(got) != 0 {
		t.Errorf("KeymapsFor(missing) = %v", got)
	}
}
