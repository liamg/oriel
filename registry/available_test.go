package registry_test

import (
	"errors"
	"testing"

	"github.com/liamg/oriel/registry"
)

// A command is available while it's registered, on, and its Available agrees.
func TestAvailable(t *testing.T) {
	r := registry.New()
	if r.Available("nope", nil) {
		t.Error("an unregistered command is available")
	}
	views := map[string]bool{"explorer": false}
	r.Command(registry.Command{ID: "open", Run: func(registry.Ctx, registry.Args) error { return nil },
		Available: func(a registry.Args) bool { return views[a["id"].(string)] }})
	if r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("available before its view is")
	}
	views["explorer"] = true
	if !r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("not available once its view is")
	}
	r.TurnOff(registry.KindCommand, "open", errors.New("broken"))
	if r.Available("open", registry.Args{"id": "explorer"}) {
		t.Error("available while turned off")
	}
	r.Command(registry.Command{ID: "boom", Run: func(registry.Ctx, registry.Args) error { return nil },
		Available: func(registry.Args) bool { panic("boom") }})
	if r.Available("boom", nil) {
		t.Error("a panicking Available says yes")
	}
}
