package stats

import (
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// pageAccounts are the accounts the Usage page offers, by provider: each one read, and a provider being read or
// failed with none read yet, as an account with no windows.
func (x *Plugin) pageAccounts() []core.ProviderUsage {
	st := x.h.State()
	us := slices.Clone(st.Usage)
	pending := slices.Clone(st.UsageReading)
	for _, f := range st.UsageFailures {
		pending = append(pending, f.Provider)
	}
	for _, p := range pending {
		if !slices.ContainsFunc(us, func(u core.ProviderUsage) bool { return u.Provider == p }) {
			us = append(us, core.ProviderUsage{Provider: p})
		}
	}
	slices.SortStableFunc(us, func(a, b core.ProviderUsage) int { return strings.Compare(a.Provider, b.Provider) })
	return us
}

// sessionUsage is the one of us the session in front runs on: its provider's, its account's when known.
func (x *Plugin) sessionUsage(us []core.ProviderUsage) (core.ProviderUsage, bool) {
	st := x.h.State()
	se, ok := st.Session(st.Focus)
	if !ok {
		return core.ProviderUsage{}, false
	}
	s := se.Snapshot()
	acct := ""
	if n := len(s.Agent.Accounts); n > 0 {
		acct = s.Agent.Accounts[n-1].Account
	}
	for _, v := range us {
		if v.Provider == s.Agent.Provider && (acct == "" || v.Account == "" || v.Account == acct) {
			return v, true
		}
	}
	return core.ProviderUsage{}, false
}

// unread is a provider's section with no windows read yet: being read, or why the read failed.
func (x *Plugin) unread(c *ui.Ctx, u core.ProviderUsage) {
	th := c.Theme()
	st := x.h.State()
	name := kit.ProviderName(x.h, u.Provider)
	c.Column("unread", ui.Style{W: ui.Flex(1), Gap: 10, Role: ui.RoleGroup, Label: name}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			providerMark(c, u.Provider)
			c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 15, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
		})
		if slices.Contains(st.UsageReading, u.Provider) {
			widget.Busy(c, "busy", widget.LoadState{Phase: widget.Pending}, widget.BusyOpts{Label: "Reading " + name + "'s plan windows", Clock: -1,
				Help: "agent/usage"})
			widget.Skeleton(c, "windows", widget.SkeletonOpts{Block: render.Point{X: 420, Y: 120}, Count: 2, Dir: ui.Row, Gap: 12,
				Width: ui.Flex(1), Label: "Reading " + name + "'s plan windows", Help: "agent/usage"})
			return
		}
		for i, f := range st.UsageFailures {
			if f.Provider == u.Provider {
				c.Box(fmt.Sprint("fail", i), ui.Style{Tooltip: f.Err}, func(c *ui.Ctx) {
					c.Text("t", failureText(x.h, f), kit.MonoFont(11, th.C(theme.Warn)))
				})
			}
		}
	})
}
