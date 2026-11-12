package api

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/ui"
)

//oriel:api name=inbox cap=ui
type Inbox struct{ a *APIs }

//oriel:api doc="Adds a card to the inbox, or replaces the one with its id: { id, type, title, sub (its inbox row's second line), group (agents, bus, github or sandbox: its filter), mine, session, icon, tone (a theme token), kicker, detail, code, note, interrupt (stacks over the window while it waits), banner (on its own at the top), prompt (in the middle of the window, one at a time, while its session is in front and once typing pauses; its tab calls for you while it is behind), done (history only), expires (seconds from now: a countdown ring), body (oriel.ui nodes under its text, or fn() returning them: choices, a field to type an answer), actions = { { label, key, primary, command, args, run = fn() } } }. An action runs its command or its function and answers the card: it goes to the inbox's history."
func (i Inbox) Add(card script.Table, where string) error {
	c := uireg.InboxCard{ID: str(card, "id"), Type: str(card, "type"), Title: str(card, "title"), Sub: str(card, "sub"), Group: str(card, "group"),
		Session: str(card, "session"), Icon: str(card, "icon"), Tone: str(card, "tone"), Kicker: str(card, "kicker"), Detail: str(card, "detail"),
		Code: str(card, "code"), Note: str(card, "note"), Created: time.Now()}
	if u := i.a.Host.UI(); u != nil {
		c.Created = u.Now() // expires counts from the window's clock, as oriel.now does
	}
	if c.ID == "" || c.Title == "" {
		return errors.New("an inbox card needs an id and a title")
	}
	c.Mine, _ = card["mine"].(bool)
	c.Interrupt, _ = card["interrupt"].(bool)
	c.Banner, _ = card["banner"].(bool)
	c.Prompt, _ = card["prompt"].(bool)
	c.Done, _ = card["done"].(bool)
	if s, ok := card["expires"].(float64); ok && s > 0 {
		c.Expires = c.Created.Add(time.Duration(s * float64(time.Second)))
	}
	acts, _ := card["actions"].([]any)
	for _, v := range acts {
		t, ok := v.(script.Table)
		if !ok {
			continue
		}
		a := uireg.InboxAction{Label: str(t, "label"), Key: str(t, "key"), Command: str(t, "command")}
		a.Primary, _ = t["primary"].(bool)
		if args, ok := t["args"].(script.Table); ok {
			a.Args = registry.Args(args)
		}
		id, cmd, args := c.ID, a.Command, a.Args
		run, _ := t["run"].(*script.Fn)
		a.Run = func() {
			if run != nil {
				i.a.callback(t, "run")
			} else if cmd != "" {
				i.a.Host.Run(context.Background(), cmd, args)
			}
			i.Remove(id)
		}
		c.Actions = append(c.Actions, a)
	}
	if body, ok := card["body"]; ok && body != nil {
		c.Body = func(cx *ui.Ctx) {
			v := body
			if fn, ok := body.(*script.Fn); ok {
				out, err := i.a.VM.Call(fn)
				if v = nil; len(out) > 0 {
					v = out[0]
				}
				if err != nil {
					v = script.Table{"type": "text", "text": "✕ " + err.Error(), "style": "error"}
				}
			}
			cx.Column(nil, ui.Style{W: ui.Flex(1), Gap: 6}, func(cx *ui.Ctx) { build(cx, v, i.a) })
		}
	}
	i.a.mu.Lock()
	defer i.a.mu.Unlock()
	if i.a.inbox == nil {
		i.a.inbox = &luaInbox{}
		i.a.owned = append(i.a.owned, i.a.Host.Contribute(uireg.InboxSource{Name: "lua:" + i.a.Plugin.ID, Cards: i.a.inbox.list, Source: where}))
	}
	i.a.inbox.put(c)
	i.a.Host.Redraw()
	return nil
}

//oriel:api doc="Takes the inbox card id away: answered, it stays in the inbox's history."
func (i Inbox) Remove(id string) error {
	i.a.mu.Lock()
	in := i.a.inbox
	i.a.mu.Unlock()
	if in != nil {
		in.drop(id)
		i.a.Host.Redraw()
	}
	return nil
}

// luaInbox is a VM's inbox cards.
type luaInbox struct {
	mu    sync.Mutex
	cards []uireg.InboxCard
}

func (in *luaInbox) put(c uireg.InboxCard) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if j := slices.IndexFunc(in.cards, func(x uireg.InboxCard) bool { return x.ID == c.ID }); j >= 0 {
		c.Created = in.cards[j].Created
		in.cards[j] = c
		return
	}
	in.cards = append(in.cards, c)
}

func (in *luaInbox) drop(id string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.cards = slices.DeleteFunc(in.cards, func(x uireg.InboxCard) bool { return x.ID == id })
}

func (in *luaInbox) list() []uireg.InboxCard {
	in.mu.Lock()
	defer in.mu.Unlock()
	return slices.Clone(in.cards)
}
