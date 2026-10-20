package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/ui"
)

// Mission control (ui.md 03): every session as a card, grouped by status or repo, over the window under the
// title bar. A working card shows the agent's live output in a terminal; any other shows what it waits on, its asks
// answered from the card as the inbox answers them. Its opening and its state are commands, so a plugin or the
// assistant drives it as the keys do.
const (
	MissionView     = OverlayPrefix + "mission"
	MissionKeyMode  = "mission"
	AtLaunchOption  = "mission.at_launch"
	missionHelp     = "shell/mission"
	missionMinCardW = 340
)

// missionGroups are how the cards are grouped: the segmented control's order, and mission.group's {by}.
var missionGroups = []string{"status", "repo"}

type missionPlugin struct {
	disposer
	sh *shell
	h  plugin.Host

	open    bool
	group   int
	filter  string
	sel     core.SessionID
	rows    [][]core.SessionID // the cards as last laid out, a row at a time
	reveal  bool               // bring the selected card into view next frame
	pending bool               // opens once a session is known, while the window is still on the start view

	focusFilter bool
	renaming    core.SessionID
	name        string
	queueOpen   string // a card without a session, open under its queue row
	err         string

	terms map[core.SessionID]*liveTerm
	seen  map[core.SessionID]bool
}

// liveTerm is a working card's terminal: the agent's activity written into a VT the size of its box.
type liveTerm struct {
	emu        term.Emulator
	cols, rows int
	sig        string
}

func (x *missionPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "mission"} }

