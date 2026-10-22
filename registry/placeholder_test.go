package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
)

// lazyHi is a placeholder that, when provided, registers what it stands for: a command HiThere, a key bound to it
// and a hook on Save. Its omnibox prefix is the pickers' (registry/uireg).
func lazyHi(r registry.Registry, provided *int, ran *[]string) registry.Placeholder {
	return registry.Placeholder{Name: "acme/hi", Commands: []string{"Hi*"},
		Keys:    []registry.Keymap{{Mode: "n", Keys: "<Leader>h"}},
		Events:  []registry.EventTrigger{{Event: "Save", Pattern: "*.go"}},
		Omnibox: []string{"hi:"},
		Provide: func() error {
			*provided++
			r.Command(registry.Command{ID: "HiThere", Run: func(_ registry.Ctx, a registry.Args) error {
				*ran = append(*ran, "HiThere "+a.String("who"))
				return nil
			}})
			r.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>h", Command: "HiThere", Args: registry.Args{"who": "key"}})
			r.Hook(registry.Hook{Event: "Save", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
				*ran = append(*ran, "Save "+ev.(string))
				return registry.Verdict{}, nil
			}})
			return nil
		}}
}

// Lazy load on command, key, event, filetype or omnibox source, then the trigger goes on as if it had always been
// loaded.
func TestPlaceholderCommandGlob(t *testing.T) {
	r := registry.New()
	var provided int
	var ran []string
	r.Placeholder(lazyHi(r, &provided, &ran))
	ctx := context.Background()
	if err := r.Run(ctx, "Nope", nil); !registry.IsNoCommand(err, "Nope") {
		t.Errorf("an id no placeholder matches: %v", err)
	}
	if provided != 0 {
		t.Fatal("provided before anything was used")
	}
	if !r.Available("HiThere", nil) {
		t.Error("a command a placeholder stands for isn't available")
	}
	if err := r.Run(ctx, "HiThere", registry.Args{"who": "cmd"}); err != nil {
		t.Fatal(err)
	}
	if provided != 1 || len(ran) != 1 || ran[0] != "HiThere cmd" {
		t.Errorf("provided %d times, ran %q", provided, ran)
	}
	if err := r.Run(ctx, "HiNothing", nil); !registry.IsNoCommand(err, "HiNothing") {
		t.Errorf("a glob's id the provider didn't register: %v", err)
	}
	if provided != 1 {
		t.Errorf("provided %d times", provided)
	}
}

func TestPlaceholderKey(t *testing.T) {
	r := registry.New()
	var provided int
	var ran []string
	r.Placeholder(lazyHi(r, &provided, &ran))
	k, _ := r.Lookup([]string{"n"}, keymap.Parse(" h"))
	if k == nil {
		t.Fatal("the placeholder's key isn't mapped")
	}
	if err := r.Run(context.Background(), k.Command, k.Args); err != nil {
		t.Fatal(err)
	}
	if provided != 1 || len(ran) != 1 || ran[0] != "HiThere key" {
		t.Errorf("provided %d times, ran %q", provided, ran)
	}
	if k, _ := r.Lookup([]string{"n"}, keymap.Parse(" h")); k == nil || k.Command != "HiThere" {
		t.Errorf("after: %+v", k)
	}
}

func TestPlaceholderEvent(t *testing.T) {
	r := registry.New()
	var provided int
	var ran []string
	r.Placeholder(lazyHi(r, &provided, &ran))
	ctx := context.Background()
	r.Fire(ctx, "Save", "/x/a.txt", "a.txt")
	r.Fire(ctx, "Other", "/x/a.go", "a.go")
	if provided != 0 {
		t.Fatal("provided on an event or file it isn't for")
	}
	r.Fire(ctx, "Save", "/x/b.go", "b.go")
	if provided != 1 || len(ran) != 1 || ran[0] != "Save b.go" {
		t.Errorf("provided %d times, ran %q", provided, ran)
	}
}

