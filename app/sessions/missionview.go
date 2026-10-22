package sessions

import (
	"cmp"
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/term/ghostty"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tMissionHead     = theme.Declare("mission.head", "bg.surface")
	tMissionQueue    = theme.Declare("mission.queue", "bg.surface")
	tMissionCard     = theme.Declare("mission.card", "bg.surface")
	tMissionCardOn   = theme.Declare("mission.card.selected", "accent")
	tMissionBox      = theme.Declare("mission.box", "bg.base")
	tMissionLive     = theme.Declare("mission.live", "status.working")
	tMissionQueueOld = theme.Declare("mission.queue.age", "status.needs_you")
	tMissionQueueOn  = theme.DeclareOn("mission.queue.selected", "alpha(accent, 6%)", "bg.surface")
)

const (
	missionQueueW = 300
	missionCardH  = 236
	missionBodyH  = 150
	missionLineH  = 15
)

var missionTermFont = typeset.FontSpec{Family: typeset.Mono, Size: 11}

// page is mission control while it's open, under the title bar: the header, the needs-you queue beside the cards,
// the strip of indicators and the keys along its foot.
func (x *Mission) page(c *ui.Ctx) {
	x.land()
	if !x.open || c.Theme().IsProbe() {
		if !x.open {
			x.closeTerms(nil)
			if inbox, ok := kit.InboxOf(x.h); ok {
				inbox.ShowInline(nil)
			}
		}
		return
	}
	st := x.h.State()
	groups := x.groups(st)
	x.seen = map[core.SessionID]bool{}
	inline := map[string]bool{}
	var waiting []kit.WaitingCard
	inbox, hasInbox := kit.InboxOf(x.h)
	if hasInbox {
		waiting = inbox.Waiting()
		for _, r := range waiting {
			inline[r.Card.Key()] = true
		}
	}
	widget.Page(c, "mission", &x.open, widget.PageOpts{Top: kit.TitleH, KeyMode: MissionKeyMode, Label: "Mission control", Help: kit.MissionHelp + "#page"},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.header(c, st)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.queue(c, st, waiting)
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				x.grid(c, st, groups, waiting)
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.strip(c)
			c.Box("rule3", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.footer(c, st)
		})
	x.closeTerms(x.seen)
	if hasInbox {
		inbox.ShowInline(inline)
	}
}

// header is the title, the sessions by status, the grouping and the filter.
func (x *Mission) header(c *ui.Ctx, st core.State) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 18, Cross: ui.Center, Fill: th.C(tMissionHead)}, func(c *ui.Ctx) {
		c.Text("h", "MISSION CONTROL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 15, Weight: typeset.SemiBold, Tracking: .2},
			Color: th.C(theme.Fg)})
		counts := map[core.Status]int{}
		for _, s := range st.Sessions {
			if !s.Archived {
				counts[s.Status]++
			}
		}
		c.Row("counts", ui.Style{W: ui.Flex(1), Gap: 14, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
			for _, s := range kit.StatusOrder {
				n := counts[s]
				if n == 0 || s == core.StatusOff {
					continue
				}
				word := kit.StatusName(s)
				switch s {
				case core.StatusNeedsYou:
					word = "need you"
				case core.StatusReviewer:
					word = "in review"
				}
				label := fmt.Sprintf("%d %s", n, word)
				c.Row(s, ui.Style{Gap: 6, Cross: ui.Center, Role: ui.RoleText, Label: label, Keep: true}, func(c *ui.Ctx) {
					kit.HUDDot(c, "dot", th.C(kit.StatusTokens[s]))
					c.Text("t", label, kit.MonoFont(11, th.C(theme.FgMuted)))
				})
			}
		})
		if x.err != "" {
			c.Text("err", x.err, kit.MonoFont(10.5, th.C(theme.Error)))
		}
		g := x.group
		if widget.Segmented(c, "group", []string{"BY STATUS", "BY REPO"}, &g, widget.SegmentedOpts{Label: "Group by",
			Help: kit.MissionHelp + "#group"}) {
			x.group, x.reveal = g, true
		}
		id := c.Next("filter")
		if x.focusFilter {
			c.RequestFocus(id)
			x.focusFilter = false
		}
		r := widget.Input(c, "filter", &x.filter, widget.InputOpts{Label: "Filter sessions", Placeholder: "/ filter sessions…", Width: ui.Px(240),
			Help: kit.MissionHelp + "#filter"})
		if r.Submitted || r.Cancelled {
			c.Blur()
		}
	})
}

