package settings

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// ConsentKeyMode is the install consent dialog's keymap mode.
const ConsentKeyMode = "consent"

const consentHelp = "plugins/install"

var (
	tRiskLow  = theme.Declare("plugins.consent.risk.low", "fg.faint")
	tRiskMed  = theme.Declare("plugins.consent.risk.medium", "amber")
	tRiskHigh = theme.Declare("plugins.consent.risk.high", "cyan")
	tRiskCrit = theme.Declare("plugins.consent.risk.critical", "red")
	tRiskRow  = theme.Declare("plugins.consent.row.border", "line")

	tBadgeBuiltin    = theme.Declare("plugins.badge.builtin", "fg.faint")
	tBadgeReviewed   = theme.Declare("plugins.badge.reviewed", "green")
	tBadgeTrusted    = theme.Declare("plugins.badge.trusted", "cyan")
	tBadgeUnverified = theme.Declare("plugins.badge.unverified", "warn")
	tConsentWarning  = theme.Declare("plugins.consent.warning", "warn")
)

// badgeTone is a plugin badge's colour.
func badgeTone(badge string) theme.TokenID {
	switch badge {
	case "reviewed":
		return tBadgeReviewed
	case "trusted":
		return tBadgeTrusted
	case "unverified":
		return tBadgeUnverified
	}
	return tBadgeBuiltin
}

// Consent is builtin:consent: the all-or-nothing install and update consent. An
// installer asks through plugins.consent; the dialog shows every capability the plugin declares, with Oriel's
// explanation and the author's reason, and Install (or Update) runs the installer's command. Nothing is granted
// in part.
type Consent struct {
	kit.Disposer
	h     plugin.Host
	queue []*consentAsk // the first is on screen
	focus bool
}

type consentAsk struct {
	c      plugin.Consent
	answer func(accepted bool)
}

func (x *Consent) Manifest() plugin.Manifest { return plugin.Manifest{ID: "consent"} }

func (x *Consent) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: ConsentKeyMode, Keys: "<D-CR>", Command: "plugins.consent.accept", Desc: "Install"}))
	x.Add(h.View(kit.OverlayPrefix+"plugins.consent", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "consent", Fn: x.view}
	})))
	return nil
}

func (x *Consent) commands() []registry.Command {
	open := func(registry.Args) bool { return len(x.queue) > 0 }
	return []registry.Command{
		{ID: "plugins.consent", Title: "Ask to install the plugin in {dir} (or update to it from the one in {from}), then run {then} with {dir} if it's accepted; {source}, {badge}, {warning} and {facts} ({label, value}s) are what's known of where it comes from",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.consentCommand(a) }},
		{ID: "plugins.consent.accept", Title: "Install: accept every capability the plugin asks for", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.answer(true); return nil }},
		{ID: "plugins.consent.cancel", Title: "Cancel the install: grant nothing", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.answer(false); return nil }},
	}
}

func (x *Consent) Deactivate(ctx context.Context) error {
	for _, a := range x.queue {
		a.answer(false)
	}
	x.queue = nil
	return x.Disposer.Deactivate(ctx)
}

// ask shows c and calls answer with the choice; a consent with nothing to ask is accepted at once. Call it on the
// loop.
func (x *Consent) ask(c plugin.Consent, answer func(accepted bool)) {
	if c.Silent() {
		answer(true)
		return
	}
	x.queue = append(x.queue, &consentAsk{c: c, answer: answer})
	if len(x.queue) == 1 {
		x.focus = true
	}
	x.h.Redraw()
}

func (x *Consent) answer(accepted bool) {
	if len(x.queue) == 0 {
		return
	}
	a := x.queue[0]
	x.queue = x.queue[1:]
	x.focus = len(x.queue) > 0
	a.answer(accepted)
	x.h.Redraw()
}

