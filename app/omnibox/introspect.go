package omnibox

import (
	"context"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// introspect is :Keys and :Commands: every keymap and command, with where it came from, in a picker.
type Introspect struct {
	kit.Disposer
	ConfigDir string // the config directory, where :Keys reads the user's own keymaps
}

func (i *Introspect) Manifest() plugin.Manifest { return plugin.Manifest{ID: "introspect"} }

func (i *Introspect) Activate(_ context.Context, h plugin.Host) error {
	pick := func(source string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": source})
		}
	}
	i.Add(h.Picker(commandsSource(h)))
	i.Add(h.Picker(keymapsSource(h, i.ConfigDir)))
	i.Add(h.Command(registry.Command{ID: "keymaps.browse", Grant: registry.Open, Ex: "Keys", Title: "List keymaps", Args: registry.NoArgs, Run: pick("keymaps")}))
	i.Add(h.Command(registry.Command{ID: "commands.browse", Grant: registry.Open, Ex: "Commands", Title: "List commands", Args: registry.NoArgs, Run: pick("commands")}))
	return nil
}
