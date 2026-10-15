package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The inbox (ui.md 12): every InboxSource's cards. Those waiting stack at the window's bottom right (a banner one,
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
	keep  func(registry.InboxCard) bool
}{
	{"All", func(registry.InboxCard) bool { return true }},
	{"Me", func(c registry.InboxCard) bool { return c.Mine }},
	{"Agents", func(c registry.InboxCard) bool { return c.Group == registry.GroupAgents }},
	{"Bus", func(c registry.InboxCard) bool { return c.Group == registry.GroupBus }},
	{"GitHub", func(c registry.InboxCard) bool { return c.Group == registry.GroupGitHub }},
	{"Sandbox", func(c registry.InboxCard) bool { return c.Group == registry.GroupSandbox }},
}

type inboxExt struct {
	disposer
	sh *shell
	h  ext.Host

	mu        sync.Mutex
	rows      map[string]*inboxRow // by card key
	order     []string             // card keys, as first seen
	dismissed map[string]bool      // taken off the stack; they wait on in the panel
	inline    map[string]bool      // shown by a surface of their own (mission control), so not stacked
	filter    int
	sel       string // the panel's selected row, its card open under it
	list      scrollState
	focusList bool
	panelLeft float32 // where the Inbox panel's left edge was last drawn, so the stack sits beside it
}

// inboxRow is a card as the inbox last had it, and whether its source has let it go (answered: history now).
type inboxRow struct {
	card registry.InboxCard
	seen time.Time
	gone bool
}

func (r *inboxRow) waiting() bool { return !r.gone && !r.card.Done }

func (r *inboxRow) when() time.Time {
	if !r.card.Created.IsZero() {
		return r.card.Created
	}
	return r.seen
}

func (x *inboxExt) Manifest() ext.Manifest { return ext.Manifest{ID: "inbox"} }

func (x *inboxExt) Activate(_ context.Context, h ext.Host) error {
	x.h, x.sh.inbox = h, x
	x.rows, x.dismissed = map[string]*inboxRow{}, map[string]bool{}
	open := registry.Args{"id": InboxView, "place": "dock", "edge": "right", "size": 380, "title": "Inbox"}
	for _, c := range []registry.Command{
		{ID: "inbox.open", Title: "Show the Inbox panel: everything waiting on you, and what's been answered; or hide it",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				x.mu.Lock()
				x.focusList = true
				x.mu.Unlock()
				return h.Run(ctx, "view.toggle", open)
			}},
		{ID: "inbox.dismiss", Title: "Take a card off the stack over the window: {id} (source/id), or the top one; it waits on in the Inbox panel",
			Run: func(_ registry.Ctx, a registry.Args) error {
				id := a.String("id")
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
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
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
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.View(InboxView, registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "inbox", Fn: func(c *ui.Ctx) { x.panel(c, vc) }}
	})))
	x.add(h.View(OverlayPrefix+"inbox", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "inbox", Fn: x.overlay}
	})))
	x.add(h.Contribute(registry.Indicator{Name: "inbox", Slot: registry.SlotTitleBar, Order: 80, Help: inboxHelp + "#title-bar",
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "inbox", Fn: x.indicator}, nil }}))
	return nil
}