// consentCommand is plugins.consent: the manifests in dir and from, and the command to run once accepted.
func (x *Consent) consentCommand(a registry.Args) error {
	dir := plugin.Expand(a.String("dir"))
	if dir == "" {
		return fmt.Errorf("plugins.consent needs the plugin's {dir}")
	}
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		return err
	}
	var from *plugin.Manifest
	if f := a.String("from"); f != "" {
		old, err := plugin.LoadManifest(plugin.Expand(f))
		if err != nil {
			return err
		}
		if old.ID != m.ID {
			return fmt.Errorf("%s isn't an update of %s", m.ID, old.ID)
		}
		from = &old
	}
	c := plugin.Ask(m, from)
	c.Source = a.String("source")
	if c.Source == "" {
		c.Source = dir
	}
	c.Badge, c.Warning = a.String("badge"), a.String("warning")
	if fs, ok := a["facts"].([]any); ok {
		for _, f := range fs {
			if f, ok := f.(map[string]any); ok {
				l, _ := f["label"].(string)
				v, _ := f["value"].(string)
				c.Facts = append(c.Facts, plugin.Fact{Label: l, Value: v})
			}
		}
	}
	then := a.String("then")
	x.ask(c, func(ok bool) {
		if !ok || then == "" {
			return
		}
		plugin.Go(x.h, "what follows a plugin's install", func() {
			if err := x.h.Run(context.Background(), then, registry.Args{"dir": dir}); err != nil {
				x.h.Notify(core.Notice{ID: "plugins.install", Severity: core.SevError, Time: time.Now(), Title: "Install " + m.ID, Text: err.Error()})
			}
		})
	})
	return nil
}

func riskTone(r plugin.Risk) theme.TokenID {
	switch r {
	case plugin.RiskMedium:
		return tRiskMed
	case plugin.RiskHigh:
		return tRiskHigh
	case plugin.RiskCritical:
		return tRiskCrit
	}
	return tRiskLow
}

func riskWord(r plugin.Risk) string {
	return [...]string{"low", "med", "high", "crit"}[r]
}

func (x *Consent) view(c *ui.Ctx) {
	if len(x.queue) == 0 {
		return
	}
	a := x.queue[0]
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	w := min(1080, size.X-32)
	side := min(float32(310), w/3)
	label := "Install " + a.c.Plugin.ID
	if a.c.Update() {
		label = "Update " + a.c.Plugin.ID
	}
	closed := widget.Scrim(c, "consent", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: consentHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if x.focus {
			c.RequestFocus(box)
			x.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(w), H: ui.Px(min(680, size.Y-32)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1,
			Radius: 10, Clip: true, Trap: true, Interactive: true, Focusable: true, KeyMode: ConsentKeyMode,
			Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}}, Role: ui.RoleDialog, Label: label, Help: consentHelp}, func(c *ui.Ctx) {
			x.header(c, a.c)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				c.Column("caps", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.Insets{L: 20, R: 20, T: 18, B: 18}, Gap: 8, ScrollY: true}, func(c *ui.Ctx) {
					consentRows(c, a.c, w-side-1-40)
				})
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("side", ui.Style{W: ui.Px(side), H: ui.Flex(1), Pad: ui.Pad(18), Gap: 10, ScrollY: true}, func(c *ui.Ctx) {
					consentSide(c, a.c, side-36)
				})
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.footer(c, a.c)
		})
		for _, k := range c.Interaction(box).Keys {
			if kit.Notation(k) == "<Esc>" {
				x.answer(false)
			}
		}
	})
	if closed {
		x.answer(false)
	}
}

// pluginName is the name part of an <owner>/<name> id.
func pluginName(id string) string {
	_, name, _ := strings.Cut(id, "/")
	return name
}