// queueItem is a row of the needs-you queue: a card waiting on you, or a session that needs you with no card.
type queueItem struct {
	key     string
	title   string
	sub     string
	at      time.Time
	session core.SessionID
	card    *uireg.InboxCard
	tone    render.Color
	bus     bool
}

// queue is what needs you, oldest first: every card waiting, and the sessions that need you with none; agentbus's
// apart, with their countdowns.
func (x *Mission) queue(c *ui.Ctx, st core.State, waiting []kit.WaitingCard) {
	th := c.Theme()
	now := c.Now()
	var mine, bus []queueItem
	carded := map[string]bool{}
	for _, r := range waiting {
		tone, ok := th.Color(r.Card.Tone)
		if !ok {
			tone = th.C(theme.Accent)
		}
		it := queueItem{key: r.Card.Key(), title: r.Card.Title, sub: r.Card.Sub, at: r.At, session: core.SessionID(r.Card.Session), card: &r.Card, tone: tone,
			bus: r.Card.Group == uireg.GroupBus}
		if it.bus {
			if !r.Card.Expires.IsZero() {
				left := max(r.Card.Expires.Sub(now), 0)
				it.sub = kit.JoinParts(it.sub, "auto-answer in "+kit.Duration(left))
				c.WakeAt(now.Add(time.Second))
			}
			bus = append(bus, it)
			continue
		}
		carded[r.Card.Session] = true
		mine = append(mine, it)
	}
	for _, se := range x.live(st) {
		if carded[string(se.ID)] || se.Status != core.StatusNeedsYou && se.Status != core.StatusPermission {
			continue
		}
		at := se.Created
		for _, it := range slices.Backward(se.Transcript) {
			if !it.Time.IsZero() {
				at = it.Time
				break
			}
		}
		title, what := kit.FirstLine(lastSaid(se)), "replied"
		if title == "" || se.Agent.Error != "" {
			title, what = cmp.Or(kit.Why(se, now), kit.StatusName(se.Status)), ""
		}
		mine = append(mine, queueItem{key: "session/" + string(se.ID), title: title,
			sub: kit.JoinParts(kit.Label(se), what), at: at, session: se.ID, tone: th.C(kit.StatusTokens[se.Status])})
	}
	oldest := func(a, b queueItem) int { return a.at.Compare(b.at) }
	slices.SortStableFunc(mine, oldest)
	slices.SortStableFunc(bus, oldest)
	c.Column("queue", ui.Style{W: ui.Px(missionQueueW), H: ui.Flex(1), Fill: th.C(tMissionQueue), ScrollY: true, Clip: true, Role: ui.RoleList,
		Label: "Needs you", Help: kit.MissionHelp + "#queue"}, func(c *ui.Ctx) {
		x.queueSection(c, "mine", "NEEDS YOU", "oldest first", th.C(theme.StatusNeedsYou), mine, now)
		if len(bus) > 0 {
			x.queueSection(c, "bus", "AGENTBUS", "", th.C(theme.FgFaint), bus, now)
		}
	})
}

