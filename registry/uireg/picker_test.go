package uireg_test

import (
	"context"
	"testing"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// A source with nothing to offer now is left out of the pickers, and back once it has.
func TestPickerAvailable(t *testing.T) {
	r := uireg.New()
	on := false
	r.Picker(uireg.PickerSource{ID: "always"})
	r.Picker(uireg.PickerSource{ID: "tickets", Available: func() bool { return on }})
	r.Picker(uireg.PickerSource{ID: "broken", Available: func() bool { panic("no") }})
	ids := func() (out []string) {
		for _, s := range r.Pickers() {
			out = append(out, s.ID)
		}
		return out
	}
	if got := ids(); len(got) != 1 || got[0] != "always" {
		t.Errorf("pickers %v", got)
	}
	on = true
	if got := ids(); len(got) != 2 || got[1] != "tickets" {
		t.Errorf("pickers %v", got)
	}
}

func TestPlaceholderOmnibox(t *testing.T) {
	r := uireg.New()
	var provided int
	r.Placeholder(registry.Placeholder{Name: "acme/hi", Omnibox: []string{"hi:"}, Provide: func() error {
		provided++
		r.Picker(uireg.PickerSource{ID: "hi", Prefix: "hi:", Items: func(_ registry.Ctx, q string) []uireg.PickerItem {
			return []uireg.PickerItem{{ID: q, Label: "hi " + q}}
		}})
		return nil
	}})
	var src uireg.PickerSource
	for _, s := range r.Pickers() {
		if s.Prefix == "hi:" {
			src = s
		}
	}
	if src.Items == nil || provided != 0 {
		t.Fatalf("no source stands for the prefix, or it's provided already (%d)", provided)
	}
	items := src.Items(context.Background(), "there")
	if provided != 1 || len(items) != 1 || items[0].Label != "hi there" {
		t.Errorf("provided %d times, items %+v", provided, items)
	}
	for _, s := range r.Pickers() {
		if s.Prefix == "hi:" && s.ID != "hi" {
			t.Errorf("the placeholder's source %q is still there", s.ID)
		}
	}
}
