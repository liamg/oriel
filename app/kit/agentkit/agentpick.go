// Package agentkit is the agent controls the sessions and agent features share: picking a model, a sandbox backend
// and its settings, and what a provider's sessions can do.
package agentkit

import (
	"cmp"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"

	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ModelSelects are a provider's model and effort menus, at model and effort ("" is the default effort). It reports
// a pick, with what's picked: a new model's effort is its default.
func ModelSelects(c *ui.Ctx, p host.ProviderInfo, model, effort string, disabled bool, help string) (string, string, bool) {
	name := kit.ProviderNameIn(c, p.ID)
	picked := false
	c.Row("model", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		var ids, efforts []string
		for _, m := range p.Models {
			ids = append(ids, m.ID)
			if m.ID == model {
				efforts = m.Efforts
			}
		}
		if v, ok := widget.Select(c, "m", model, ids, widget.SelectOpts{Label: name + " model", Disabled: disabled, Help: help}); ok {
			model, effort, picked = v, "", true
		}
		if len(efforts) > 0 {
			label := cmp.Or(effort, "default effort")
			if v, ok := widget.Select(c, "e", label, efforts, widget.SelectOpts{Label: name + " effort", Disabled: disabled, Help: help}); ok {
				effort, picked = v, true
			}
		}
	})
	return model, effort, picked
}

// ToggleRowOpts are a ToggleRow's note at the right, whether it's off limits, and its help.
type ToggleRowOpts struct {
	Note     string
	Disabled bool
	Help     string
}

// ToggleRow is a toggle with its label, and a note at the right.
func ToggleRow(c *ui.Ctx, key any, label string, on *bool, o ToggleRowOpts) {
	note, disabled, help := o.Note, o.Disabled, o.Help
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if widget.Toggle(c, "t", *on, widget.ToggleOpts{Label: label, Disabled: disabled, Help: help}) {
			*on = !*on
		}
		fg := th.C(theme.Fg)
		if disabled {
			fg = th.C(theme.FgFaint)
		}
		c.Text("l", label, kit.UIFont(12.5, fg))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		c.Text("n", note, kit.MonoFont(10, th.C(theme.FgFaint)))
	})
}

// BackendPick is the sandbox backend a session runs in: chosen from those this machine offers, sandbox.backend
// until one is.
type BackendPick struct {
	Chosen  *string
	Offered []string
	Setting string
}

// SettingBackend is sandbox.backend.
func SettingBackend(h plugin.Host) string {
	v, _, _ := h.Options().Get("sandbox.backend", registry.At{})
	s, _ := v.(string)
	return s
}

// SandboxToggles are where an agent runs: in a sandbox, in which backend when this machine has more than one, and
// with an open network there.
func SandboxToggles(c *ui.Ctx, on, openNet *bool, pick BackendPick, help string) {
	ToggleRow(c, "sandbox", "Sandbox", on, ToggleRowOpts{Note: SandboxNoun(pick.Current()) + " · gateway for git & gh", Help: help})
	if *on && pick.Chosen != nil && len(pick.Offered) > 1 {
		c.Row("backend", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			labels := make([]string, len(pick.Offered))
			for i, b := range pick.Offered {
				labels[i] = strings.ToUpper(b)
			}
			at := slices.Index(pick.Offered, pick.Current())
			c.Text("l", "Sandbox type", kit.UIFont(12.5, c.Theme().C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Segmented(c, "pick", labels, &at, widget.SegmentedOpts{Label: "Sandbox type", Help: help}) {
				*pick.Chosen = pick.Offered[at]
			}
		})
	}
	ToggleRow(c, "network", "Open network", openNet, ToggleRowOpts{Note: "ask per new host when off", Disabled: !*on, Help: help})
}

// SandboxNoun is what a backend runs an agent in.
func SandboxNoun(backend string) string {
	if backend == "" || backend == "docker" {
		return "container"
	}
	return backend + " sandbox"
}

// Current is the backend chosen, else the setting.
func (p BackendPick) Current() string {
	if p.Chosen != nil && *p.Chosen != "" {
		return *p.Chosen
	}
	return p.Setting
}
