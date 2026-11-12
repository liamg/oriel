package agentui

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// The inbox: every InboxSource's cards. Those waiting stack at the window's bottom right (a banner one,
// such as a security-key touch, on its own at the top), the session tabs count theirs, and the Inbox panel keeps
// them all, answered ones too, as history.
const (
	InboxView    = "inbox"
	InboxKeyMode = "inbox"
	inboxHelp    = "shell/inbox"
	// maxInterrupts is how many cards stack at once over the window; the rest wait behind a count.
	maxInterrupts = 4
	// maxInboxHistory is how many answered cards the Inbox panel keeps.
	maxInboxHistory = 200
)

var (
	tInboxCount  = theme.Declare("inbox.count", "status.permission")
	tInboxRowSel = theme.DeclareOn("inbox.row.selected", "alpha(accent, 6%)", "bg.surface")
	tInboxDone   = theme.Declare("inbox.done", "fg.ghost")
)

// inboxFilters are the Inbox panel's filters: their labels, and the cards each lets through.
var inboxFilters = []struct {
	label string
	keep  func(uireg.InboxCard) bool
}{
	{"All", func(uireg.InboxCard) bool { return true }},
	{"Me", func(c uireg.InboxCard) bool { return c.Mine }},
	{"Agents", func(c uireg.InboxCard) bool { return c.Group == uireg.GroupAgents }},
	{"Bus", func(c uireg.InboxCard) bool { return c.Group == uireg.GroupBus }},
	{"GitHub", func(c uireg.InboxCard) bool { return c.Group == uireg.GroupGitHub }},
	{"Sandbox", func(c uireg.InboxCard) bool { return c.Group == uireg.GroupSandbox }},
}

// Inbox is every InboxSource's cards: stacked over the window while they wait, and kept in the Inbox panel.
type Inbox struct {
	kit.Disposer
	h plugin.Host

	mu        sync.Mutex
	rows      map[string]*inboxRow // by card key
	order     []string             // card keys, as first seen
	dismissed map[string]bool      // taken off the stack; they wait on in the panel
	inline    map[string]bool      // shown by a surface of their own, so not stacked
	filter    int
	sel       string // the panel's selected row, its card open under it
	list      kit.Scroll
	panelLeft float32 // where the Inbox panel's left edge was last drawn, so the stack sits beside it

	front    core.SessionID // the session in front as the overlay last saw it
	frontAt  time.Time      // when it came to the front, on the UI's clock
	promptUp time.Time      // when a prompt was last up, on the UI's clock
	prompted string         // the prompt up in the last frame, by key; "" for none
}

// inboxRow is a card as the inbox last had it, and whether its source has let it go (answered: history now).
type inboxRow struct {
	card  uireg.InboxCard
	seen  time.Time
	shown time.Time // when it first went over the window: once typing paused
	gone  bool
}

// typingPause is how long typing must stop before a card goes over the window, so it doesn't cover the text being
// written or catch a key meant for it.
const typingPause = 1200 * time.Millisecond

// PromptPauseOption is how long no key must go down before a Prompt card comes up.
const PromptPauseOption = "inbox.prompt_pause"

func (r *inboxRow) waiting() bool { return !r.gone && !r.card.Done }

func (r *inboxRow) when() time.Time {
	if !r.card.Created.IsZero() {
		return r.card.Created
	}
	return r.seen
}

func (x *Inbox) Manifest() plugin.Manifest { return plugin.Manifest{ID: "inbox"} }

