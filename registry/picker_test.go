package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// A source with nothing to offer now is left out of the pickers, and back once it has.
func TestPickerAvailable(t *testing.T) {
	r := registry.New()
	on := false
	r.Picker(registry.PickerSource{ID: "always"})
	r.Picker(registry.PickerSource{ID: "tickets", Available: func() bool { return on }})
	r.Picker(registry.PickerSource{ID: "broken", Available: func() bool { panic("no") }})
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