func (x *Consent) header(c *ui.Ctx, k plugin.Consent) {
	th := c.Theme()
	m := k.Plugin
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(68), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Row("icon", ui.Style{W: ui.Px(38), H: ui.Px(38), Radius: 8, Fill: th.C(theme.Accent), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(pluginName(m.ID)[:1]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 16, Weight: typeset.Bold},
				Color: th.C(theme.AccentInk)})
		})
		c.Column("titles", ui.Style{W: ui.Flex(1), Gap: 5, Clip: true}, func(c *ui.Ctx) {
			title := "Install " + pluginName(m.ID)
			if k.Update() {
				title = "Update " + pluginName(m.ID)
			}
			c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 16, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			meta := []string{m.ID}
			switch {
			case k.Update():
				meta = append(meta, "v"+k.From.Version+" → v"+m.Version)
			case m.Version != "":
				meta = append(meta, "v"+m.Version)
			}
			meta = append(meta, "runs: "+runsOf(m))
			c.Text("meta", strings.Join(meta, " · "), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
		if k.Badge != "" {
			widget.Chip(c, "badge", strings.ToUpper(k.Badge), th.C(badgeTone(k.Badge)), consentHelp+"#badges")
		}
		if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: consentHelp}) {
			x.answer(false)
		}
	})
}

func runsOf(m plugin.Manifest) string {
	if m.Runs == "" {
		return "app"
	}
	return m.Runs
}

// consentRows are the capabilities under their headings, each with its risk, Oriel's explanation and the
// author's reason.
func consentRows(c *ui.Ctx, k plugin.Consent, width float32) {
	th := c.Theme()
	intro := "Installing grants all of these. Anything not listed is denied. Explanations in plain type are Oriel's; italic lines are the author's own reasons."
	if k.Update() {
		intro = "This version asks for more. Updating grants all of these; staying keeps the version you have. Anything not listed is denied."
	}
	if k.Warning != "" {
		c.Row("warning", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 10, Stroke: th.C(tConsentWarning), Border: 1, Radius: 6,
			Role: ui.RoleGroup, Label: k.Warning}, func(c *ui.Ctx) {
			c.Text("sign", "⚠", kit.UIFont(12.5, th.C(tConsentWarning)))
			widget.Wrap(c, "t", k.Warning, width-60, kit.UIFont(12.5, th.C(tConsentWarning)))
		})
	}
	widget.Wrap(c, "intro", intro, width, kit.UIFont(12.5, th.C(theme.FgMuted)))
	for _, g := range plugin.Groups {
		var rows []plugin.ConsentRow
		for _, r := range k.Rows {
			if r.Group == g {
				rows = append(rows, r)
			}
		}
		if len(rows) == 0 {
			continue
		}
		c.Column(g, ui.Style{W: ui.Flex(1), Gap: 6, Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
			tone := theme.FgFaint
			if g == "Critical" {
				tone = tRiskCrit
			}
			c.Text("cap", strings.ToUpper(g), kit.KickerFont(th.C(tone)))
			for _, r := range rows {
				consentRow(c, k, r, width)
			}
		})
	}
	if len(k.Removed) > 0 {
		c.Column("removed", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
			c.Text("cap", "NO LONGER ASKED FOR", kit.KickerFont(th.C(theme.FgFaint)))
			widget.Wrap(c, "list", strings.Join(k.Removed, ", "), width, kit.MonoFont(11, th.C(theme.FgMuted)))
		})
	}
}

func consentRow(c *ui.Ctx, k plugin.Consent, r plugin.ConsentRow, width float32) {
	th := c.Theme()
	tone := th.C(riskTone(r.Info.Risk))
	border := th.C(tRiskRow)
	switch {
	case len(r.Added) > 0:
		border = th.C(theme.Accent)
	case r.Info.Risk == plugin.RiskCritical:
		border = tone
	}
	help := consentHelp + "#" + strings.ReplaceAll(string(r.Cap), ".", "-")
	c.Row(r.Cap, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 12, Cross: ui.Center, Stroke: border, Border: 1, Radius: 6,
		Role: ui.RoleGroup, Label: string(r.Cap), Help: help}, func(c *ui.Ctx) {
		c.Row("cap", ui.Style{W: ui.Px(170), Gap: 10, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			widget.Chip(c, "risk", riskWord(r.Info.Risk), tone, help)
			c.Text("name", string(r.Cap), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		})
		text := width - 170 - 12 - 28
		c.Column("what", ui.Style{W: ui.Flex(1), Gap: 5}, func(c *ui.Ctx) {
			if k.Update() && len(r.Added) > 0 {
				word := "more"
				if r.New() {
					word = "new"
				}
				c.Row("added", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					widget.Chip(c, "chip", word, th.C(theme.Accent), help)
					if !r.New() {
						c.Text("t", strings.Join(r.Added, ", "), kit.MonoFont(10.5, th.C(theme.Accent)))
					}
				})
			}
			widget.Wrap(c, "explain", r.Explain, text, kit.UIFont(12.5, th.C(theme.Fg)))
			if r.Why != "" {
				c.Row("why", ui.Style{Gap: 8}, func(c *ui.Ctx) {
					c.Text("by", "AUTHOR SAYS", kit.KickerFont(th.C(theme.FgGhost)))
					widget.Wrap(c, "t", r.Why, text-100, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11.5, Italic: true}, Color: th.C(theme.FgMuted)})
				})
			}
		})
	})
}

