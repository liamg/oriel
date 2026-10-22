package shell

import (
	"fmt"
	"path/filepath"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

// keymapsSource is every keymap, with its mode, keys and source: Enter runs it, ⌃e opens where it's defined.
func keymapsSource(h plugin.Host, configDir string) uireg.PickerSource {
	return uireg.PickerSource{ID: "keymaps", Title: "Keymaps", Order: 50, PickerOnly: true,
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for i, k := range h.Keymaps() {
				label := k.Desc
				if label == "" {
					label = k.Command
				}
				out = append(out, uireg.PickerItem{ID: fmt.Sprint(i), Label: label + " " + k.Keys, Detail: k.Mode + " · " + k.Command, Note: k.Source,
					Icon: "⌨", Data: k})
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			k := it.Data.(registry.Keymap)
			return &ui.Func{Key: "keymap", Fn: func(c *ui.Ctx) {
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					kit.PreviewHead(c, k.Mode, ui.TextStyle{Color: c.Theme().C(tOmniMatch)}, k.Keys)
					kit.PreviewField(c, "command", k.Command)
					if len(k.Args) > 0 {
						kit.PreviewField(c, "args", fmt.Sprint(k.Args))
					}
					kit.PreviewField(c, "source", k.Source)
					if k.Desc != "" {
						kit.PreviewField(c, "desc", k.Desc)
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			k := it.Data.(registry.Keymap)
			return h.Run(ctx, k.Command, k.Args)
		},
		Actions: []uireg.PickerAction{{Key: "<C-e>", Title: "go to definition", Run: func(ctx registry.Ctx, items []uireg.PickerItem) error {
			k := items[0].Data.(registry.Keymap)
			file, line := script.SplitLoc(k.Source)
			if line == 0 {
				return fmt.Errorf("%s isn't a place in a file", k.Source)
			}
			path := file
			if !filepath.IsAbs(path) {
				path = filepath.Join(configDir, file)
			}
			return h.Run(ctx, "editor.open", registry.Args{"path": path, "line": line})
		}}},
	}
}