func (x *Inbox) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.rows, x.dismissed = map[string]*inboxRow{}, map[string]bool{}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(h.View(InboxView, uireg.ViewFunc(func(vc uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "inbox", Fn: func(c *ui.Ctx) { x.panel(c, vc) }}
	})))
	x.Add(h.View(kit.OverlayPrefix+"inbox", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "inbox", Fn: x.overlay}
	})))
	x.Add(h.Contribute(registry.Service{Name: kit.InboxService, Impl: inboxService{x}, Source: "builtin:inbox"}))
	x.Add(h.Option(registry.OptionSpec{Name: PromptPauseOption, Type: registry.Duration, Default: 3 * time.Second, Scopes: registry.ScopeUser,
		Validate: func(v any) error {
			if v.(time.Duration) < 0 {
				return fmt.Errorf("a pause can't be negative")
			}
			return nil
		},
		Help: "How long no key must go down before a prompt (an agent's permission) comes up in the middle of the window, so it never takes a key meant for what you're typing. 0 shows it at once."}))
	x.Add(h.Contribute(uireg.Indicator{Name: "inbox", Slot: uireg.SlotTitleBar, Global: true, Order: 80, Help: inboxHelp + "#title-bar",
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "inbox", Fn: x.indicator}, nil }}))
	return nil
}

func (x *Inbox) keymaps() []registry.Keymap {
	return []registry.Keymap{
		{Mode: "global", Keys: "<D-I>", Command: "inbox.open", Desc: "Inbox"},
		{Mode: "n", Keys: "<Leader>i", Command: "inbox.open", Desc: "Inbox"},
		{Mode: InboxKeyMode, Keys: "j", Command: "inbox.next", Desc: "Next card"},
		{Mode: InboxKeyMode, Keys: "<Down>", Command: "inbox.next", Desc: "Next card"},
		{Mode: InboxKeyMode, Keys: "k", Command: "inbox.prev", Desc: "Card before"},
		{Mode: InboxKeyMode, Keys: "<Up>", Command: "inbox.prev", Desc: "Card before"},
		{Mode: InboxKeyMode, Keys: "<Esc>", Command: "view.close", Args: registry.Args{"docked": true}, Desc: "Close the inbox"},
		{Mode: InboxKeyMode, Keys: "q", Command: "view.close", Desc: "Close"},
		{Mode: InboxKeyMode, Keys: "<C-w>c", Command: "view.close", Desc: "Close"},
		{Mode: InboxKeyMode, Keys: "<C-w>q", Command: "view.close", Desc: "Close"},
	}
}

func (x *Inbox) commands() []registry.Command {
	h := x.h
	open := registry.Args{"id": InboxView, "place": "dock", "edge": "right", "size": 380, "title": "Inbox"}
	return []registry.Command{
		{ID: "inbox.open", Title: "Show the Inbox panel: everything waiting on you, and what's been answered; or hide it",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				return h.Run(ctx, "view.toggle", open)
			}},
		{ID: "inbox.dismiss", Title: "Take a card off the window: {id} (source/id), or the prompt up, or the top of the stack; it waits on in the Inbox panel",
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := a.String("id")
				if id == "" {
					x.mu.Lock()
					id = x.prompted
					x.mu.Unlock()
				}
				if id == "" {
					if st := x.stacked(); len(st) > 0 {
						id = st[0].card.Key()
					}
				}
				if id == "" {
					return fmt.Errorf("no card is stacked over the window")
				}
				return x.dismiss(id)
			}},
		{ID: "inbox.next", Title: "Inbox: the next card", Run: func(registry.Ctx, registry.Args) error { x.move(1); return nil }},
		{ID: "inbox.prev", Title: "Inbox: the card before", Run: func(registry.Ctx, registry.Args) error { x.move(-1); return nil }},
	}
}

// gather reads every source again: new cards join the rows, and those a source has let go become history.
func (x *Inbox) gather() []*inboxRow {
	cards := uireg.InboxCards(x.h)
	x.mu.Lock()
	defer x.mu.Unlock()
	now := time.Now()
	live := map[string]bool{}
	for _, cd := range cards {
		k := cd.Key()
		live[k] = true
		r := x.rows[k]
		if r == nil {
			r = &inboxRow{seen: now}
			x.rows[k] = r
			x.order = append(x.order, k)
		}
		r.card, r.gone = cd, false
	}
	gone := 0
	for k, r := range x.rows {
		if !live[k] {
			if !r.gone {
				r.gone, r.card.Draw, r.card.Body = true, nil, nil
				delete(x.dismissed, k)
			}
		}
		if !r.waiting() {
			gone++
		}
	}
	for i := 0; gone > maxInboxHistory && i < len(x.order); {
		k := x.order[i]
		if r := x.rows[k]; !r.waiting() {
			delete(x.rows, k)
			x.order = slices.Delete(x.order, i, i+1)
			gone--
			continue
		}
		i++
	}
	out := make([]*inboxRow, 0, len(x.order))
	for _, k := range x.order {
		out = append(out, x.rows[k])
	}
	return out
}

