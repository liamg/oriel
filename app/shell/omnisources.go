package shell

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// sessionsSource is the sessions, the ones that need you first.
func sessionsSource(h plugin.Host) uireg.PickerSource {
	boost := map[core.Status]int{core.StatusNeedsYou: 40, core.StatusPermission: 30, core.StatusReviewer: 10}
	return uireg.PickerSource{ID: "sessions", Title: "Sessions", Order: 10,
		Key: func() any { return h.State().Seq },
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			tabs := map[core.SessionID]int{}
			for i, s := range openTabs(h.State()) {
				tabs[s.ID()] = i
			}
			for _, s := range h.Sessions() {
				it := uireg.PickerItem{ID: string(s.ID()), Label: s.Name(), Detail: string(s.Kind()), Note: statusWord(s),
					IconStyle: "status." + string(s.Status()), Boost: boost[s.Status()], Data: s}
				if i, ok := tabs[s.ID()]; ok && i < 9 {
					it.Key = fmt.Sprintf("⌘%d", i+1)
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			s := it.Data.(core.Session)
			return &ui.Func{Key: "session", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					tone := ui.TextStyle{Color: th.C(kit.StatusTokens[s.Status()])}
					kit.PreviewHead(c, strings.ReplaceAll(string(s.Status()), "_", " "), tone, s.Name())
					kit.PreviewField(c, "kind", string(s.Kind()))
					kit.PreviewField(c, "session", string(s.ID()))
					if n := s.Unseen(); n > 0 {
						kit.PreviewField(c, "errors", fmt.Sprintf("%d unseen", n))
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			return h.Run(ctx, "session.focus", registry.Args{"id": it.ID})
		},
	}
}

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
					keys[k.Command] = k.Keys
				}
			}
			var out []uireg.PickerItem
			for _, c := range h.Commands() {
				if !h.Available(c.ID, nil) {
					continue
				}
				it := uireg.PickerItem{ID: c.ID, Label: c.ID, Desc: c.Title, Icon: "›", Data: c}
				it.Key = keys[c.ID]
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			cmd := it.Data.(registry.Command)
			return &ui.Func{Key: "command", Fn: func(c *ui.Ctx) {
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					kit.PreviewHead(c, "command", ui.TextStyle{Color: c.Theme().C(theme.Accent)}, it.Label)
					if cmd.Title != "" {
						kit.PreviewText(c, "desc", cmd.Title, kit.UIFont(13, c.Theme().C(theme.FgMuted)))
					}
					kit.PreviewField(c, "source", cmd.Source)
					for i, k := range keysFor(cmd.ID) {
						kit.PreviewField(c, fmt.Sprint("keys ", i+1), k.Mode+" "+k.Keys)
					}
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error { return h.Run(ctx, it.ID, nil) },
	}
}