func (x *missionPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.terms = map[core.SessionID]*liveTerm{}
	if !x.sh.launchDone {
		x.sh.launchDone = true
		x.pending = x.sh.cfg.WorkDir == "" && len(x.sh.cfg.Files) == 0
	}
	x.add(h.Option(registry.OptionSpec{Name: AtLaunchOption, Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Open on mission control when Oriel starts with nothing open, once there's a session to show."}))
	selected := func(a registry.Args) core.SessionID {
		if id := a.String("id"); id != "" {
			return core.SessionID(id)
		}
		return x.selected()
	}
	cards := func(registry.Args) bool { return x.open && x.selected() != "" }
	for _, c := range []registry.Command{
		{ID: "mission.toggle", Title: "Show mission control, or put it away", Run: func(registry.Ctx, registry.Args) error {
			if x.open {
				x.close()
				return nil
			}
			x.show()
			return nil
		}},
		{ID: "mission.open", Title: "Show mission control: grouped {by} status or repo, {filter}ed, with the card of {id} selected",
			Run: func(_ registry.Ctx, a registry.Args) error {
				x.show()
				return x.set(a)
			}},
		{ID: "mission.close", Title: "Put mission control away", Run: func(registry.Ctx, registry.Args) error { x.close(); return nil }},
		{ID: "mission.group", Title: "Group mission control's cards {by} status or repo", Run: func(_ registry.Ctx, a registry.Args) error {
			if a.String("by") == "" {
				return errors.New("group by status or repo")
			}
			return x.set(a)
		}},
		{ID: "mission.filter", Title: "Show only the sessions matching {text} on mission control; empty shows them all",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.set(registry.Args{"filter": a.String("text")}) }},
		{ID: "mission.find", Title: "Mission control: type a filter", Run: func(registry.Ctx, registry.Args) error {
			x.focusFilter = true
			return nil
		}},
		{ID: "mission.select", Title: "Select the card of {id} on mission control", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.set(registry.Args{"id": a.String("id")})
		}},
		{ID: "mission.move", Title: "Select the card to the {dir} on mission control: left, right, up or down", Available: cards,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.move(a.String("dir")) }},
		{ID: "mission.open-card", Title: "Open the session of the selected card, or of {id}, and put mission control away",
			Available: func(a registry.Args) bool { return selected(a) != "" },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				id := selected(a)
				if err := h.Run(ctx, "session.open", registry.Args{"id": string(id)}); err != nil {
					return err
				}
				x.close()
				return nil
			}},
		{ID: "mission.allow", Title: "Allow what the selected card's session, or {id}, asks first", Available: func(a registry.Args) bool { return selected(a) != "" },
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.decide(ctx, selected(a), "allow") }},
		{ID: "mission.deny", Title: "Deny what the selected card's session, or {id}, asks first", Available: func(a registry.Args) bool { return selected(a) != "" },
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.decide(ctx, selected(a), "deny") }},
		{ID: "mission.stop", Title: "Stop the agent of the selected card's session, or {id}", Available: func(a registry.Args) bool { return selected(a) != "" },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return h.Run(ctx, "session.stop", registry.Args{"id": string(selected(a))})
			}},
		{ID: "mission.rename", Title: "Rename the selected card's session, or {id}: {name}, or type it on the card",
			Available: func(a registry.Args) bool { return selected(a) != "" },
			Run: func(ctx registry.Ctx, a registry.Args) error {
				id := selected(a)
				if name, ok := a["name"].(string); ok {
					return h.Run(ctx, "session.rename", registry.Args{"id": string(id), "name": name})
				}
				x.renaming, x.name = id, ""
				if se, ok := h.State().Session(id); ok {
					x.name = se.Name()
				}
				return nil
			}},
		{ID: "mission.new", Title: "Start a new session: the launcher", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "session.new", nil)
		}},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "global", Keys: "<D-K>", Command: "mission.toggle", Desc: "Mission control"},
		{Mode: MissionKeyMode, Keys: "h", Command: "mission.move", Args: registry.Args{"dir": "left"}, Desc: "Card to the left"},
		{Mode: MissionKeyMode, Keys: "<Left>", Command: "mission.move", Args: registry.Args{"dir": "left"}, Desc: "Card to the left"},
		{Mode: MissionKeyMode, Keys: "l", Command: "mission.move", Args: registry.Args{"dir": "right"}, Desc: "Card to the right"},
		{Mode: MissionKeyMode, Keys: "<Right>", Command: "mission.move", Args: registry.Args{"dir": "right"}, Desc: "Card to the right"},
		{Mode: MissionKeyMode, Keys: "k", Command: "mission.move", Args: registry.Args{"dir": "up"}, Desc: "Card above"},
		{Mode: MissionKeyMode, Keys: "<Up>", Command: "mission.move", Args: registry.Args{"dir": "up"}, Desc: "Card above"},
		{Mode: MissionKeyMode, Keys: "j", Command: "mission.move", Args: registry.Args{"dir": "down"}, Desc: "Card below"},
		{Mode: MissionKeyMode, Keys: "<Down>", Command: "mission.move", Args: registry.Args{"dir": "down"}, Desc: "Card below"},
		{Mode: MissionKeyMode, Keys: "<CR>", Command: "mission.open-card", Desc: "Open"},
		{Mode: MissionKeyMode, Keys: "a", Command: "mission.allow", Desc: "Allow"},
		{Mode: MissionKeyMode, Keys: "x", Command: "mission.stop", Desc: "Stop"},
		{Mode: MissionKeyMode, Keys: "r", Command: "mission.rename", Desc: "Rename"},
		{Mode: MissionKeyMode, Keys: "n", Command: "mission.new", Desc: "New session"},
		{Mode: MissionKeyMode, Keys: "/", Command: "mission.find", Desc: "Filter"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(registry.AgentTool{Name: "mission_control", For: []string{assistantAgent}, Reads: "mission control", Source: "builtin:mission",
		Params: map[string]string{"show": "boolean?", "by": "string?", "filter": "string?", "id": "string?"}, Run: x.tool,
		Desc: "What mission control shows: every open session grouped by status (or by repo: by), each with its status and what it waits on, and the queue of what needs the user, oldest first. show: true also opens it on screen, grouped by, filtered and with the card of id selected."}))
	mission := pagePlace("mission", "Mission control", "mission.open", &x.open)
	mission.Close = x.close
	x.add(h.Contribute(mission))
	x.add(h.Contribute(registry.StartEntry{Name: "mission", Icon: "mission", Title: "Mission control", Desc: "Every session as a card, and what each waits on",
		Command: "mission.toggle", Order: 95, Link: true, Note: x.needNote, Shown: x.watching}))
	x.add(h.View(MissionView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "mission", Fn: x.page} })))
	return nil
}

func (x *missionPlugin) Deactivate(ctx context.Context) error {
	x.closeTerms(nil)
	if x.sh.inbox != nil {
		x.sh.inbox.showInline(nil)
	}
	return x.disposer.Deactivate(ctx)
}

func (x *missionPlugin) show() {
	x.open, x.err, x.reveal = true, "", true
}

func (x *missionPlugin) close() {
	x.open, x.renaming = false, ""
}