// stacked are the cards over the window: waiting, to interrupt, not dismissed; banner ones first. Prompts aren't
// stacked: they come up one at a time (prompt).
func (x *Inbox) stacked() []*inboxRow {
	var out []*inboxRow
	for _, r := range x.gather() {
		if r.waiting() && !r.card.Prompt && (r.card.Interrupt || r.card.Banner) && !x.isDismissed(r.card.Key()) && (r.card.Banner || !x.isInline(r.card.Key())) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b *inboxRow) int {
		switch {
		case a.card.Banner == b.card.Banner:
			return 0
		case a.card.Banner:
			return -1
		}
		return 1
	})
	return out
}

func (x *Inbox) isDismissed(k string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.dismissed[k]
}

func (x *Inbox) isInline(k string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.inline[k]
}

// showInline keeps the cards keyed in keys off the stack while a surface draws them itself;
// nil gives them back. A banner card stays at the top whatever shows it.
func (x *Inbox) showInline(keys map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.inline = keys
}

// waiting are the cards waiting on you, in the order they came.
func (x *Inbox) waiting() []*inboxRow {
	var out []*inboxRow
	for _, r := range x.gather() {
		if r.waiting() {
			out = append(out, r)
		}
	}
	return out
}

// dismiss takes a card off the stack, and tells its source.
func (x *Inbox) dismiss(k string) error {
	x.gather()
	x.mu.Lock()
	r := x.rows[k]
	if r == nil || !r.waiting() {
		x.mu.Unlock()
		return fmt.Errorf("no card %q is waiting", k)
	}
	x.dismissed[k] = true
	fn := r.card.Dismissed
	x.mu.Unlock()
	if fn != nil {
		registry.Call(func() error { fn(); return nil })
	}
	return nil
}

// inboxService is the inbox as kit.Inbox.
type inboxService struct{ x *Inbox }

func (s inboxService) Waiting() []kit.WaitingCard {
	var out []kit.WaitingCard
	for _, r := range s.x.waiting() {
		out = append(out, kit.WaitingCard{Card: r.card, At: r.when()})
	}
	return out
}

func (s inboxService) Count(session string) (int, string) { return s.x.Count(session) }
func (s inboxService) Calling(session string) (string, string, bool) {
	return s.x.Calling(session)
}
func (s inboxService) ShowInline(keys map[string]bool) { s.x.showInline(keys) }
func (s inboxService) Card(c *ui.Ctx, card uireg.InboxCard) {
	s.x.card(c, &inboxRow{card: card}, false)
}

// Count is how many cards wait in session, and the tone of the first: its tab's badge.
func (x *Inbox) Count(session string) (n int, tone string) {
	for _, r := range x.gather() {
		if r.waiting() && r.card.Session == session {
			if n == 0 {
				tone = r.card.Tone
			}
			n++
		}
	}
	return n, tone
}

// Calling says a Prompt card waits in session while another is in front: the newest's key, and its tone.
func (x *Inbox) Calling(session string) (key, tone string, ok bool) {
	if session == "" || core.SessionID(session) == x.h.State().Focus {
		return "", "", false
	}
	for _, r := range x.gather() {
		if r.waiting() && r.card.Prompt && r.card.Session == session {
			key, tone, ok = r.card.Key(), r.card.Tone, true
		}
	}
	return key, tone, ok
}

