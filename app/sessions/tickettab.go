package sessions

import (
	"cmp"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// ticketTab is a ticket in a tab of its own (kit.TicketView, its key the instance): what the tickets page shows of
// the one selected, with room to read its description.
type ticketTab struct{ x *Tickets }

func (v ticketTab) Title(key string) string { return cmp.Or(key, "Ticket") }
func (v ticketTab) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "ticket:" + vc.Instance, Fn: func(c *ui.Ctx) { v.x.tabView(c, vc.Instance) }}
}

// tabView is ticket key's tab: its tracker's view of it, the one listed while it's asked for the rest.
func (x *Tickets) tabView(c *ui.Ctx, key string) {
	th := c.Theme()
	c.Column("ticket", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 24, R: 24, T: 18, B: 24}, Gap: 12,
		Fill: th.C(theme.BgBase), Selectable: true, Role: ui.RoleRegion, Label: "Ticket " + key, Help: ticketsHelp + "#ticket"}, func(c *ui.Ctx) {
		tr, ok := registry.TrackerFor(x.h, key)
		if !ok {
			c.Text("none", "No ticket tracker takes "+key+".", kit.UIFont(13, th.C(theme.FgFaint)))
			return
		}
		t := registry.Ticket{Key: key}
		for _, r := range x.rows() {
			if r.ticket != nil && r.ticket.Key == key {
				t = *r.ticket
			}
		}
		d := x.detail(tr, key)
		x.mu.Lock()
		if d.done && d.err == "" {
			t = d.t
		}
		x.mu.Unlock()
		x.pageDetail(c, ticketRow{tracker: tr, ticket: &t})
	})
}