func (x *Mission) queueSection(c *ui.Ctx, key, title, note string, tone render.Color, items []queueItem, now time.Time) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", title, kit.KickerFont(tone))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if note != "" {
				c.Text("n", note, kit.MonoFont(9.5, th.C(theme.FgGhost)))
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		if len(items) == 0 {
			c.Box("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 12)}, func(c *ui.Ctx) {
				c.Text("t", "Nothing needs you.", kit.UIFont(12, th.C(theme.FgFaint)))
			})
		}
		sel := x.selected()
		for _, it := range items {
			on := it.session != "" && it.session == sel
			fill, edge := render.Color{}, render.Color{}
			if on {
				fill, edge = th.C(tMissionQueueOn), it.tone
			}
			c.Column(it.key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				if widget.Clickable(c, "row", ui.Style{Dir: ui.Row, W: ui.Flex(1), Fill: fill, Selected: on, Label: it.title, Role: ui.RoleListItem,
					Help: kit.MissionHelp + "#queue"}, func(c *ui.Ctx) {
					c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
					c.Row("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 12, T: 10, B: 10}, Gap: 10}, func(c *ui.Ctx) {
						c.Row("age", ui.Style{W: ui.Px(30)}, func(c *ui.Ctx) { c.Text("t", kit.Ago(now, it.at), kit.MonoFont(10, th.C(tMissionQueueOld))) })
						c.Column("text", ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
							widget.Wrap(c, "title", it.title, missionQueueW-80, kit.UIFont(12.5, th.C(theme.Fg)))
							if it.sub != "" {
								c.Text("sub", it.sub, kit.MonoFont(10, th.C(theme.FgFaint)))
							}
						})
					})
				}).Clicked {
					switch {
					case it.session != "":
						x.sel, x.reveal = it.session, true
					case x.queueOpen == it.key:
						x.queueOpen = ""
					default:
						x.queueOpen = it.key
					}
				}
				if inbox, ok := kit.InboxOf(x.h); ok && it.session == "" && x.queueOpen == it.key && it.card != nil {
					c.Box("open", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 10, B: 10}}, func(c *ui.Ctx) { inbox.Card(c, *it.card) })
				}
				c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			})
		}
	})
}

// grid is the cards under their groups' headings, as many to a row as fit.
func (x *Mission) grid(c *ui.Ctx, st core.State, groups []missionGroup, waiting []kit.WaitingCard) {
	th := c.Theme()
	id := c.Next("grid")
	cols := 3
	if r, ok := c.Rect(id); ok {
		cols = min(max(int((r.Dx()-40+14)/(missionMinCardW+14)), 1), 4)
	}
	sel := x.selected()
	var rows [][]core.SessionID
	var selID ui.ID
	c.Column("grid", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true, Pad: ui.Insets{L: 20, R: 20, T: 14, B: 20}, Gap: 14,
		Role: ui.RoleList, Label: "Sessions", Help: kit.MissionHelp + "#cards"}, func(c *ui.Ctx) {
		if len(groups) == 0 {
			c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 60), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
				msg := "No sessions yet. Start one, and it shows here as a card."
				if x.filter != "" {
					msg = "No session matches “" + x.filter + "”."
				}
				c.Text("t", msg, kit.UIFont(13, th.C(theme.FgFaint)))
				if x.filter == "" && widget.Button(c, "new", "New session", widget.ButtonOpts{Primary: true, Key: kit.BoundKey(x.h, "session.new", nil),
					Help: kit.MissionHelp + "#new"}) {
					x.run("mission.new", nil)
				}
			})
		}
		for _, g := range groups {
			c.Column(g.key, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
				c.Row("h", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Role: ui.RoleText, Label: kit.JoinParts(g.title, g.meta)}, func(c *ui.Ctx) {
					kit.HUDDot(c, "dot", th.C(kit.StatusTokens[g.tone]))
					c.Text("t", g.title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 12, Weight: typeset.SemiBold, Tracking: .18},
						Color: th.C(theme.Fg)})
					if g.meta != "" {
						c.Text("m", g.meta, kit.MonoFont(10, th.C(theme.FgFaint)))
					}
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				})
				n := len(g.cards)
				for r := 0; r*cols < n; r++ {
					var row []core.SessionID
					c.Row(r, ui.Style{W: ui.Flex(1), Gap: 14}, func(c *ui.Ctx) {
						for i := r * cols; i < min((r+1)*cols, n); i++ {
							se := g.cards[i]
							row = append(row, se.ID)
							cid := x.card(c, st, se, se.ID == sel, waiting)
							if se.ID == sel {
								selID = cid
							}
						}
						for i := n; i < (r+1)*cols; i++ {
							c.Box(fmt.Sprint("pad", i), ui.Style{W: ui.Flex(1)}, nil)
						}
					})
					if len(row) > 0 {
						rows = append(rows, row)
					}
				}
			})
		}
	})
	x.rows = rows
	if x.reveal && selID != 0 {
		c.ScrollIntoView(id, selID)
		x.reveal = false
	}
}