// prompt is the Prompt card up in the middle of the window now, if any: the first waiting for the session in front
// (or none), not put off, once no key has gone down for the pause; at once if none has since the session came to
// the front or a prompt was last up, as the keys that answered that one were its, not typing. Coming to the front
// brings back the session's prompts that were put off.
func (x *Inbox) prompt(c *ui.Ctx) *inboxRow {
	now := c.Now()
	focus := x.h.State().Focus
	rows := x.gather()
	x.mu.Lock()
	if focus != x.front {
		x.front, x.frontAt = focus, now
		for _, r := range rows {
			if r.card.Prompt && r.card.Session == string(focus) {
				delete(x.dismissed, r.card.Key())
			}
		}
	}
	frontAt, promptUp := x.frontAt, x.promptUp
	var next *inboxRow
	for _, r := range rows {
		if !r.waiting() || !r.card.Prompt || x.dismissed[r.card.Key()] || r.card.Session != "" && r.card.Session != string(focus) {
			continue
		}
		if next == nil || !r.shown.IsZero() && next.shown.IsZero() {
			next = r
		}
	}
	x.prompted = ""
	x.mu.Unlock()
	if next == nil {
		return nil
	}
	if next.shown.IsZero() {
		last, pause := c.LastKey(), x.promptPause()
		if !last.IsZero() && last.After(frontAt) && last.After(promptUp) && now.Sub(last) < pause {
			c.WakeAt(last.Add(pause))
			return nil
		}
		next.shown = now
	}
	x.mu.Lock()
	x.promptUp, x.prompted = now, next.card.Key()
	x.mu.Unlock()
	return next
}

func (x *Inbox) promptPause() time.Duration {
	v, _, _ := x.h.Options().Get(PromptPauseOption, registry.At{})
	d, _ := v.(time.Duration)
	return d
}

// promptDialog draws a Prompt card in the middle of the window over a scrim, its keys answering it; closing it puts
// it off.
func (x *Inbox) promptDialog(c *ui.Ctx, r *inboxRow) {
	k := r.card.Key()
	place := uireg.CardPlace{Stacked: true, TakeFocus: true, Dismiss: func() { x.dismiss(k) }}
	if widget.Dialog(c, "prompt", widget.DialogOpts{Label: cmp.Or(r.card.Title, r.card.Kicker), Help: inboxHelp + "#prompts"}, func(c *ui.Ctx) {
		c.Box(k, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.draw(c, r, place) })
	}) {
		x.dismiss(k)
	}
}

// waitingCount is how many cards wait, in every session and none.
func (x *Inbox) waitingCount() int {
	n := 0
	for _, r := range x.gather() {
		if r.waiting() {
			n++
		}
	}
	return n
}

// overlay is the banner cards at the top and the stack at the bottom right.
func (x *Inbox) overlay(c *ui.Ctx) {
	all := slices.DeleteFunc(x.stacked(), func(r *inboxRow) bool {
		if !r.shown.IsZero() || r.card.Dialog { // a dialog answers what was just done: it shows at once
			r.shown = cmp.Or(r.shown, c.Now())
			return false
		}
		if last := c.LastTyped(); !last.IsZero() && c.Now().Sub(last) < typingPause {
			c.WakeAt(last.Add(typingPause))
			return true
		}
		r.shown = c.Now()
		return false
	})
	var banner, stack, dialog []*inboxRow
	for _, r := range all {
		if r.card.Dialog {
			dialog = append(dialog, r)
		} else if r.card.Banner || r.card.Top {
			banner = append(banner, r)
		} else {
			stack = append(stack, r)
		}
	}
	if len(banner) > 0 {
		top := widget.AreaFloat(c, widget.AreaTop, render.Point{X: .5, Y: 1}, render.Point{X: .5}, render.Point{Y: 10})
		c.Column("banner", ui.Style{W: ui.Px(440), Gap: 8, Z: ui.LayerToast, Float: top,
			Role: ui.RoleGroup, Label: "Needs you now"}, func(c *ui.Ctx) {
			for _, r := range banner {
				x.card(c, r, true)
			}
		})
	}
	if len(dialog) > 0 {
		x.mu.Lock()
		x.prompted = ""
		x.mu.Unlock()
		x.dialog(c, dialog[0])
	} else if p := x.prompt(c); p != nil {
		x.promptDialog(c, p)
	}
	if len(stack) == 0 {
		return
	}
	th := c.Theme()
	corner := widget.AreaFloat(c, widget.AreaMain, render.Point{X: 1, Y: 1}, render.Point{X: 1, Y: 1}, render.Point{X: -16, Y: -10})
	if x.panelShown() {
		x.mu.Lock()
		if main, ok := widget.AreaRect(c, widget.AreaMain); ok && x.panelLeft > 0 {
			corner.Offset.X = min(corner.Offset.X, x.panelLeft-16-main.Max.X)
		}
		x.mu.Unlock()
	}
	c.Column("asks", ui.Style{W: ui.Px(440), Gap: 8, Z: ui.LayerToast, Float: corner,
		Role: ui.RoleGroup, Label: "Waiting on you"}, func(c *ui.Ctx) {
		// While a card waits at the top, the stack holds back to a count: one thing at a time, and nothing over it.
		shown := maxInterrupts
		if len(banner) > 0 {
			shown = 0
		}
		for _, r := range stack[:min(len(stack), shown)] {
			x.card(c, r, true)
		}
		if n := len(stack) - shown; n > 0 {
			c.Row("more", ui.Style{W: ui.Flex(1), Main: ui.End, Cross: ui.Center, Gap: 8}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("+%d more waiting", n), kit.MonoFont(10, th.C(theme.FgFaint)))
				if widget.Button(c, "open", "Open the inbox", widget.ButtonOpts{Ghost: true, Key: kit.BoundKey(x.h, "inbox.open", nil), Help: inboxHelp}) {
					x.h.Run(context.Background(), "inbox.open", nil)
				}
			})
		}
	})
}