// set takes mission control's state from a: {by}, {filter}, {id}.
func (x *missionPlugin) set(a registry.Args) error {
	if by := a.String("by"); by != "" {
		i := slices.Index(missionGroups, by)
		if i < 0 {
			return fmt.Errorf("can't group by %q: by status or repo", by)
		}
		x.group = i
	}
	if f, ok := a["filter"].(string); ok {
		x.filter = f
	}
	if id := a.String("id"); id != "" {
		if _, ok := x.session(core.SessionID(id)); !ok {
			return fmt.Errorf("no session %q", id)
		}
		x.sel, x.reveal = core.SessionID(id), true
	}
	return nil
}

func (x *missionPlugin) session(id core.SessionID) (core.SessionState, bool) {
	if s, ok := x.h.State().Session(id); ok && id != "" {
		return s.Snapshot(), true
	}
	return core.SessionState{}, false
}

// selected is the selected card's session: the one chosen, while it's still shown, else the first card.
func (x *missionPlugin) selected() core.SessionID {
	var first core.SessionID
	for _, g := range x.groups(x.h.State()) {
		for _, se := range g.cards {
			if se.ID == x.sel {
				return se.ID
			}
			if first == "" {
				first = se.ID
			}
		}
	}
	return first
}

// move selects the card beside the selected one, as the cards were last laid out.
func (x *missionPlugin) move(dir string) error {
	cur := x.selected()
	r, col := -1, 0
	for i, row := range x.rows {
		if j := slices.Index(row, cur); j >= 0 {
			r, col = i, j
		}
	}
	if r < 0 {
		return nil
	}
	switch dir {
	case "left", "right":
		var flat []core.SessionID
		for _, row := range x.rows {
			flat = append(flat, row...)
		}
		i := slices.Index(flat, cur)
		if dir == "left" {
			i = max(i-1, 0)
		} else {
			i = min(i+1, len(flat)-1)
		}
		x.sel = flat[i]
	case "up", "down":
		if dir == "up" {
			r = max(r-1, 0)
		} else {
			r = min(r+1, len(x.rows)-1)
		}
		row := x.rows[r]
		x.sel = row[min(col, len(row)-1)]
	default:
		return fmt.Errorf("move left, right, up or down, not %q", dir)
	}
	x.reveal = true
	return nil
}

// decide answers the first of a session's asks that takes an allow or a deny, through ask.answer as its card does.
func (x *missionPlugin) decide(ctx registry.Ctx, id core.SessionID, decision string) error {
	se, ok := x.session(id)
	if !ok {
		return fmt.Errorf("no session %q", id)
	}
	for _, a := range se.Asks {
		switch a.Kind {
		case core.AskPermission, core.AskNetwork, core.AskGateway:
			return x.h.Run(ctx, "ask.answer", registry.Args{"id": string(id), "ask": a.ID, "decision": decision})
		}
	}
	return fmt.Errorf("%s isn't asking for anything to %s", cmpOr(se.Name, string(id)), decision)
}