// card is a session as mission control shows it: its border in its status's colour, its live output while it works,
// else what it waits on; a click selects it.
func (x *Mission) card(c *ui.Ctx, st core.State, se core.SessionState, on bool, waiting []kit.WaitingCard) ui.ID {
	th := c.Theme()
	tone := th.C(kit.StatusTokens[se.Status])
	line, border := theme.WithAlpha(tone, .5), float32(1)
	if on {
		line, border = th.C(tMissionCardOn), 1.5
	}
	name := kit.Label(se)
	id := c.Next(se.ID)
	in := widget.Clickable(c, se.ID, ui.Style{Dir: ui.Column, W: ui.Flex(1), Fill: th.C(tMissionCard), Stroke: line,
		Border: border, Radius: 8, Pad: ui.Pad(12), Gap: 10, NoHover: true, Cursor: render.CursorDefault, Role: ui.RoleListItem, Label: name,
		Selected: on, Help: kit.MissionHelp + "#cards", Subject: kit.SessionSubject + string(se.ID)}, func(c *ui.Ctx) {
		x.cardHead(c, st, se, tone, widget.PointerIn(c))
		c.Column("body", ui.Style{W: ui.Flex(1), H: ui.Size{Min: missionBodyH}}, func(c *ui.Ctx) { x.cardBody(c, se, tone, waiting) })
		x.cardFoot(c, se)
	})
	if in.Clicked {
		x.sel = se.ID
	}
	if in.DoubleClicked {
		x.run("mission.open-card", registry.Args{"id": string(se.ID)})
	}
	return id
}

