package agentui

import (
	"cmp"
	"errors"
	"fmt"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// RulesSection is Settings' section of the rules for all sessions.
const RulesSection = "rules"

const rulesHelp = "agent/asks#all-sessions"

// rulesCommands add and remove the rules for all sessions: what "Allow for all sessions" makes, met in every session
// before a card.
func (x *Permissions) rulesCommands() []registry.Command {
	return []registry.Command{
		{ID: "rules.add", Grant: "gate", Title: "Allow {rule} in every session without asking: {kind} permission (Bash(go test:*), Edit, a tool), gateway (a gateway's tool) or network (net(*.golang.org:443))",
			Run: func(_ registry.Ctx, a registry.Args) error {
				w, err := kit.SandboxCalls(x.h)
				if err != nil {
					return err
				}
				return w.AddRule(core.AskKind(cmp.Or(a.String("kind"), string(core.AskPermission))), a.String("rule"))
			}},
		{ID: "rules.remove", Grant: "gate", Title: "Stop allowing {rule} in every session: what it let through asks again; {kind} as rules.add's",
			Run: func(_ registry.Ctx, a registry.Args) error {
				rule := a.String("rule")
				if rule == "" {
					return errors.New("rules.remove needs a rule")
				}
				w, err := kit.SandboxCalls(x.h)
				if err != nil {
					return err
				}
				return w.RemoveRule(core.AskKind(cmp.Or(a.String("kind"), string(core.AskPermission))), rule)
			}},
	}
}

// rulesSettings is Settings › Rules: each rule for all sessions, what it answers and when it was made, with Remove.
func (x *Permissions) rulesSettings() uireg.SettingsSection {
	return uireg.SettingsSection{Name: RulesSection, Source: "builtin:permissions", Build: x.rulesSection,
		Count: func() int { return len(x.h.State().Rules) }}
}

func (x *Permissions) rulesSection(c *ui.Ctx) {
	th := c.Theme()
	rules := x.h.State().Rules
	widget.Wrap(c, "about", "What goes through in every session without asking: each made by Allow for all sessions on a prompt, and kept in "+
		"Oriel's home. A session's own rules (Allow for this session) are the session's, in its Permissions.", 700, kit.UIFont(12.5, th.C(theme.FgFaint)))
	if len(rules) == 0 {
		widget.Wrap(c, "none", "No rule for all sessions yet.", 700, kit.UIFont(12.5, th.C(theme.FgMuted)))
	}
	for _, r := range rules {
		c.Row(string(r.Kind)+"/"+r.Rule, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 8), Gap: 16, Cross: ui.Center, Help: rulesHelp, Role: ui.RoleGroup,
			Label: fmt.Sprintf("%s, %s", r.Rule, r.Kind)}, func(c *ui.Ctx) {
			c.Box("kind", ui.Style{W: ui.Px(96)}, func(c *ui.Ctx) {
				widget.Chip(c, "k", string(r.Kind), th.C(askTone(r.Kind)), rulesHelp)
			})
			c.Text("rule", r.Rule, kit.MonoFont(12.5, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if !r.Added.IsZero() {
				widget.Ago(c, "added", r.Added, widget.AgoOpts{Long: true, Prefix: "added ",
					Text: ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)}})
			}
			if widget.Button(c, "remove", "Remove", widget.ButtonOpts{Ghost: true, Help: rulesHelp}) {
				x.run("rules.remove", registry.Args{"kind": string(r.Kind), "rule": r.Rule})
			}
		})
	}
}
