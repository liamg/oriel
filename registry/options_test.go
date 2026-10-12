package registry_test

import (
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

func lineNumbers() registry.OptionSpec {
	return registry.OptionSpec{
		Name:    "editor.line_numbers",
		Type:    registry.Enum,
		Values:  []string{"off", "absolute", "relative"},
		Default: "absolute",
		Scopes:  registry.ScopeUser | registry.ScopeProject | registry.ScopeSession | registry.ScopeBuffer | registry.ScopeWindow,
		Aliases: []string{"number"},
	}
}

// Later layers win: defaults, the user file, init.lua, the project file, the session, then buffer and window.
func TestOptionScopePrecedence(t *testing.T) {
	r := registry.New()
	r.Option(lineNumbers())
	o := r.Options()
	here := registry.At{Project: "/src/oriel", Session: "s1", Buffer: "b1", Window: "w1"}
	steps := []struct {
		layer registry.Layer
		at    registry.At
		value string
	}{
		{registry.LayerUser, registry.At{}, "relative"},
		{registry.LayerLua, registry.At{}, "off"},
		{registry.LayerProject, registry.At{Project: "/src/oriel"}, "absolute"},
		{registry.LayerSession, registry.At{Session: "s1"}, "relative"},
		{registry.LayerBuffer, registry.At{Buffer: "b1"}, "off"},
		{registry.LayerWindow, registry.At{Window: "w1"}, "absolute"},
	}
	if v, l, _ := o.Get("editor.line_numbers", here); v != "absolute" || l != registry.LayerDefault {
		t.Errorf("default: %v from %v", v, l)
	}
	for _, s := range steps {
		if err := o.Set("editor.line_numbers", s.layer, s.at, s.value); err != nil {
			t.Fatalf("set at %v: %v", s.layer, err)
		}
		if v, l, _ := o.Get("editor.line_numbers", here); v != s.value || l != s.layer {
			t.Errorf("after setting %v: %v from %v", s.layer, v, l)
		}
	}
	// Elsewhere only the layers that reach it apply.
	for _, tt := range []struct {
		at    registry.At
		value string
		layer registry.Layer
	}{
		{registry.At{}, "off", registry.LayerLua},
		{registry.At{Project: "/src/other", Session: "s2"}, "off", registry.LayerLua},
		{registry.At{Project: "/src/oriel", Session: "s2", Buffer: "b2"}, "absolute", registry.LayerProject},
		{registry.At{Project: "/src/oriel", Session: "s1", Buffer: "b2"}, "relative", registry.LayerSession},
		{registry.At{Project: "/src/oriel", Session: "s1", Buffer: "b1", Window: "w2"}, "off", registry.LayerBuffer},
	} {
		if v, l, _ := o.Get("editor.line_numbers", tt.at); v != tt.value || l != tt.layer {
			t.Errorf("at %+v: %v from %v, want %v from %v", tt.at, v, l, tt.value, tt.layer)
		}
	}
	// Unsetting a layer uncovers the one beneath.
	o.Unset("editor.line_numbers", registry.LayerWindow, registry.At{Window: "w1"})
	if v, l, _ := o.Get("number", here); v != "off" || l != registry.LayerBuffer {
		t.Errorf("after unsetting the window: %v from %v", v, l)
	}
}

func TestOptionValidation(t *testing.T) {
	r := registry.New()
	r.Option(lineNumbers())
	r.Option(registry.OptionSpec{Name: "editor.tab_width", Type: registry.Int, Default: 4, Min: 1, Max: 16, Scopes: registry.ScopeUser | registry.ScopeBuffer})
	o := r.Options()
	for _, tt := range []struct {
		name  string
		layer registry.Layer
		at    registry.At
		v     any
	}{
		{"editor.line_numbers", registry.LayerUser, registry.At{}, "sideways"},
		{"editor.line_numbers", registry.LayerUser, registry.At{}, true},
		{"editor.tab_width", registry.LayerUser, registry.At{}, 0},
		{"editor.tab_width", registry.LayerUser, registry.At{}, "4"},
		{"editor.tab_width", registry.LayerSession, registry.At{Session: "s1"}, 2},
		{"editor.tab_width", registry.LayerBuffer, registry.At{}, 2},
		{"editor.nope", registry.LayerUser, registry.At{}, 1},
	} {
		if err := o.Set(tt.name, tt.layer, tt.at, tt.v); err == nil {
			t.Errorf("set %s = %v at %v succeeded", tt.name, tt.v, tt.layer)
		}
	}
	if v, _, _ := o.Get("editor.tab_width", registry.At{}); v != 4 {
		t.Errorf("tab_width = %v after rejected sets", v)
	}
}

func TestOptionWatchAndDispose(t *testing.T) {
	r := registry.New()
	d := r.Option(registry.OptionSpec{Name: "ui.blink", Type: registry.Duration, Default: 500 * time.Millisecond, Scopes: registry.ScopeUser})
	o := r.Options()
	var seen []any
	cancel := o.Watch("ui.blink", func() {
		v, _, _ := o.Get("ui.blink", registry.At{})
		seen = append(seen, v)
	})
	o.Set("ui.blink", registry.LayerUser, registry.At{}, time.Second)
	cancel()
	o.Set("ui.blink", registry.LayerUser, registry.At{}, 2*time.Second)
	if len(seen) != 1 || seen[0] != time.Second {
		t.Errorf("watch saw %v", seen)
	}
	if specs := o.Specs(); len(specs) == 0 || specs[len(specs)-1].Name != "ui.blink" {
		t.Errorf("specs = %v", specs)
	}
	d.Dispose()
	if _, _, err := o.Get("ui.blink", registry.At{}); err == nil {
		t.Error("a disposed option still resolves")
	}
}