// needNote is the start view's link figure: how many sessions need you.
func (x *missionPlugin) needNote() string {
	n := 0
	for _, s := range x.h.State().Sessions {
		if !s.Archived && (s.Status == core.StatusNeedsYou || s.Status == core.StatusPermission) {
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%d need you", n)
}

// missionGroup is a heading's cards: a status's or a repo's.
type missionGroup struct {
	key, title, meta string
	tone             core.Status
	cards            []core.SessionState
}

// watching says there's something for mission control to watch: a session whose agent is running, starting, or
// asking. The start view links to it only then.
func (x *missionPlugin) watching() bool {
	return slices.ContainsFunc(x.h.State().Sessions, func(se core.SessionState) bool {
		return !se.Archived && (se.Agent.Live() || len(se.Asks) > 0)
	})
}

// live are the sessions mission control shows: not archived, and matching the filter.
func (x *missionPlugin) live(st core.State) []core.SessionState {
	q := strings.ToLower(strings.TrimSpace(x.filter))
	var out []core.SessionState
	for _, se := range st.Sessions {
		if se.Archived {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(missionHay(se)), q) {
			continue
		}
		out = append(out, se)
	}
	slices.SortStableFunc(out, func(a, b core.SessionState) int {
		if d := slices.Index(statusOrder, a.Status) - slices.Index(statusOrder, b.Status); d != 0 {
			return d
		}
		return a.Created.Compare(b.Created)
	})
	return out
}

// missionHay is what the filter matches a session by: its name, kind, status, branch, projects, ticket, PR.
func missionHay(se core.SessionState) string {
	parts := []string{string(se.ID), se.Name, string(se.Kind), statusName(se.Status), se.Branch, se.Ticket, se.PR, se.Agent.Provider}
	for _, p := range se.Projects {
		parts = append(parts, p.Name)
	}
	return strings.Join(parts, " ")
}

// groups are the cards under their headings, as mission control is grouped now.
func (x *missionPlugin) groups(st core.State) []missionGroup {
	ss := x.live(st)
	var out []missionGroup
	switch missionGroups[x.group] {
	default:
		for _, s := range statusOrder {
			g := missionGroup{key: string(s), title: strings.ToUpper(statusName(s)), tone: s}
			for _, se := range ss {
				if se.Status == s {
					g.cards = append(g.cards, se)
				}
			}
			if len(g.cards) > 0 {
				out = append(out, g)
			}
		}
	case "repo":
		at := map[string]int{}
		for _, se := range ss {
			name := "no repo"
			if p, ok := se.Primary(); ok && p.Name != "" {
				name = p.Name
			}
			i, ok := at[name]
			if !ok {
				i = len(out)
				at[name] = i
				out = append(out, missionGroup{key: name, title: strings.ToUpper(name), tone: core.StatusQuiet})
			}
			out[i].cards = append(out[i].cards, se)
		}
	}
	return out
}

// closeTerms closes the live terminals of cards not in keep.
func (x *missionPlugin) closeTerms(keep map[core.SessionID]bool) {
	for id, t := range x.terms {
		if !keep[id] {
			t.emu.Close()
			delete(x.terms, id)
		}
	}
}

// missionCardLine is a card as the assistant reads mission control.
type missionCardLine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Status  string `json:"status"`
	Why     string `json:"why,omitempty"`
	Waiting string `json:"waiting,omitempty"` // the first card waiting on the user for it
}

type missionGroupLine struct {
	Title    string            `json:"title"`
	Meta     string            `json:"meta,omitempty"`
	Sessions []missionCardLine `json:"sessions"`
}

type missionState struct {
	Open     bool               `json:"open"`
	By       string             `json:"by"`
	Filter   string             `json:"filter,omitempty"`
	Selected string             `json:"selected,omitempty"`
	Groups   []missionGroupLine `json:"groups"`
	NeedsYou []string           `json:"needsYou,omitempty"` // what waits on the user, oldest first
}

// tool is mission_control, the assistant's: mission control's state, opened first with {show}.
func (x *missionPlugin) tool(ctx registry.Ctx, p map[string]any) (any, error) {
	var out missionState
	var err error
	lerr := onLoop(ctx, x.h, func() {
		a := registry.Args{}
		for _, k := range []string{"by", "filter", "id"} {
			if v, ok := p[k].(string); ok && v != "" {
				a[k] = v
			}
		}
		if show, _ := p["show"].(bool); show {
			x.show()
		}
		if err = x.set(a); err != nil {
			return
		}
		out = x.state(time.Now())
	})
	if lerr != nil {
		return nil, lerr
	}
	return out, err
}

// state is mission control as it reads now, open or not.
func (x *missionPlugin) state(now time.Time) missionState {
	st := x.h.State()
	out := missionState{Open: x.open, By: missionGroups[x.group], Filter: x.filter, Selected: string(x.selected())}
	waiting := map[string]string{}
	if x.sh.inbox != nil {
		rows := x.sh.inbox.waiting()
		slices.SortStableFunc(rows, func(a, b *inboxRow) int { return a.when().Compare(b.when()) })
		for _, r := range rows {
			if r.card.Session != "" && waiting[r.card.Session] == "" {
				waiting[r.card.Session] = r.card.Title
			}
			out.NeedsYou = append(out.NeedsYou, joinParts(r.card.Title, r.card.Sub))
		}
	}
	for _, g := range x.groups(st) {
		gl := missionGroupLine{Title: g.title, Meta: g.meta, Sessions: []missionCardLine{}}
		for _, se := range g.cards {
			gl.Sessions = append(gl.Sessions, missionCardLine{ID: string(se.ID), Name: cmpOr(se.Name, string(se.ID)), Kind: string(se.Kind),
				Status: string(se.Status), Why: why(se, now), Waiting: waiting[string(se.ID)]})
		}
		out.Groups = append(out.Groups, gl)
	}
	return out
}
