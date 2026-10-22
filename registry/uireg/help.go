package uireg

import (
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
)

// liveHelp is a help page's live block, as the registry keeps it.
type liveHelp struct {
	name string
	draw func(c *ui.Ctx)
}

func (liveHelp) ContributionKind() string   { return "help.live" }
func (l liveHelp) ContributionName() string { return l.name }

func (r *reg) HelpLive(name string, fn func(c *ui.Ctx)) registry.Disposable {
	return r.Contribute(liveHelp{name, fn})
}

func (r *reg) HelpLiveByName(name string) (func(c *ui.Ctx), bool) {
	c, ok := r.Contribution("help.live", name)
	if !ok {
		return nil, false
	}
	return c.(liveHelp).draw, true
}