// cardHead is a card's name, kind and chips; hovered, an Open button (Resume while its agent is stopped).
func (x *Mission) cardHead(c *ui.Ctx, st core.State, se core.SessionState, tone render.Color, hovered bool) {
	th := c.Theme()
	name := kit.Label(se)
	c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		kit.HUDDot(c, "dot", tone)
		if x.renaming == se.ID {
			if x.focusName {
				x.focusName = false
				c.RequestFocus(c.Next("rename"))
			}
			r := widget.Input(c, "rename", &x.name, widget.InputOpts{Label: "Rename " + name, SelectOnFocus: true, Help: kit.MissionHelp + "#keys"})
			switch {
			case r.Submitted:
				x.renaming = ""
				x.run("session.rename", registry.Args{"id": string(se.ID), "name": strings.TrimSpace(x.name)})
			case r.Cancelled:
				x.renaming = ""
			}
			return
		}
		if widget.Clickable(c, "name", ui.Style{Label: "Open " + name, Help: kit.MissionHelp + "#open", Tooltip: "Open the session"}, func(c *ui.Ctx) {
			c.Text("t", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		}).Clicked {
			x.run("mission.open-card", registry.Args{"id": string(se.ID)})
		}
		kind := registry.Label(x.h, se)
		if n := len(se.Projects); n > 1 {
			kind += fmt.Sprintf(" · %d repos", n)
		}
		c.Text("kind", kind, kit.MonoFont(10, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		for i, ch := range registry.Chips(x.h, se) {
			widget.Chip(c, fmt.Sprint("chip", i), ch.Text, th.C(theme.FgMuted), cmp.Or(ch.Help, kit.MissionHelp+"#cards"))
		}
		if p := se.Agent.Provider; p != "" {
			widget.Chip(c, "prov", kit.ProviderName(x.h, p), th.C(theme.FgMuted), kit.MissionHelp+"#cards")
		}
		if hovered {
			if widget.Button(c, "end", "End…", widget.ButtonOpts{Ghost: true, Help: kit.MissionHelp + "#keys"}) {
				x.run("mission.end", registry.Args{"id": string(se.ID)})
			}
			label := "Open"
			if !se.Agent.Live() {
				label = "Resume"
			}
			if widget.Button(c, "open", label, widget.ButtonOpts{Help: kit.MissionHelp + "#open"}) {
				x.run("mission.open-card", registry.Args{"id": string(se.ID)})
			}
		}
	})
}

// firstChip is the text of the session's first chip from its kind: a review's PR.
func (x *Mission) firstChip(se core.SessionState) string {
	if chips := registry.Chips(x.h, se); len(chips) > 0 {
		return chips[0].Text
	}
	return ""
}

// cardBody is what a card shows: the agent's live output while it works; a card waiting on you, answered here as
// in the inbox; else what its status is about.
func (x *Mission) cardBody(c *ui.Ctx, se core.SessionState, tone render.Color, waiting []kit.WaitingCard) {
	th := c.Theme()
	var mine []kit.WaitingCard
	for _, r := range waiting {
		if r.Card.Session == string(se.ID) && !r.Card.Banner {
			mine = append(mine, r)
		}
	}
	if len(mine) > 0 {
		if inbox, ok := kit.InboxOf(x.h); ok {
			inbox.Card(c, mine[0].Card)
		}
		if n := len(mine) - 1; n > 0 {
			c.Text("more", fmt.Sprintf("+%d more waiting", n), kit.MonoFont(10, th.C(theme.FgFaint)))
		}
		return
	}
	if s := se.Agent.Starting; s != nil {
		c.Column("box", ui.Style{W: ui.Flex(1), H: ui.Px(missionBodyH), Pad: ui.Pad(12), Gap: 8, Fill: th.C(tMissionBox),
			Stroke: theme.WithAlpha(th.C(theme.StatusWorking), .35), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
			c.Row("k", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				widget.Spinner(c, "spin", widget.SpinnerOpts{Size: 11, Tone: th.C(theme.StatusWorking), Name: "starting", Help: kit.StartingHelp})
				c.Text("k", "STARTING · "+kit.Duration(c.Now().Sub(s.Since)), kit.KickerFont(th.C(theme.StatusWorking)))
			})
			widget.Wrap(c, "t", s.Step+"…", missionMinCardW-40, kit.UIFont(12.5, th.C(theme.Fg)))
			if s.Note != "" {
				widget.Wrap(c, "note", firstLines(s.Note, 3), missionMinCardW-40, kit.UIFont(12, th.C(theme.FgMuted)))
			}
		})
		c.WakeAt(c.Now().Truncate(time.Second).Add(time.Second))
		return
	}
	if se.Status == core.StatusWorking || se.Agent.Working {
		x.liveOutput(c, se)
		return
	}
	kicker, text := "", ""
	last := lastSaid(se)
	switch {
	case se.Agent.Error != "":
		kicker, text, tone = "FAILED", se.Agent.Error, th.C(theme.Error)
	case se.Status == core.StatusNeedsYou:
		kicker, text = "REPLIED — AWAITING YOU", last
	case se.Status == core.StatusPermission:
		kicker, text = "WANTS PERMISSION", kit.Why(se, c.Now())
	case se.Status == core.StatusCI:
		kicker, text = "CI RUNNING", kit.JoinParts(x.firstChip(se), last)
	case se.Status == core.StatusReviewer:
		kicker, text = "WITH A REVIEWER", kit.JoinParts(x.firstChip(se), last)
	case se.Status == core.StatusOff:
		kicker, text = "STOPPED", "Open it to resume the agent."
	default:
		kicker, text = "QUIET", cmp.Or(last, "Nothing running.")
	}
	c.Column("box", ui.Style{W: ui.Flex(1), H: ui.Px(missionBodyH), Pad: ui.Pad(12), Gap: 8, Fill: th.C(tMissionBox), Stroke: theme.WithAlpha(tone, .35), Border: 1,
		Radius: 6, Clip: true}, func(c *ui.Ctx) {
		c.Text("k", kicker, kit.KickerFont(tone))
		widget.Wrap(c, "t", firstLines(text, 5), missionMinCardW-40, kit.UIFont(12.5, th.C(theme.FgMuted)))
	})
}

// lastSaid is the agent's last prose.
func lastSaid(se core.SessionState) string {
	for _, it := range slices.Backward(se.Transcript) {
		if it.Kind == core.ItemAssistant && strings.TrimSpace(it.Text) != "" {
			return strings.TrimSpace(it.Text)
		}
	}
	return ""
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	return strings.Join(lines[:min(len(lines), n)], "\n")
}