// dialog draws a Dialog card in the middle of the window; closing it dismisses it.
func (x *Inbox) dialog(c *ui.Ctx, r *inboxRow) {
	k := r.card.Key()
	if widget.Dialog(c, "dialog", widget.DialogOpts{Label: cmp.Or(r.card.Kicker, r.card.Title), Help: inboxHelp + "#dialogs"}, func(c *ui.Ctx) {
		x.card(c, r, true)
	}) {
		x.dismiss(k)
	}
}

// card draws a row's card: stacked, with its ✕; or in the panel.
func (x *Inbox) card(c *ui.Ctx, r *inboxRow, stacked bool) {
	k := r.card.Key()
	p := uireg.CardPlace{Stacked: stacked, TakeFocus: stacked && r.card.Top}
	if stacked {
		p.Dismiss = func() { x.dismiss(k) }
	}
	c.Box(k, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.draw(c, r, p) })
}

// draw draws a row's card where p says: its own Draw, or its face.
func (x *Inbox) draw(c *ui.Ctx, r *inboxRow, p uireg.CardPlace) {
	if r.card.Draw != nil {
		draw := r.card.Draw
		if err := registry.Call(func() error { draw(c, "card", p); return nil }); err != nil {
			c.Text("err", r.card.Title+": "+err.Error(), kit.MonoFont(10, c.Theme().C(theme.Error)))
		}
		return
	}
	x.face(c, r.card, p)
}

// face is a card with no Draw of its own: its fields in an action card, its actions running their commands.
func (x *Inbox) face(c *ui.Ctx, cd uireg.InboxCard, p uireg.CardPlace) {
	th := c.Theme()
	tone, ok := th.Color(cd.Tone)
	if !ok {
		tone = th.C(theme.Accent)
	}
	o := widget.ActionCardOpts{Kicker: cd.Kicker, Title: cd.Title, Detail: cd.Detail, Code: cd.Code, Note: cd.Note, Tone: tone,
		Help: inboxHelp + "#cards", Dismiss: p.Dismiss != nil, TakeFocus: p.TakeFocus}
	if p.Stacked && o.Kicker == "" && cd.Session != "" {
		if s, ok := x.h.State().Session(core.SessionID(cd.Session)); ok {
			o.Kicker = s.Name() + " · " + strings.ReplaceAll(cd.Type, "-", " ")
		}
	}
	if !cd.Expires.IsZero() {
		now := c.Now()
		o.Left = max(cd.Expires.Sub(now), 0)
		o.Countdown = max(cd.Expires.Sub(cd.Created), o.Left)
		if cd.Created.IsZero() {
			o.Countdown = max(2*time.Minute, o.Left)
		}
		if o.Left > 0 {
			c.WakeAt(now.Add(o.Left % time.Second).Add(time.Millisecond))
		}
	}
	for _, a := range cd.Actions {
		o.Actions = append(o.Actions, widget.CardAction{Label: a.Label, Key: a.Key, Primary: a.Primary})
	}
	res := widget.ActionCard(c, "card", o, cd.Body)
	if res.Dismissed && p.Dismiss != nil {
		p.Dismiss()
	}
	if res.Action < 0 {
		return
	}
	a := cd.Actions[res.Action]
	if a.Run != nil {
		registry.Call(func() error { a.Run(); return nil })
		return
	}
	if a.Command != "" {
		x.h.Run(context.Background(), a.Command, a.Args)
	}
}

