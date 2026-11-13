package kit

import (
	"context"
	"maps"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// AskTwins are "ask agent to explain" and "ask agent to fix" about t, side by side: explain sits next to fix
// everywhere. Explain answers under key, which ExplainKept draws where it was asked; fix writes a request.
func AskTwins(c *ui.Ctx, h plugin.Host, key string, t registry.Trouble, help string) {
	AskTwinsLabelled(c, h, key, t, help, "ask agent to explain", "ask agent to fix")
}

// AskTwinsLabelled is AskTwins with its buttons' own words ("Explain with agent", "Fix CI with agent").
func AskTwinsLabelled(c *ui.Ctx, h plugin.Host, key string, t registry.Trouble, help, explainLabel, fixLabel string) {
	fix := t.Args()
	explain := maps.Clone(fix)
	explain["key"] = key
	run := func(cmd string, a registry.Args) {
		if err := h.Run(context.Background(), cmd, a); err != nil {
			h.Notify(core.Notice{ID: cmd, Severity: core.SevWarning, Source: "explain", Key: true, Text: err.Error()})
		}
	}
	if widget.Button(c, "explain", explainLabel, widget.ButtonOpts{Disabled: !h.Available(registry.ExplainCommand, explain), Help: help}) {
		run(registry.ExplainCommand, explain)
	}
	if widget.Button(c, "fix", fixLabel, widget.ButtonOpts{Primary: true, Disabled: !h.Available(registry.FixCommand, fix), Help: help}) {
		run(registry.FixCommand, fix)
	}
}

// ExplainService draws the answers AskTwins asked for.
const ExplainService = "explain"

// Explains are the answers explain was asked for under a key.
type Explains interface {
	// Kept draws the answer asked for under key, once it was: an explain card that changes nothing.
	Kept(c *ui.Ctx, key, help string)
}

// ExplainKept draws the answer AskTwins asked for under key, if the window explains.
func ExplainKept(c *ui.Ctx, r registry.Registry, key, help string) {
	if x, ok := registry.ServiceOf[Explains](r, ExplainService); ok {
		x.Kept(c, key, help)
	}
}
