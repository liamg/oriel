// Package vimreg keeps the vim engine's options in the registry: each is an OptionSpec named editor.*, with vim's
// names as aliases, so :set, the settings page, settings.toml and Lua all change one value.
package vimreg

import (
	"math"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/vim"
)

// names are the registry's names for the engine's options.
var names = map[string]string{
	"shiftwidth":    "editor.shift_width",
	"tabstop":       "editor.tab_stop",
	"expandtab":     "editor.expand_tab",
	"ignorecase":    "editor.ignore_case",
	"smartcase":     "editor.smart_case",
	"wrapscan":      "editor.wrap_scan",
	"magic":         "editor.magic",
	"report":        "editor.report",
	"nrformats":     "editor.number_formats",
	"hidden":        "editor.hidden",
	"clipboard":     "editor.clipboard",
	"wrap":          "editor.wrap",
	"sidescroll":    "editor.side_scroll",
	"sidescrolloff": "editor.side_scroll_off",
}

// Name is the registry's name for a vim option.
func Name(vimName string) string { return names[vimName] }

// Specs are the engine's options as registry options.
func Specs() []registry.OptionSpec {
	var out []registry.OptionSpec
	for _, d := range vim.OptionDefs() {
		s := registry.OptionSpec{Name: Name(d.Name), Section: "editor", Default: d.Default, Help: d.Help, Values: d.Values,
			Scopes:  registry.ScopeUser | registry.ScopeProject | registry.ScopeSession,
			Aliases: []string{d.Name}}
		if d.Short != "" {
			s.Aliases = append(s.Aliases, d.Short)
		}
		switch d.Kind {
		case "bool":
			s.Type = registry.Bool
		case "int":
			s.Type = registry.Int
			s.Min, s.Max = float64(d.Min), math.MaxInt32
		case "enum":
			s.Type = registry.Enum
		default:
			s.Type = registry.String
		}
		switch d.Scope {
		case vim.BufferOption:
			s.Scopes |= registry.ScopeBuffer
		case vim.WindowOption:
			s.Scopes |= registry.ScopeWindow
		}
		out = append(out, s)
	}
	return out
}

// Register adds the engine's options to r.
func Register(r registry.Registry) []registry.Disposable {
	var out []registry.Disposable
	for _, s := range Specs() {
		out = append(out, r.Option(s))
	}
	return out
}

// Settings is vim.Settings over the registry's options. At places a buffer (by name) and window (the engine's id)
// in the IDE: its session and project too.
type Settings struct {
	Options registry.Options
	At      func(buffer, window string) registry.At
}

// at is where buffer and window are; an editor in no session (a test, a scratch window) has a session of its own,
// so :set of a global option has somewhere to go.
func (s Settings) at(buffer, window string) registry.At {
	at := registry.At{Buffer: buffer, Window: window}
	if s.At != nil {
		at = s.At(buffer, window)
	}
	if at.Session == "" {
		at.Session = "vim"
	}
	return at
}

func (s Settings) Get(name, buffer, window string) (any, bool) {
	v, _, err := s.Options.Get(Name(name), s.at(buffer, window))
	return v, err == nil
}

// Set is :set: a buffer option at the buffer, a window option at the window, a global one for the session. A
// buffer with no name has no layer of its own, so its options go to the session.
func (s Settings) Set(name string, scope vim.OptionScope, buffer, window string, v any) error {
	at := s.at(buffer, window)
	l := registry.LayerSession
	switch {
	case scope == vim.BufferOption && at.Buffer != "":
		l = registry.LayerBuffer
	case scope == vim.WindowOption && at.Window != "":
		l = registry.LayerWindow
	}
	return s.Options.SetFrom(Name(name), l, at, v, ":set")
}
