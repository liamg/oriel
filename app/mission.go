package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/term"
	"github.com/liamg/oriel/ui"
)

// Mission control (ui.md 03): every session as a card, grouped by worker, status or repo, over the window under the
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
var missionGroups = []string{"worker", "status", "repo"}

type missionExt struct {
	disposer
	sh *shell
	h  ext.Host

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

func (x *missionExt) Manifest() ext.Manifest { return ext.Manifest{ID: "mission"} }

func (x *missionExt) Activate(_ context.Context, h ext.Host) error {
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
		{ID: "mission.open", Title: "Show mission control: grouped {by} worker, status or repo, {filter}ed, with the card of {id} selected",
			Run: func(_ registry.Ctx, a registry.Args) error {
				x.show()
				return x.set(a)
			}},
		{ID: "mission.close", Title: "Put mission control away", Run: func(registry.Ctx, registry.Args) error { x.close(); return nil }},
		{ID: "mission.group", Title: "Group mission control's cards {by} worker, status or repo", Run: func(_ registry.Ctx, a registry.Args) error {
			if a.String("by") == "" {
				return errors.New("group by worker, status or repo")
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
		{ID: "mission.new", Title: "Start a session on the selected card's worker, or {worker}", Run: func(ctx registry.Ctx, a registry.Args) error {
			w := a.String("worker")
			if w == "" {
				if se, ok := x.session(x.selected()); ok {
					w = string(workerOf(se))
				}
			}
			args := registry.Args{}
			if w != "" {
				args["worker"] = w
			}
			return h.Run(ctx, "session.new", args)
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
		Desc: "What mission control shows: every open session grouped by worker (or by status or repo: by), each with its status and what it waits on, and the queue of what needs the user, oldest first. show: true also opens it on screen, grouped by, filtered and with the card of id selected."}))
	x.add(h.Contribute(registry.StartEntry{Name: "mission", Icon: "mission", Title: "Mission control", Desc: "Every session as a card, and what each waits on",
		Command: "mission.toggle", Order: 95, Link: true, Note: x.needNote}))
	x.add(h.View(MissionView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "mission", Fn: x.page} })))
	return nil
}

func (x *missionExt) Deactivate(ctx context.Context) error {
	x.closeTerms(nil)
	if x.sh.inbox != nil {
		x.sh.inbox.showInline(nil)
	}
	return x.disposer.Deactivate(ctx)
}

func (x *missionExt) show() {
	x.open, x.err, x.reveal = true, "", true
}

func (x *missionExt) close() {
	x.open, x.renaming = false, ""
}

// set takes mission control's state from a: {by}, {filter}, {id}.
func (x *missionExt) set(a registry.Args) error {
	if by := a.String("by"); by != "" {
		i := slices.Index(missionGroups, by)
		if i < 0 {
			return fmt.Errorf("can't group by %q: by worker, status or repo", by)
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

func (x *missionExt) session(id core.SessionID) (core.SessionState, bool) {
	if s, ok := x.h.State().Session(id); ok && id != "" {
		return s.Snapshot(), true
	}
	return core.SessionState{}, false
}

// selected is the selected card's session: the one chosen, while it's still shown, else the first card.
func (x *missionExt) selected() core.SessionID {
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
func (x *missionExt) move(dir string) error {
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
func (x *missionExt) decide(ctx registry.Ctx, id core.SessionID, decision string) error {
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
func (x *missionExt) needNote() string {
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

// missionGroup is a heading's cards: a worker's, a status's or a repo's.
type missionGroup struct {
	key, title, meta string
	tone             core.Status
	worker           core.WorkerID // a worker's group, which ends with a card that starts a session there
	newCard          bool
	cards            []core.SessionState
}

// live are the sessions mission control shows: not archived, and matching the filter.
func (x *missionExt) live(st core.State) []core.SessionState {
	q := strings.ToLower(strings.TrimSpace(x.filter))
	var out []core.SessionState
	for _, se := range st.Sessions {
		if se.Archived {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(missionHay(st, se)), q) {
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

// missionHay is what the filter matches a session by: its name, kind, status, branch, projects, worker, ticket, PR.
func missionHay(st core.State, se core.SessionState) string {
	parts := []string{string(se.ID), se.Name, string(se.Kind), statusName(se.Status), se.Branch, se.Ticket, se.PR, st.WorkerName(se.Worker),
		se.Agent.Provider}
	for _, p := range se.Projects {
		parts = append(parts, p.Name)
	}
	return strings.Join(parts, " ")
}

func workerOf(se core.SessionState) core.WorkerID {
	return core.WorkerID(cmpOr(string(se.Worker), string(core.LocalWorker)))
}

// groups are the cards under their headings, as mission control is grouped now.
func (x *missionExt) groups(st core.State) []missionGroup {
	ss := x.live(st)
	var out []missionGroup
	switch missionGroups[x.group] {
	case "status":
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
	default:
		ws := slices.Clone(st.Workers)
		slices.SortStableFunc(ws, func(a, b core.WorkerState) int {
			switch {
			case a.ID == core.LocalWorker:
				return -1
			case b.ID == core.LocalWorker:
				return 1
			}
			return strings.Compare(a.Title(), b.Title())
		})
		for _, se := range ss {
			if !slices.ContainsFunc(ws, func(w core.WorkerState) bool { return w.ID == workerOf(se) }) {
				ws = append(ws, core.WorkerState{ID: workerOf(se)})
			}
		}
		for _, w := range ws {
			g := missionGroup{key: string(w.ID), title: strings.ToUpper(w.Title()), meta: workerMeta(w), tone: core.StatusQuiet, worker: w.ID,
				newCard: w.Connected && x.filter == ""}
			if !w.Connected {
				g.tone = core.StatusOff
			}
			for _, se := range ss {
				if workerOf(se) == w.ID {
					g.cards = append(g.cards, se)
				}
			}
			if len(g.cards) > 0 || g.newCard {
				out = append(out, g)
			}
		}
	}
	return out
}

// workerMeta is a worker's heading's detail: where it is, its cores, CPU and memory.
func workerMeta(w core.WorkerState) string {
	where := cmpOr(w.Host, "remote")
	if w.ID == core.LocalWorker {
		where = "local"
	}
	parts := []string{where}
	if !w.Connected {
		return joinParts(where, "offline")
	}
	if w.Cores > 0 {
		parts = append(parts, fmt.Sprintf("%d cores", w.Cores))
	}
	if !w.Sampled.IsZero() {
		parts = append(parts, fmt.Sprintf("cpu %d%%", pct(w.CPU)))
		if w.MemTotal > 0 {
			parts = append(parts, "mem "+strings.TrimSuffix(gibShort(w.Mem), "G")+"/"+gibShort(w.MemTotal))
		}
	}
	return strings.Join(parts, " · ")
}

// closeTerms closes the live terminals of cards not in keep.
func (x *missionExt) closeTerms(keep map[core.SessionID]bool) {
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
func (x *missionExt) tool(ctx registry.Ctx, p map[string]any) (any, error) {
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
func (x *missionExt) state(now time.Time) missionState {
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
