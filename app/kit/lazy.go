package kit

import (
	"context"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// LazyOn are a Go plugin's lazy triggers (plugin.Lazy) from what it registers: the ids (and titles) of cmds, and
// the keys of keys that run a command, "mode keys".
func LazyOn(cmds []registry.Command, keys []registry.Keymap) plugin.Lazy {
	lz := plugin.Lazy{Titles: map[string]string{}}
	for _, c := range cmds {
		lz.Commands = append(lz.Commands, c.ID)
		lz.Titles[c.ID] = c.Title
	}
	for _, k := range keys {
		if k.Command != "" {
			lz.Keys = append(lz.Keys, k.Mode+" "+k.Keys)
		}
	}
	return lz
}

// Entries are what a lazy plugin registers from the start, as a plugin of its own: its options and events, and the
// ways to it that are on screen before it loads (its rail entry, a key). Its own contributions of the same names
// stack over them once it loads.
type Entries struct {
	Disposer
	ID       string
	Options  []registry.OptionSpec
	Events   []registry.EventInfo
	Contribs []registry.Contribution
	Keymaps  []registry.Keymap
}

func (x *Entries) Manifest() plugin.Manifest { return plugin.Manifest{ID: x.ID} }

func (x *Entries) Activate(_ context.Context, h plugin.Host) error {
	for _, o := range x.Options {
		x.Add(h.Option(o))
	}
	for _, e := range x.Events {
		x.Add(h.DeclareEvent(e))
	}
	for _, c := range x.Contribs {
		x.Add(h.Contribute(c))
	}
	for _, k := range x.Keymaps {
		x.Add(h.Keymap(k))
	}
	return nil
}