// cardFoot is the session's tasks, what's running in the background, the processes left running, context, change and
// how long it's been.
func (x *Mission) cardFoot(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	now := c.Now()
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		if n := len(se.Tasks); n > 0 {
			done := 0
			for _, t := range se.Tasks {
				if t.Status == core.TaskCompleted {
					done++
				}
			}
			c.Text("tasks", fmt.Sprintf("tasks %d/%d", done, n), kit.MonoFont(10, th.C(theme.FgFaint)))
			widget.Meter(c, "tm", float64(done)/float64(n), widget.MeterOpts{Tone: th.C(theme.OK), Width: ui.Px(40), Height: 3, Label: "Tasks done"})
		}
		if bg := se.Agent.Background; len(bg) > 0 {
			var what []string
			for _, t := range bg {
				what = append(what, cmp.Or(t.Kind, "task")+": "+t.Description)
			}
			c.Box("bg", ui.Style{Tooltip: strings.Join(what, "\n"), Help: kit.MissionHelp + "#background"}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("+%d in background", len(bg)), kit.MonoFont(10, th.C(theme.StatusWorking)))
			})
		}
		if ps := se.Agent.Processes; len(ps) > 0 {
			var what []string
			for _, t := range ps {
				what = append(what, t.Description)
			}
			c.Box("running", ui.Style{Tooltip: strings.Join(what, "\n"), Help: kit.MissionHelp + "#running"}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("+%d running", len(ps)), kit.MonoFont(10, th.C(theme.FgFaint)))
			})
		}
		if s := se.Agent.Stats; s.ContextMax > 0 {
			c.Text("ctx", fmt.Sprintf("ctx %d%%", kit.Pct(float64(s.Context)/float64(s.ContextMax))), kit.MonoFont(10, th.C(theme.FgFaint)))
		}
		add, del := x.transcriptDiff(se)
		if add+del > 0 {
			widget.DiffStat(c, "diff", add, del, widget.DiffStatOpts{})
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		when := ""
		switch {
		case se.Agent.Working && !se.Agent.TurnStarted.IsZero():
			when = kit.Duration(now.Sub(se.Agent.TurnStarted))
			c.WakeAt(now.Add(time.Second))
		default:
			at := se.Created
			for _, it := range slices.Backward(se.Transcript) {
				if !it.Time.IsZero() {
					at = it.Time
					break
				}
			}
			if !at.IsZero() {
				when = kit.Ago(now, at)
			}
		}
		c.Text("when", when, kit.MonoFont(10, th.C(theme.FgGhost)))
	})
}

// diffTotal is the lines a session's transcript added and removed, and the transcript they were summed over.
type diffTotal struct {
	items    *core.Item
	n        int
	add, del int
}

// transcriptDiff is the lines se's transcript added and removed, summed again only when it changes: a changed
// transcript is a new slice.
func (x *Mission) transcriptDiff(se core.SessionState) (add, del int) {
	var first *core.Item
	if len(se.Transcript) > 0 {
		first = &se.Transcript[0]
	}
	if d, ok := x.diffs[se.ID]; ok && d.items == first && d.n == len(se.Transcript) {
		return d.add, d.del
	}
	d := diffTotal{items: first, n: len(se.Transcript)}
	for _, it := range se.Transcript {
		if it.Diff != nil {
			d.add, d.del = d.add+it.Diff.Added, d.del+it.Diff.Removed
		}
	}
	if x.diffs == nil {
		x.diffs = map[core.SessionID]diffTotal{}
	}
	x.diffs[se.ID] = d
	return d.add, d.del
}

// modeKey is the key bound to command in mode, as the keyboard shows it.
func modeKey(h registry.Keys, mode, command string) string {
	for _, k := range h.KeymapsFor(command) {
		if k.Mode == mode {
			return kit.KeyLabel(k.Keys)
		}
	}
	return ""
}

