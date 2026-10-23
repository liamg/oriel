package api

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=picker cap=ui
type Picker struct{ a *APIs }

//oriel:api doc="A source of results for the omnibox and the pickers: { id, title, prefix ('!' narrows the omnibox to it), order (50), picker_only, items = fn(query), run = fn(item), preview = fn(item), loading = fn() }. items returns a list of { label, detail, note, icon, icon_style, badge, badge_style, key, boost, id, ... }; the omnibox matches the query against label and detail, and the item comes back to run and preview as it was given, so keep your own fields on it. items runs as the omnibox draws: answer from what you have, and fetch in the background; while loading returns true the picker shows it's still loading, and says there are no matches only once it doesn't. preview returns oriel.ui nodes."
func (p Picker) Source(spec script.Table, where string) error {
	a := p.a
	id := str(spec, "id")
	items, _ := spec["items"].(*script.Fn)
	if id == "" || items == nil {
		return errors.New("a picker source needs an id and items")
	}
	s := uireg.PickerSource{ID: id, Title: cmpOr(str(spec, "title"), id), Prefix: str(spec, "prefix"), Order: 50, Source: where}
	if o, ok := spec["order"].(float64); ok {
		s.Order = int(o)
	}
	s.PickerOnly, _ = spec["picker_only"].(bool)
	s.Items = func(_ registry.Ctx, query string) []uireg.PickerItem {
		out, err := a.VM.Call(items, query)
		if err != nil {
			a.Host.Report("picker", id, err)
			return nil
		}
		list, _ := first(out).([]any)
		var res []uireg.PickerItem
		for _, v := range list {
			t, ok := v.(script.Table)
			if !ok {
				continue
			}
			it := uireg.PickerItem{ID: str(t, "id"), Label: str(t, "label"), Detail: str(t, "detail"), Note: str(t, "note"),
				Icon: str(t, "icon"), IconStyle: str(t, "icon_style"), Badge: str(t, "badge"), BadgeStyle: str(t, "badge_style"),
				Key: str(t, "key"), Boost: int(num(t["boost"])), Data: t}
			res = append(res, it)
		}
		return res
	}
	if loading, ok := spec["loading"].(*script.Fn); ok {
		s.Loading = func() bool {
			out, err := a.VM.Call(loading)
			if err != nil {
				a.Host.Report("picker", id, err)
				return false
			}
			busy, _ := first(out).(bool)
			return busy
		}
	}
	if run, ok := spec["run"].(*script.Fn); ok {
		s.Run = func(_ registry.Ctx, it uireg.PickerItem) error {
			_, err := a.VM.Call(run, it.Data)
			return err
		}
	}
	if prev, ok := spec["preview"].(*script.Fn); ok {
		s.Preview = func(it uireg.PickerItem) ui.Node {
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

//oriel:api doc="A ticket tracker (Linear, Jira…): Oriel builds the rest on it, the omnibox's tickets (!), the launcher's card, the moves as a session starts from a ticket and its PR is seen and merged, and the assistant's my_tickets. { name, title, icon (an icon's name, shown beside its tickets where more than one tracker's are listed), order (50), keys = a regular expression its keys match ('^[A-Z][A-Z0-9]*-[0-9]+$'; every key when nil), secret = the secret it needs set first, which Oriel asks for while it isn't, and the functions, each answering with done(value) or done(nil, err), now or later: lookup = fn(key, done) with a ticket; assigned = fn(done) with the open tickets assigned to you, in any order; start = fn(key, done), review = fn(key, done) and done = fn(key, done) moving it into progress, into review (its PR is open) or to done (the session's PRs are merged), with { moved, state }; link = fn(key, pr, done) attaching pr = { number, url, repo, title } }. Only lookup is required. A ticket is { key, title, url, state, stage ('todo', 'started' or 'review'), project, priority (1 the most urgent; 0 none), priority_label, blocked, blocked_by = { keys }, pr = { number, url, repo }, summary (the description's start, for a card), description (all of it, markdown, for the ticket's page), facts = { '2 attachments', … }, context }. context is markdown, the ticket with its comments and attachments, and goes to the agent with the prompt when a session starts from the ticket: hence agent.prompt."
func (t TicketAPI) Tracker(spec script.Table, where string) error {
	a := t.a
	name := str(spec, "name")
	lookup, _ := spec["lookup"].(*script.Fn)
	if name == "" || lookup == nil {
		return errors.New("a ticket tracker needs a name and lookup")
	}
	tr := registry.TicketTracker{Name: name, Title: str(spec, "title"), Icon: str(spec, "icon"), Order: 50, Secret: str(spec, "secret"), Source: where}
	if o, ok := spec["order"].(float64); ok {
		tr.Order = int(o)
	}
	if k := str(spec, "keys"); k != "" {
		re, err := regexp.Compile(k)
		if err != nil {
			return fmt.Errorf("the tracker's keys: %w", err)
		}
		tr.Keys = re
	}
	if tr.Secret != "" {
		if err := a.Plugin.Check(plugin.CapSecrets, tr.Secret); err != nil {
			return err
		}
	}
	tr.Lookup = func(ctx context.Context, key string) (registry.Ticket, error) {
		v, err := a.await(ctx, lookup, key)
		if err != nil {
			return registry.Ticket{}, err
		}
		tt, ok := v.(script.Table)
		if !ok {
			return registry.Ticket{}, errors.New("no ticket " + key)
		}
		return ticketOf(tt), nil
	}
	if fn, ok := spec["assigned"].(*script.Fn); ok {
		tr.Assigned = func(ctx context.Context) ([]registry.Ticket, error) {
			v, err := a.await(ctx, fn)
			if err != nil {
				return nil, err
			}
			list, _ := v.([]any)
			out := []registry.Ticket{}
			for _, x := range list {
				if tt, ok := x.(script.Table); ok {
					out = append(out, ticketOf(tt))
				}
			}
			return out, nil
		}
	}
	move := func(field string) func(ctx context.Context, key string) (registry.TicketMove, error) {
		fn, ok := spec[field].(*script.Fn)
		if !ok {
			return nil
		}
		return func(ctx context.Context, key string) (registry.TicketMove, error) {
			v, err := a.await(ctx, fn, key)
			if err != nil {
				return registry.TicketMove{}, err
			}
			tt, _ := v.(script.Table)
			moved, _ := tt["moved"].(bool)
			return registry.TicketMove{Moved: moved, State: str(tt, "state")}, nil
		}
	}
	tr.Start, tr.Review, tr.Done = move("start"), move("review"), move("done")
	if fn, ok := spec["link"].(*script.Fn); ok {
		tr.Link = func(ctx context.Context, key string, pr registry.TicketPR) error {
			_, err := a.await(ctx, fn, key, map[string]any{"number": pr.Number, "url": pr.URL, "repo": pr.Repo, "title": pr.Title})
			return err
		}
	}
	a.own(a.Host.Contribute(tr))
	return nil
}

// await calls a Lua function on the app's loop with args and a done(value) / done(nil, err) after them, and waits
// for its answer. Never call it on the loop.
func (a *APIs) await(ctx context.Context, fn *script.Fn, args ...any) (any, error) {
	type answer struct {
		v   any
		err error
	}
	ch := make(chan answer, 1)
	var once sync.Once
	done := script.GoFunc(func(c *script.Args) ([]any, error) {
		once.Do(func() {
			if c.Len() > 1 && c.Any(1) != nil {
				ch <- answer{err: errors.New(c.String(1))}
				return
			}
			ch <- answer{v: c.Any(0)}
		})
		return nil, nil
	})
	failed := make(chan error, 1)
	a.mu.Lock()
	if a.waiting == nil {
		a.waiting = map[int]chan error{}
	}
	a.n++
	key := a.n
	a.waiting[key] = failed
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.waiting, key)
		a.mu.Unlock()
	}()
	a.Host.Do(func() {
		if _, err := a.VM.Call(fn, append(args, done)...); err != nil {
			once.Do(func() { ch <- answer{err: err} })
		}
	})
	a.Host.Redraw()
	select {
	case r := <-ch:
		return r.v, r.err
	case err := <-failed:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// later calls fn, a callback the plugin gave an earlier call (an http.request's, a process request's), on the loop.
// An error in it is shown, and fails the calls still awaiting the plugin: their answer would have come from it.
func (a *APIs) later(fn *script.Fn, args ...any) {
	_, err := a.VM.Call(fn, args...)
	if err == nil || errors.Is(err, script.ErrClosed) {
		return
	}
	nt := core.Notice{ID: "lua.callback." + fn.Source, Severity: core.SevError, Source: fn.Source, Title: "Lua", Text: err.Error()}
	if e, ok := err.(*script.Error); ok {
		nt.Location, nt.Detail = e.Where(), e.Trace()
	}
	a.Host.Notify(nt)
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, ch := range a.waiting {
		select {
		case ch <- err:
		default:
		}
	}
}

func ticketOf(t script.Table) registry.Ticket {
	tk := registry.Ticket{Key: str(t, "key"), Title: str(t, "title"), URL: str(t, "url"), State: str(t, "state"), Stage: str(t, "stage"),
		Project: str(t, "project"), Priority: int(num(t["priority"])), PriorityLabel: str(t, "priority_label"), BlockedBy: strs(t["blocked_by"]),
		Summary: str(t, "summary"), Description: str(t, "description"), Facts: strs(t["facts"]), Context: str(t, "context")}
	tk.Blocked, _ = t["blocked"].(bool)
	tk.Blocked = tk.Blocked || len(tk.BlockedBy) > 0
	if pr, ok := t["pr"].(script.Table); ok {
		tk.PR = &registry.TicketPR{Number: int(num(pr["number"])), URL: str(pr, "url"), Repo: str(pr, "repo")}
	}
	return tk
}

func first(out []any) any {
	if len(out) == 0 {
		return nil
	}
	return out[0]
}
