package api

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

//oriel:api name=inbox cap=ui
type Inbox struct{ a *APIs }

//oriel:api doc="Adds a card to the inbox, or replaces the one with its id: { id, type, title, sub (its inbox row's second line), group (agents, bus, github or sandbox: its filter), mine, session, icon, tone (a theme token), kicker, detail, code, note, interrupt (stacks over the window while it waits), banner (on its own at the top), done (history only), expires (seconds from now: a countdown ring), actions = { { label, key, primary, command, args } } }. An action runs its command and answers the card: it goes to the inbox's history."
func (i Inbox) Add(card script.Table, where string) error {
	c := registry.InboxCard{ID: str(card, "id"), Type: str(card, "type"), Title: str(card, "title"), Sub: str(card, "sub"), Group: str(card, "group"),
		Session: str(card, "session"), Icon: str(card, "icon"), Tone: str(card, "tone"), Kicker: str(card, "kicker"), Detail: str(card, "detail"),
		Code: str(card, "code"), Note: str(card, "note"), Created: time.Now()}
	if c.ID == "" || c.Title == "" {
		return errors.New("an inbox card needs an id and a title")
	}
	c.Mine, _ = card["mine"].(bool)
	c.Interrupt, _ = card["interrupt"].(bool)
	c.Banner, _ = card["banner"].(bool)
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
		a := registry.InboxAction{Label: str(t, "label"), Key: str(t, "key"), Command: str(t, "command")}
		a.Primary, _ = t["primary"].(bool)
		if args, ok := t["args"].(script.Table); ok {
			a.Args = registry.Args(args)
		}
		id, cmd, args := c.ID, a.Command, a.Args
		a.Run = func() {
			if cmd != "" {
				i.a.Host.Run(context.Background(), cmd, args)
			}
			i.Remove(id)
		}
		c.Actions = append(c.Actions, a)
	}
	i.a.mu.Lock()
	defer i.a.mu.Unlock()
	if i.a.inbox == nil {
		i.a.inbox = &luaInbox{}
		i.a.owned = append(i.a.owned, i.a.Host.Contribute(registry.InboxSource{Name: "lua:" + i.a.Plugin.ID, Cards: i.a.inbox.list, Source: where}))
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
	cards []registry.InboxCard
}

func (in *luaInbox) put(c registry.InboxCard) {
	in.mu.Lock()
	defer in.mu.Unlock()
	if j := slices.IndexFunc(in.cards, func(x registry.InboxCard) bool { return x.ID == c.ID }); j >= 0 {
		c.Created = in.cards[j].Created
		in.cards[j] = c
		return
	}
	in.cards = append(in.cards, c)
}

func (in *luaInbox) drop(id string) {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.cards = slices.DeleteFunc(in.cards, func(x registry.InboxCard) bool { return x.ID == id })
}

func (in *luaInbox) list() []registry.InboxCard {
	in.mu.Lock()
	defer in.mu.Unlock()
	return slices.Clone(in.cards)
}