func TestPlaceholderProvideFails(t *testing.T) {
	r := registry.New()
	r.Placeholder(registry.Placeholder{Name: "acme/broken", Commands: []string{"Broken"}, Provide: func() error { return errors.New("no init.lua") }})
	err := r.Run(context.Background(), "Broken", nil)
	if err == nil || registry.IsNoCommand(err, "Broken") {
		t.Errorf("err = %v, want why it didn't load", err)
	}
}

func TestPlaceholderDisposed(t *testing.T) {
	r := registry.New()
	var provided int
	var ran []string
	d := r.Placeholder(lazyHi(r, &provided, &ran))
	d.Dispose()
	if err := r.Run(context.Background(), "HiThere", nil); !registry.IsNoCommand(err, "HiThere") || provided != 0 {
		t.Errorf("a disposed placeholder: %v, provided %d", err, provided)
	}
	if k, _ := r.Lookup([]string{"n"}, keymap.Parse(" h")); k != nil {
		t.Errorf("its key is still mapped: %+v", k)
	}
}

// A command named in full is listed before it's provided, so the omnibox and :Name find it.
func TestPlaceholderNamedCommandIsListed(t *testing.T) {
	r := registry.New()
	ran := 0
	r.Placeholder(registry.Placeholder{Name: "acme/hi", Commands: []string{"Hi"}, Provide: func() error {
		r.Command(registry.Command{ID: "Hi", Run: func(registry.Ctx, registry.Args) error { ran++; return nil }})
		return nil
	}})
	listed := false
	for _, c := range r.Commands() {
		listed = listed || c.ID == "Hi"
	}
	if !listed {
		t.Error("Hi isn't listed")
	}
	if err := r.Run(context.Background(), "Hi", nil); err != nil || ran != 1 {
		t.Errorf("ran %d: %v", ran, err)
	}
}

func TestTriggersFromText(t *testing.T) {
	if k := registry.KeyTrigger("<leader>f"); k.Mode != "n" || k.Keys != "<leader>f" {
		t.Errorf("%+v", k)
	}
	if k := registry.KeyTrigger("global <D-j>"); k.Mode != "global" || k.Keys != "<D-j>" {
		t.Errorf("%+v", k)
	}
	if e := registry.EventTriggerOf("BufRead *.go"); e.Event != "BufRead" || e.Pattern != "*.go" {
		t.Errorf("%+v", e)
	}
	if e := registry.EventTriggerOf("SessionStart"); e.Event != "SessionStart" || e.Pattern != "" {
		t.Errorf("%+v", e)
	}
}

type viewStub string

func (viewStub) ContributionKind() string   { return registry.KindView }
func (v viewStub) ContributionName() string { return string(v) }

// A view a placeholder stands for: what opens views asks for its holder, and providing it registers the view (a
// rail entry's view.toggle on a lazy plugin's panel).
func TestPlaceholderView(t *testing.T) {
	r := registry.New()
	provided := 0
	r.Placeholder(registry.Placeholder{Name: "acme/hi", Contributions: map[string][]string{registry.KindView: {"hi.*"}}, Provide: func() error {
		provided++
		r.Contribute(viewStub("hi.panel"))
		return nil
	}})
	if _, ok := r.Holder(registry.KindView, "other"); ok {
		t.Fatal("it holds a view it doesn't stand for")
	}
	provide, ok := r.Holder(registry.KindView, "hi.panel")
	if !ok || provided != 0 {
		t.Fatalf("holds hi.panel %v, provided %d", ok, provided)
	}
	if err := provide(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Contribution(registry.KindView, "hi.panel"); !ok || provided != 1 {
		t.Errorf("hi.panel registered %v, provided %d", ok, provided)
	}
	if _, ok := r.Holder(registry.KindView, "hi.panel"); ok {
		t.Error("it still holds hi.panel once provided")
	}
}