// visible are the panel's rows under its filter: those waiting first, then history, each newest first.
func (x *Inbox) visible() []*inboxRow {
	rows := x.gather()
	x.mu.Lock()
	keep := inboxFilters[x.filter].keep
	x.mu.Unlock()
	var out []*inboxRow
	for _, r := range rows {
		if keep(r.card) {
			out = append(out, r)
		}
	}
	slices.SortStableFunc(out, func(a, b *inboxRow) int {
		if a.waiting() != b.waiting() {
			if a.waiting() {
				return -1
			}
			return 1
		}
		return b.when().Compare(a.when())
	})
	return out
}

// panelShown says whether the Inbox panel is open in the layout.
func (x *Inbox) panelShown() bool {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == InboxView {
			return true
		}
	}
	return false
}

// move selects the next or the previous row of the panel.
func (x *Inbox) move(d int) {
	rows := x.visible()
	if len(rows) == 0 {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	i := slices.IndexFunc(rows, func(r *inboxRow) bool { return r.card.Key() == x.sel })
	switch {
	case i < 0 && d < 0:
		i = len(rows) - 1
	case i < 0:
		i = 0
	default:
		i = min(max(i+d, 0), len(rows)-1)
	}
	x.sel = rows[i].card.Key()
	x.list.Reveal = x.sel
}

// panel is the Inbox panel: the filters, then a row for every card, the selected one's card open
// under it.
func (x *Inbox) panel(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	rows := x.visible()
	waiting := x.waitingCount()
	now := c.Now()
	root := c.Next("inbox")
	if r, ok := c.Rect(root); ok {
		x.mu.Lock()
		x.panelLeft = r.Min.X
		x.mu.Unlock()
	}
	c.Column("inbox", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgSurface), Role: ui.RoleRegion, Label: "Inbox", Help: inboxHelp}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 12, B: 10}, Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			if waiting > 0 {
				widget.Chip(c, "n", fmt.Sprintf("%d waiting", waiting), th.C(tInboxCount), inboxHelp)
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if k := kit.BoundKey(x.h, "inbox.open", nil); k != "" {
				c.Text("keys", k, kit.MonoFont(10, th.C(theme.FgGhost)))
			}
		})
		c.Row("filters", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, B: 10}, Gap: 6, ScrollX: true, Clip: true, Role: ui.RoleTabList,
			Label: "Show"}, func(c *ui.Ctx) {
			for i, f := range inboxFilters {
				x.mu.Lock()
				on := x.filter == i
				x.mu.Unlock()
				if widget.ToggleChip(c, i, f.label, on, widget.ToggleChipOpts{Help: inboxHelp + "#filters"}) {
					x.mu.Lock()
					x.filter = i
					x.mu.Unlock()
				}
			}
		})
		x.mu.Lock()
		sel := x.sel
		x.mu.Unlock()
		kit.Scrolled(c, "list", &x.list, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, DefaultFocus: true, KeyMode: InboxKeyMode, Role: ui.RoleList,
			Label: "Cards", Help: inboxHelp + "#list"}, ui.Style{Pad: ui.Insets{B: 8}}, func(c *ui.Ctx, block func(string) ui.ID) {
			if len(rows) == 0 {
				c.Box("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12)}, func(c *ui.Ctx) {
					widget.Wrap(c, "t", "Nothing is waiting on you. Questions, approvals and review requests show here as they come.",
						max(vc.Width-32, 120), kit.UIFont(12, th.C(theme.FgFaint)))
				})
			}
			for _, r := range rows {
				k := r.card.Key()
				block(k)
				c.Column(k, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
					if x.row(c, r, k == sel, now) {
						x.mu.Lock()
						if x.sel == k {
							x.sel = ""
						} else {
							x.sel = k
						}
						x.mu.Unlock()
					}
					if k == sel && r.waiting() {
						c.Box("open", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 10}}, func(c *ui.Ctx) { x.card(c, r, false) })
					}
				})
			}
		})
		inds := uireg.Indicators(x.h, uireg.SlotInboxFoot)
		if is, ok := kit.IndicatorsOf(x.h); ok && len(inds) > 0 {
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(40), Pad: ui.Insets{L: 16, R: 16}, Gap: 10, Cross: ui.Center, Clip: true,
				Help: inboxHelp + "#foot"}, func(c *ui.Ctx) {
				for _, ind := range inds {
					if n := is.Node(c, ind); n != nil {
						c.Row(ind.Name, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) { c.Node(n) })
					}
				}
			})
		}
	})
}