// consentSide is where the plugin runs, what's known of where it comes from, and what it gets without asking.
func consentSide(c *ui.Ctx, k plugin.Consent, width float32) {
	th := c.Theme()
	where := map[string]string{
		"app":     "In the app, as Lua or WASM: every call checked against these grants in the VM.",
		"session": "Inside each session's sandbox container: the worktree only, network through the egress proxy, no home directory.",
		"both":    "A UI part in the app (Lua or WASM, checked in the VM) and a session part inside each session's sandbox container: the worktree only, network through the egress proxy.",
	}[runsOf(k.Plugin)]
	c.Text("where", "WHERE IT RUNS", kit.KickerFont(th.C(theme.FgFaint)))
	widget.Wrap(c, "runs", where, width, kit.UIFont(12, th.C(theme.Fg)))
	c.Box("gap", ui.Style{H: ui.Px(6)}, nil)
	c.Text("prov", "PROVENANCE", kit.KickerFont(th.C(theme.FgFaint)))
	facts := append([]plugin.Fact{{Label: "source", Value: k.Source}}, k.Facts...)
	for i, f := range facts {
		c.Row(i, ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			c.Text("l", f.Label, kit.MonoFont(10.5, th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Box("v", ui.Style{Clip: true}, func(c *ui.Ctx) {
				c.Text("t", f.Value, kit.MonoFont(10.5, th.C(theme.Fg)))
			})
		})
	}
	c.Box("gap2", ui.Style{H: ui.Px(6)}, nil)
	c.Text("also", "ALSO", kit.KickerFont(th.C(theme.FgFaint)))
	widget.Wrap(c, "always", "UI, themes and notifications are always allowed, not listed. Updates that add capabilities ask again: take the update, or stay on this version.",
		width, kit.UIFont(12, th.C(theme.FgMuted)))
}

func (x *Consent) footer(c *ui.Ctx, k plugin.Consent) {
	th := c.Theme()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(56), Pad: ui.PadXY(20, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		parts := []string{"all or nothing", fmt.Sprintf("%d %s", len(k.Rows), kit.Plural(len(k.Rows), "capability", "capabilities"))}
		if k.Update() {
			n := 0
			for _, r := range k.Rows {
				if len(r.Added) > 0 {
					n++
				}
			}
			parts = append(parts, fmt.Sprintf("%d more than v%s", n, k.From.Version))
		}
		if n := k.Count(plugin.RiskCritical); n > 0 {
			parts = append(parts, fmt.Sprintf("%d critical", n))
		}
		parts = append(parts, "audit log in :Plugins")
		c.Box("about", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			c.Text("t", strings.Join(parts, " · "), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		})
		cancel, ok := "Cancel", "Install"
		if k.Update() {
			cancel, ok = "Stay on v"+k.From.Version, "Update"
		}
		if widget.Button(c, "cancel", cancel, widget.ButtonOpts{Key: "esc", Help: consentHelp}) {
			x.answer(false)
		}
		if widget.Button(c, "install", ok, widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: consentHelp}) {
			x.answer(true)
		}
	})
}

// Waiting is how many asks wait for an answer, the first on screen. Call it on the loop.
func (x *Consent) Waiting() int { return len(x.queue) }