// gather reads every source again: new cards join the rows, and those a source has let go become history.
func (x *inboxExt) gather() []*inboxRow {
	cards := registry.InboxCards(x.h)
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

// stacked are the cards over the window: waiting, to interrupt, not dismissed; banner ones first.
func (x *inboxExt) stacked() []*inboxRow {
	var out []*inboxRow
	for _, r := range x.gather() {
		if r.waiting() && (r.card.Interrupt || r.card.Banner) && !x.isDismissed(r.card.Key()) && (r.card.Banner || !x.isInline(r.card.Key())) {
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

func (x *inboxExt) isDismissed(k string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.dismissed[k]
}

func (x *inboxExt) isInline(k string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.inline[k]
}

// showInline keeps the cards keyed in keys off the stack while a surface draws them itself, as mission control does;
// nil gives them back. A banner card stays at the top whatever shows it.
func (x *inboxExt) showInline(keys map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.inline = keys
}

// waiting are the cards waiting on you, in the order they came.
func (x *inboxExt) waiting() []*inboxRow {
	var out []*inboxRow
	for _, r := range x.gather() {
		if r.waiting() {
			out = append(out, r)
		}
	}
	return out
}

// dismiss takes a card off the stack, and tells its source.
func (x *inboxExt) dismiss(k string) error {
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

// Count is how many cards wait in session, and the tone of the first: its tab's badge.
func (x *inboxExt) Count(session string) (n int, tone string) {
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

// waitingCount is how many cards wait, in every session and none.
func (x *inboxExt) waitingCount() int {
	n := 0
	for _, r := range x.gather() {
		if r.waiting() {
			n++
		}
	}
	return n
}

// overlay is the banner cards at the top and the stack at the bottom right.
func (x *inboxExt) overlay(c *ui.Ctx) {
	all := x.stacked()
	if len(all) == 0 {
		return
	}
	th := c.Theme()
	var banner, stack []*inboxRow
	for _, r := range all {
		if r.card.Banner {
			banner = append(banner, r)
		} else {
			stack = append(stack, r)
		}
	}
	if len(banner) > 0 {
		top := render.Point{X: c.Size().X / 2, Y: titleH + 10}
		c.Column("banner", ui.Style{W: ui.Px(440), Gap: 8, Z: ui.LayerToast, Float: &ui.Float{Point: &top, Self: render.Point{X: .5}},
			Role: ui.RoleGroup, Label: "Needs you now"}, func(c *ui.Ctx) {
			for _, r := range banner {
				x.card(c, r, true)
			}
		})
	}
	if len(stack) == 0 {
		return
	}
	corner := render.Point{X: c.Size().X - 16, Y: c.Size().Y - 36}
	if x.panelShown() {
		x.mu.Lock()
		if x.panelLeft > 0 {
			corner.X = min(corner.X, x.panelLeft-16)
		}
		x.mu.Unlock()
	}
	c.Column("asks", ui.Style{W: ui.Px(440), Gap: 8, Z: ui.LayerToast, Float: &ui.Float{Point: &corner, Self: render.Point{X: 1, Y: 1}},
		Role: ui.RoleGroup, Label: "Waiting on you"}, func(c *ui.Ctx) {
		for _, r := range stack[:min(len(stack), maxInterrupts)] {
			x.card(c, r, true)
		}
		if n := len(stack) - maxInterrupts; n > 0 {
			c.Row("more", ui.Style{W: ui.Flex(1), Main: ui.End, Cross: ui.Center, Gap: 8}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("+%d more waiting", n), monoFont(10, th.C(theme.FgFaint)))
				if widget.Button(c, "open", "Open the inbox", widget.ButtonOpts{Ghost: true, Key: boundKey(x.h, "inbox.open", nil), Help: inboxHelp}) {
					x.h.Run(context.Background(), "inbox.open", nil)
				}
			})
		}
	})
}

// card draws a row's card: stacked, with its ✕; or in the panel.
func (x *inboxExt) card(c *ui.Ctx, r *inboxRow, stacked bool) {
	k := r.card.Key()
	p := registry.CardPlace{Stacked: stacked}
	if stacked {
		p.Dismiss = func() { x.dismiss(k) }
	}
	c.Box(k, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		if r.card.Draw != nil {
			draw := r.card.Draw
			if err := registry.Call(func() error { draw(c, "card", p); return nil }); err != nil {
				c.Text("err", r.card.Title+": "+err.Error(), monoFont(10, c.Theme().C(theme.Error)))
			}
			return
		}
		x.face(c, r.card, p)
	})
}

// face is a card with no Draw of its own: its fields in an action card, its actions running their commands.
func (x *inboxExt) face(c *ui.Ctx, cd registry.InboxCard, p registry.CardPlace) {
	th := c.Theme()
	tone, ok := th.Color(cd.Tone)
	if !ok {
		tone = th.C(theme.Accent)
	}
	o := widget.ActionCardOpts{Kicker: cd.Kicker, Title: cd.Title, Detail: cd.Detail, Code: cd.Code, Note: cd.Note, Tone: tone,
		Help: inboxHelp + "#cards", Dismiss: p.Dismiss != nil}
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
func (x *inboxExt) visible() []*inboxRow {
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
func (x *inboxExt) panelShown() bool {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == InboxView {
			return true
		}
	}
	return false
}

// move selects the next or the previous row of the panel.
func (x *inboxExt) move(d int) {
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
	x.list.reveal = x.sel
}

// panel is the Inbox panel (12-inbox, right): the filters, then a row for every card, the selected one's card open
// under it.
func (x *inboxExt) panel(c *ui.Ctx, vc registry.ViewCtx) {
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
			if k := boundKey(x.h, "inbox.open", nil); k != "" {
				c.Text("keys", k, monoFont(10, th.C(theme.FgGhost)))
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
		listID := c.Next("list")
		x.mu.Lock()
		grab := vc.Focused && !c.FocusWithin(root) || x.focusList
		x.focusList = false
		sel := x.sel
		x.mu.Unlock()
		if grab {
			c.Focus(listID)
		}
		scrolled(c, "list", &x.list, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: InboxKeyMode, Role: ui.RoleList,
			Label: "Cards", Help: inboxHelp + "#list"}, ui.Style{Pad: ui.Insets{B: 8}}, func(c *ui.Ctx, block func(string) ui.ID) {
			if len(rows) == 0 {
				c.Box("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12)}, func(c *ui.Ctx) {
					widget.Wrap(c, "t", "Nothing is waiting on you. Questions, approvals and review requests show here as they come.",
						max(vc.Width-32, 120), uiFont(12, th.C(theme.FgFaint)))
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
	})
}

// row is a card's line in the panel: its icon, title, what it's about and when. It reports a click.
func (x *inboxExt) row(c *ui.Ctx, r *inboxRow, on bool, now time.Time) bool {
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
				icon(c, "g", name, 13, tone)
			})
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
			c.Text("title", r.card.Title, uiFont(12.5, title))
			if r.card.Sub != "" {
				c.Text("sub", r.card.Sub, monoFont(10, sub))
			}
		})
		c.Text("when", ago(now, r.when()), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5}, Color: th.C(theme.FgGhost)})
	}).Clicked
}

// indicator is the title bar's INBOX and its count; a click opens the panel.
func (x *inboxExt) indicator(c *ui.Ctx) {
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
			countPill(c, "n", n, th.C(tInboxCount))
		}
	}).Clicked {
		x.h.Run(context.Background(), "inbox.open", nil)
	}
}

// countPill is a small round count: a tab's cards waiting, the inbox's.
func countPill(c *ui.Ctx, key any, n int, fill render.Color) {
	th := c.Theme()
	c.Box(key, ui.Style{H: ui.Px(15), Pad: ui.PadXY(5, 0), Radius: 7.5, Fill: fill, Cross: ui.Center, Role: ui.RoleText,
		Label: fmt.Sprintf("%d waiting on you", n)}, func(c *ui.Ctx) {
		c.Text("n", fmt.Sprint(n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold}, Color: th.C(theme.AccentInk)})
	})
}