// row is a card's line in the panel: its icon, title, what it's about and when. It reports a click.
func (x *Inbox) row(c *ui.Ctx, r *inboxRow, on bool, now time.Time) bool {
	th := c.Theme()
	tone, ok := th.Color(r.card.Tone)
	if !ok {
		tone = th.C(theme.Accent)
	}
	title, sub := th.C(theme.Fg), th.C(theme.FgFaint)
	if !r.waiting() {
		tone, title, sub = th.C(tInboxDone), th.C(theme.FgMuted), th.C(tInboxDone)
	}
	fill := render.Color{}
	if on {
		fill = th.C(tInboxRowSel)
	}
	label := r.card.Title
	if !r.waiting() {
		label += " (answered)"
	}
	return widget.Clickable(c, "row", ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 10, Fill: fill, Selected: on, Label: label,
		Help: inboxHelp + "#list"}, func(c *ui.Ctx) {
		c.Row("icon", ui.Style{W: ui.Px(24), H: ui.Px(24), Radius: 5, Stroke: theme.WithAlpha(tone, .5), Border: 1, Main: ui.Center, Cross: ui.Center},
			func(c *ui.Ctx) {
				name := cmp.Or(r.card.Icon, "inbox")
				if !r.waiting() {
					name = "✓"
				}
				kit.Icon(c, x.h, "g", name, 13, tone)
			})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			c.Text("title", r.card.Title, kit.UIFont(12.5, title))
			if r.card.Sub != "" {
				c.Text("sub", r.card.Sub, kit.MonoFont(10, sub))
			}
		})
		widget.Ago(c, "when", r.when(), widget.AgoOpts{Text: ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: th.C(theme.FgGhost)}})
	}).Clicked
}

// indicator is the title bar's INBOX and its count; a click opens the panel.
func (x *Inbox) indicator(c *ui.Ctx) {
	th := c.Theme()
	n := x.waitingCount()
	label := "Inbox"
	if n > 0 {
		label = fmt.Sprintf("Inbox: %d waiting", n)
	}
	if widget.Clickable(c, "inbox", ui.Style{Dir: ui.Row, Pad: ui.PadXY(4, 3), Gap: 6, Radius: 4, Cross: ui.Center, Label: label, Help: inboxHelp + "#title-bar",
		Tooltip: "Everything waiting on you"}, func(c *ui.Ctx) {
		c.Text("k", "INBOX", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Medium}, Color: th.C(theme.FgMuted)})
		if n > 0 {
			kit.CountPill(c, "n", n, th.C(tInboxCount))
		}
	}).Clicked {
		x.h.Run(context.Background(), "inbox.open", nil)
	}
}
