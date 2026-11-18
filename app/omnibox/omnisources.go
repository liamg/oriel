package omnibox

import (
	"fmt"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/draw"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// commandsSource is every command, by its id, with the keys bound to it; its title is in the preview.
func commandsSource(h plugin.Host) uireg.PickerSource {
	keysFor := func(id string) []registry.Keymap {
		var out []registry.Keymap
		for _, k := range h.Keymaps() {
			if k.Command == id {
				out = append(out, k)
			}
		}
		return out
	}
	return uireg.PickerSource{ID: "commands", Title: "Commands", Prefix: ">", Order: 20,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			keys := map[string]string{}
			for _, k := range h.Keymaps() {
				if _, ok := keys[k.Command]; !ok {
					keys[k.Command] = kit.KeyLabel(k.Keys)
				}
			}
			var out []uireg.PickerItem
			for _, c := range h.Commands() {
				if !h.Available(c.ID, nil) {
					continue
				}
				it := uireg.PickerItem{ID: c.ID, Label: c.ID, Desc: c.Title, Icon: "›", Data: c}
				if len(c.Ex) > 0 {
					it.Detail = ":" + c.Ex[0]
				}
				it.Key = keys[c.ID]
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			cmd, ok := uireg.PickerData[registry.Command](it)
			if !ok {
				return nil
			}
			return &ui.Func{Key: "command", Fn: func(c *ui.Ctx) {
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					draw.PreviewHead(c, "command", ui.TextStyle{Color: c.Theme().C(theme.Accent)}, it.Label)
					if cmd.Title != "" {
						draw.PreviewText(c, "desc", cmd.Title, widget.UIFont(13, c.Theme().C(theme.FgMuted)))
					}
					draw.PreviewField(c, "source", cmd.Source)
					for i, k := range keysFor(cmd.ID) {
						draw.PreviewField(c, fmt.Sprint("keys ", i+1), k.Mode+" "+kit.KeyLabel(k.Keys))
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error { return h.Run(ctx, it.ID, nil) },
	}
}