// liveOutput is a working card's terminal: the agent's tool calls and what they print, prose as it comes, in a VT the
// size of the box, so a tool's colours come through.
func (x *Mission) liveOutput(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	x.seen[se.ID] = true
	charW := c.Shaper().Shape("M", missionTermFont).Advance
	id := c.Next("live")
	t := x.terms[se.ID]
	if r, ok := c.Rect(id); ok {
		cols, rows := max(int((r.Dx()-20)/charW), 10), max(int((r.Dy()-12)/missionLineH), 2)
		if t == nil {
			emu, err := ghostty.New(cols, rows, ghostty.Options{Scrollback: 64 << 10})
			if err != nil {
				x.err = "live output: " + err.Error()
			} else {
				t = &liveTerm{emu: emu, cols: cols, rows: rows}
				x.terms[se.ID] = t
			}
		} else if cols != t.cols || rows != t.rows {
			t.cols, t.rows, t.sig = cols, rows, ""
			t.emu.Resize(cols, rows)
		}
	}
	if t != nil {
		if sig := activitySig(se); sig != t.sig {
			t.sig = sig
			t.emu.Write([]byte("\x1bc" + strings.Join(activityLines(se), "\r\n")))
		}
	}
	c.Box("live", ui.Style{W: ui.Flex(1), H: ui.Px(missionBodyH), Fill: th.C(tMissionBox), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true,
		Role: ui.RoleText, Label: "Live output", Help: kit.MissionHelp + "#live"}, func(c *ui.Ctx) {
		if t != nil {
			grid := t.emu.Grid()
			cs := t.emu.CursorState()
			c.Paint("grid", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
				x0 := r.Min.X + 10
				for row, line := range grid.Lines {
					kit.DrawTermLine(f, c.Shaper(), th, line.Cells, x0, r.Min.Y+6+float32(row)*missionLineH, charW, missionTermFont, missionLineH)
				}
				if cs.InViewport && cs.Visible {
					cx, cy := x0+float32(cs.ViewX)*charW, r.Min.Y+6+float32(cs.ViewY)*missionLineH
					f.Rect(render.Rect{Min: render.Point{X: cx, Y: cy + 1}, Max: render.Point{X: cx + charW*.6, Y: cy + missionLineH - 1}},
						render.Style{Fill: theme.WithAlpha(th.C(kit.TTermCursor), .8)})
				}
			})
		}
		c.Row("tag", ui.Style{Gap: 5, Cross: ui.Center, Float: &ui.Float{At: render.Point{X: 1}, Self: render.Point{X: 1}, Offset: render.Point{X: -8, Y: 7}}},
			func(c *ui.Ctx) {
				kit.HUDDot(c, "dot", th.C(tMissionLive))
				c.Text("t", "LIVE", kit.KickerFont(th.C(tMissionLive)))
			})
	})
}

// activityShown is how many of the transcript's last items a live card writes.
const activityShown = 14

// activityItems are the transcript's last items worth showing live: tool calls, prose and errors.
func activityItems(se core.SessionState) []core.Item {
	var out []core.Item
	for _, it := range slices.Backward(se.Transcript) {
		switch it.Kind {
		case core.ItemTool, core.ItemAssistant, core.ItemError:
			out = append(out, it)
		}
		if len(out) == activityShown {
			break
		}
	}
	slices.Reverse(out)
	return out
}

// activitySig changes whenever what a live card writes would.
func activitySig(se core.SessionState) string {
	var b strings.Builder
	for _, it := range activityItems(se) {
		fmt.Fprintf(&b, "%s/%v/%d/%d;", it.ID, it.Done, len(it.Output), len(it.Text))
	}
	return b.String()
}

// activityLines are the agent's recent activity as terminal lines: each tool call with a dot (running, ok or
// failed) and the end of what it printed, prose in cyan, failures in red.
func activityLines(se core.SessionState) []string {
	const (
		reset = "\x1b[0m"
		faint = "\x1b[90m"
		red   = "\x1b[31m"
		green = "\x1b[32m"
		cyan  = "\x1b[36m"
	)
	var out []string
	for _, it := range activityItems(se) {
		switch it.Kind {
		case core.ItemTool:
			dot := faint
			switch {
			case !it.Done:
				dot = cyan
			case it.IsError:
				dot = red
			}
			line := dot + "●" + reset + " " + faint + it.Tool + reset + " " + termSafe(kit.ToolLine(it))
			if it.Diff != nil {
				line += fmt.Sprintf(" %s(+%d -%d)%s", faint, it.Diff.Added, it.Diff.Removed, reset)
			}
			out = append(out, line)
			if it.Done && it.Diff == nil {
				for _, l := range tailLines(it.Output, 3) {
					if it.IsError {
						l = red + l + reset
					}
					out = append(out, "  "+l+reset)
				}
			}
		case core.ItemAssistant:
			text := strings.TrimSpace(it.Text)
			if text == "" {
				continue
			}
			l := lastLine(text)
			if it.Partial {
				l += "…"
			}
			out = append(out, cyan+"» "+termSafe(l)+reset)
		case core.ItemError:
			out = append(out, red+"✕ "+termSafe(kit.FirstLine(it.Text))+reset)
		}
	}
	if len(out) == 0 {
		out = append(out, green+"●"+reset+" "+faint+"working…"+reset)
	}
	return out
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return lines[len(lines)-1]
}

