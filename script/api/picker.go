package api

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=picker cap=ui
type Picker struct{ a *APIs }

//oriel:api doc="A source of results for the omnibox and the pickers (ui.md 02): { id, title, prefix ('!' narrows the omnibox to it), order (50), picker_only, items = fn(query), run = fn(item), preview = fn(item) }. items returns a list of { label, detail, note, icon, icon_style, badge, badge_style, key, boost, id, ... }; the omnibox matches the query against label and detail, and the item comes back to run and preview as it was given, so keep your own fields on it. items runs as the omnibox draws: answer from what you have, and fetch in the background. preview returns oriel.ui nodes."
func (p Picker) Source(spec script.Table, where string) error {
	a := p.a
	id := str(spec, "id")
	items, _ := spec["items"].(*script.Fn)
	if id == "" || items == nil {
		return errors.New("a picker source needs an id and items")
	}
	s := registry.PickerSource{ID: id, Title: cmpOr(str(spec, "title"), id), Prefix: str(spec, "prefix"), Order: 50, Source: where}
	if o, ok := spec["order"].(float64); ok {
		s.Order = int(o)
	}
	s.PickerOnly, _ = spec["picker_only"].(bool)
	s.Items = func(_ registry.Ctx, query string) []registry.PickerItem {
		out, err := a.VM.Call(items, query)
		if err != nil {
			a.Host.Report("picker", id, err)
			return nil
		}
		list, _ := first(out).([]any)
		var res []registry.PickerItem
		for _, v := range list {
			t, ok := v.(script.Table)
			if !ok {
				continue
			}
			it := registry.PickerItem{ID: str(t, "id"), Label: str(t, "label"), Detail: str(t, "detail"), Note: str(t, "note"),
				Icon: str(t, "icon"), IconStyle: str(t, "icon_style"), Badge: str(t, "badge"), BadgeStyle: str(t, "badge_style"),
				Key: str(t, "key"), Boost: int(num(t["boost"])), Data: t}
			res = append(res, it)
		}
		return res
	}
	if run, ok := spec["run"].(*script.Fn); ok {
		s.Run = func(_ registry.Ctx, it registry.PickerItem) error {
			_, err := a.VM.Call(run, it.Data)
			return err
		}
	}
	if prev, ok := spec["preview"].(*script.Fn); ok {
		s.Preview = func(it registry.PickerItem) ui.Node {
			out, err := a.VM.Call(prev, it.Data)
			if err != nil {
				a.Host.Report("picker", id, err)
				return nil
			}
			v := first(out)
			return &ui.Func{Key: "preview", Fn: func(c *ui.Ctx) { build(c, v, a) }}
		}
	}
	a.own(a.Host.Picker(s))
	return nil
}

//oriel:api name=ticket cap=agent.prompt
type TicketAPI struct{ a *APIs }

//oriel:api doc="A tracker's tickets, for the launcher's ticket field (ui.md 04: a ticket ID expands into a card): { name, title, order (50), match = fn(key) → bool, lookup = fn(key, done) }. lookup answers with done(ticket) or done(nil, err), now or later; ticket is { key, title, url, state, state_tone, summary, facts = { '2 attachments', … }, context }. context is markdown, the ticket with its comments and attachments, and goes to the agent with the prompt when a session starts from the ticket: hence agent.prompt."
func (t TicketAPI) Source(spec script.Table, where string) error {
	a := t.a
	name := str(spec, "name")
	lookup, _ := spec["lookup"].(*script.Fn)
	if name == "" || lookup == nil {
		return errors.New("a ticket source needs a name and lookup")
	}
	s := registry.TicketSource{Name: name, Title: str(spec, "title"), Order: 50, Source: where}
	if o, ok := spec["order"].(float64); ok {
		s.Order = int(o)
	}
	if m, ok := spec["match"].(*script.Fn); ok {
		s.Match = func(key string) bool {
			out, err := a.VM.Call(m, key)
			if err != nil {
				a.Host.Report("ticket.source", name, err)
				return false
			}
			ok, _ := first(out).(bool)
			return ok
		}
	}
	s.Lookup = func(ctx context.Context, key string) (registry.Ticket, error) {
		type answer struct {
			t   registry.Ticket
			err error
		}
		ch := make(chan answer, 1)
		var once sync.Once
		done := script.GoFunc(func(c *script.Args) ([]any, error) {
			once.Do(func() {
				if tt := c.Table(0); tt != nil {
					ch <- answer{t: ticketOf(tt)}
					return
				}
				msg := c.String(1)
				if msg == "" {
					msg = "no ticket " + key
				}
				ch <- answer{err: errors.New(msg)}
			})
			return nil, nil
		})
		a.Host.Do(func() {
			if _, err := a.VM.Call(lookup, key, done); err != nil {
				once.Do(func() { ch <- answer{err: err} })
			}
		})
		a.Host.Redraw()
		select {
		case r := <-ch:
			return r.t, r.err
		case <-ctx.Done():
			return registry.Ticket{}, fmt.Errorf("looking up %s: %w", key, ctx.Err())
		}
	}
	a.own(a.Host.Contribute(s))
	return nil
}

func ticketOf(t script.Table) registry.Ticket {
	return registry.Ticket{Key: str(t, "key"), Title: str(t, "title"), URL: str(t, "url"), State: str(t, "state"),
		StateTone: str(t, "state_tone"), Summary: str(t, "summary"), Facts: strs(t["facts"]), Context: str(t, "context")}
}

func first(out []any) any {
	if len(out) == 0 {
		return nil
	}
	return out[0]
}