// tailLines are the last n lines of a tool's output that say something, safe to write to a terminal.
func tailLines(s string, n int) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n") {
		if l = strings.TrimRight(termSafe(l), " \t"); strings.TrimSpace(stripSGR(l)) != "" {
			out = append(out, l)
		}
	}
	return out[max(len(out)-n, 0):]
}

var (
	escapes = regexp.MustCompile(`\x1b(\[[0-9;:?<=>]*[ -/]*[@-~]|\][^\x07\x1b]*(\x07|\x1b\\)?|[@-Z\\-_])`)
	sgr     = regexp.MustCompile(`^\x1b\[[0-9;:]*m$`)
)

// termSafe is a line of output with only its colours kept: no cursor movement, clearing or control characters that
// would draw outside its line.
func termSafe(s string) string {
	s = escapes.ReplaceAllStringFunc(s, func(e string) string {
		if sgr.MatchString(e) {
			return e
		}
		return ""
	})
	return strings.Map(func(r rune) rune {
		if r == '\x1b' || r == '\t' || r >= ' ' && r != 0x7f {
			if r == '\t' {
				return ' '
			}
			return r
		}
		return -1
	}, s)
}

func stripSGR(s string) string { return escapes.ReplaceAllString(s, "") }

// strip is the indicators in the mission slot along the foot: usage per provider, the machine's CPU.
func (x *Mission) strip(c *ui.Ctx) {
	th := c.Theme()
	inds := uireg.Indicators(x.h, uireg.SlotMission)
	is, ok := kit.IndicatorsOf(x.h)
	if len(inds) == 0 || !ok {
		return
	}
	c.Row("strip", ui.Style{W: ui.Flex(1), H: ui.Px(64), Fill: th.C(tMissionHead), ScrollX: true, Clip: true, Role: ui.RoleRegion,
		Label: "Indicators", Help: kit.MissionHelp + "#strip"}, func(c *ui.Ctx) {
		for _, ind := range inds {
			if n := is.Node(c, ind); n != nil {
				c.Row(ind.Name, ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) { c.Node(n) })
			}
		}
	})
}

// footer is the keys that act on the cards, and where Escape goes back to.
func (x *Mission) footer(c *ui.Ctx, st core.State) {
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(20, 0), Gap: 16, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
		keys := func(cmd string, args registry.Args) string {
			for _, k := range x.h.Keymaps() {
				if k.Mode == MissionKeyMode && k.Command == cmd && kit.SameArgs(k.Args, args) {
					return kit.KeyLabel(k.Keys)
				}
			}
			return ""
		}
		var move []string
		for _, d := range []string{"left", "down", "up", "right"} {
			if k := keys("mission.move", registry.Args{"dir": d}); k != "" {
				move = append(move, k)
			}
		}
		if len(move) > 0 {
			kit.KeyHint(c, "move", strings.Join(move, " "), "move")
		}
		for _, h := range []struct{ cmd, what string }{{"mission.open-card", "open"}, {"mission.allow", "allow"}, {"mission.stop", "stop"},
			{"mission.end", "end"}, {"mission.rename", "rename"}, {"mission.new", "new session"}, {"mission.find", "filter"}} {
			if k := keys(h.cmd, nil); k != "" {
				kit.KeyHint(c, h.cmd, k, h.what)
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		back := "the start view"
		if s, ok := st.Session(st.Focus); ok {
			back = s.Name()
		} else if dir := x.windowDir(); dir != "" {
			back = kit.TildePath(dir)
		}
		kit.KeyHint(c, "esc", "esc", "back to "+back)
	})
}

func (x *Mission) run(id string, a registry.Args) {
	x.err = ""
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
	}
}
