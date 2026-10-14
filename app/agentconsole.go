package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// AgentConsoleView is the agent console (ui.md 15): the session in front's structured transcript, its subagents and
// asks, a composer, and the session's tally.
const AgentConsoleView = "agent.console"

// The console's key modes: its transcript, and its composer.
const (
	AgentKeyMode    = "agent"
	ComposerKeyMode = "composer"
)

var (
	tConsoleBg     = theme.Declare("agent.console.bg", "bg.base")
	tConsoleSide   = theme.Declare("agent.console.side", "bg.surface")
	tBlockBg       = theme.Declare("agent.block.bg", "bg.surface")
	tBlockLine     = theme.Declare("agent.block.border", "line")
	tBlockSelected = theme.Declare("agent.block.selected", "accent")
	tToolOK        = theme.Declare("agent.tool.ok", "ok")
	tToolRunning   = theme.Declare("agent.tool.running", "status.working")
	tToolFailed    = theme.Declare("agent.tool.failed", "error")
	tThinking      = theme.Declare("agent.thinking.fg", "fg.ghost")
	tThreadKicker  = theme.Declare("agent.thread.kicker", "amber")
	tQueued        = theme.Declare("agent.queued", "amber")
	tAskPermission = theme.Declare("ask.permission", "status.permission")
	tAskNetwork    = theme.Declare("ask.network", "cyan")
	tAskGateway    = theme.Declare("ask.gateway", "amber")
	tAskQuestion   = theme.Declare("ask.question", "status.needs_you")
	tRuleAllow     = theme.Declare("agent.rule.allow", "ok")
	tRuleAsk       = theme.Declare("agent.rule.ask", "pink")
	tMeterContext  = theme.Declare("agent.meter.context", "amber")
)

// agentConsole is the console view, the asks' cards over every view, and the commands both run.
type agentConsole struct {
	disposer
	sh *shell
	h  ext.Host

	by map[core.SessionID]*consoleState
}

// consoleState is one session's console: what's selected, folded, typed.
type consoleState struct {
	sel      string          // the selected block's key; "" follows the end
	flip     map[string]bool // blocks folded or unfolded against their default
	blocks   []string        // the blocks' keys as last built, for j and k
	scroll   scrollState
	side     scrollState
	draft    string
	compose  bool // focus the composer next frame
	focus    bool // focus the transcript next frame
	forking  bool
	forkQ    string
	forks    []forkAnswer
	noting   string // the ask a note is being written for
	note     string
	focusNow string // an input to focus next frame: "note", "fork"
	chosen   map[string]map[int][]string
	typed    map[string]string
}

type forkAnswer struct {
	q, a string
	err  error
	done bool
}

func (x *agentConsole) Manifest() ext.Manifest { return ext.Manifest{ID: "agentconsole"} }

func (x *agentConsole) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.by = map[core.SessionID]*consoleState{}
	open := registry.Args{"id": AgentConsoleView, "place": "dock", "edge": "bottom", "size": 460, "title": "Agent"}
	session := func(a registry.Args) core.SessionID {
		if id := a.String("id"); id != "" {
			return core.SessionID(id)
		}
		return h.State().Focus
	}
	inFront := func(registry.Args) bool { return h.State().Focus != "" }
	cmds := []registry.Command{
		{ID: "agent.console", Title: "Show the agent console of the session in front, or hide it", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.toggle", open)
		}},
		{ID: "agent.console.next", Title: "Agent console: the next block", Run: func(registry.Ctx, registry.Args) error { x.move(1); return nil }},
		{ID: "agent.console.prev", Title: "Agent console: the block before", Run: func(registry.Ctx, registry.Args) error { x.move(-1); return nil }},
		{ID: "agent.console.fold", Title: "Agent console: fold or unfold the selected block", Run: func(registry.Ctx, registry.Args) error {
			if st := x.state(h.State().Focus); st != nil && st.sel != "" {
				st.flip[st.sel] = !st.flip[st.sel]
			}
			return nil
		}},
		{ID: "agent.console.compose", Title: "Agent console: write to the agent", Run: func(registry.Ctx, registry.Args) error {
			if st := x.state(h.State().Focus); st != nil {
				st.compose = true
			}
			return nil
		}},
		{ID: "agent.console.escape", Title: "Agent console: interrupt the turn, or close the console when the agent is idle",
			Run: func(ctx registry.Ctx, _ registry.Args) error {
				if se, ok := x.front(); ok && se.Agent.Working {
					return h.Run(ctx, "session.interrupt", nil)
				}
				return h.Run(ctx, "view.close", registry.Args{"docked": true})
			}},
		{ID: "agent.send", Title: "Send the agent of {id}, or the session in front, a message: {text}; {urgent} interrupts the turn, else it waits for it",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error {
				id := session(a)
				c, err := x.client(id)
				if err != nil {
					return err
				}
				text := strings.TrimSpace(a.String("text"))
				if text == "" {
					return errors.New("nothing to send")
				}
				return c.Send(ctx, worker.SendReq{Session: id, Text: text, Urgent: a.Bool("urgent")})
			}},
		{ID: "agent.console.send", Title: "Agent console: send what's in the composer; {urgent} interrupts the turn",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				id := h.State().Focus
				st := x.state(id)
				if st == nil || strings.TrimSpace(st.draft) == "" {
					return nil
				}
				if err := h.Run(ctx, "agent.send", registry.Args{"text": st.draft, "urgent": a.Bool("urgent")}); err != nil {
					return err
				}
				st.draft = ""
				return nil
			}},
		{ID: "agent.fork", Title: "Ask the agent of the session in front a side question: {prompt}, answered from its context outside the conversation",
			Available: inFront, Run: func(_ registry.Ctx, a registry.Args) error {
				prompt := strings.TrimSpace(a.String("prompt"))
				if prompt == "" {
					if st := x.state(h.State().Focus); st != nil {
						st.forking, st.focusNow = true, "fork"
					}
					return nil
				}
				return x.fork(session(a), prompt)
			}},
		{ID: "agent.terminal", Title: "Show the raw terminal of {id}'s agent, or the session in front's", Available: inFront,
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.rawTerminal(ctx, session(a)) }},
		{ID: "ask.answer", Title: "Answer an ask: {ask} of {id} (the first of the session in front by default) with {decision} allow, always, deny or answer, {note}, {scope}, {answers}",
			Available: inFront, Run: func(ctx registry.Ctx, a registry.Args) error {
				id := session(a)
				ask, ok := x.ask(id, a.String("ask"))
				if !ok {
					return errors.New("nothing is waiting on you there")
				}
				return x.answer(ctx, id, ask, a)
			}},
		{ID: "ask.deny-note", Title: "Deny an ask with a note for the agent: {ask} of {id}, the first of the session in front by default",
			Available: inFront, Run: func(registry.Ctx, registry.Args) error {
				id := h.State().Focus
				ask, ok := x.ask(id, "")
				if !ok {
					return errors.New("nothing is waiting on you there")
				}
				x.startNote(id, ask.ID)
				return nil
			}},
	}
	for _, c := range cmds {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "j", Command: "agent.console.next", Desc: "Next block"},
		{Keys: "<Down>", Command: "agent.console.next", Desc: "Next block"},
		{Keys: "k", Command: "agent.console.prev", Desc: "Block before"},
		{Keys: "<Up>", Command: "agent.console.prev", Desc: "Block before"},
		{Keys: "<CR>", Command: "agent.console.fold", Desc: "Fold or unfold"},
		{Keys: "i", Command: "agent.console.compose", Desc: "Write to the agent"},
		{Keys: "<Esc>", Command: "agent.console.escape", Desc: "Interrupt, or close"},
		{Keys: "y", Command: "ask.answer", Args: registry.Args{"decision": "allow"}, Desc: "Allow once"},
		{Keys: "a", Command: "ask.answer", Args: registry.Args{"decision": "always"}, Desc: "Always in this workspace"},
		{Keys: "n", Command: "ask.answer", Args: registry.Args{"decision": "deny"}, Desc: "Deny"},
		{Keys: "N", Command: "ask.deny-note", Desc: "Deny with a note"},
	} {
		k.Mode = AgentKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: ComposerKeyMode, Keys: "<D-CR>", Command: "agent.console.send", Args: registry.Args{"urgent": true},
		Desc: "Send now, interrupting the turn"}))
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-A>", Command: "agent.console", Desc: "Agent console"}))
	if len(x.sh.cfg.Workers) > 0 {
		// The rail offers the console where there are agents: with a worker connected.
		x.add(h.Contribute(registry.RailEntry{Name: "agent", Icon: "agent", Title: "Agent console", Command: "agent.console", Order: 70,
			Active: func() bool { return x.shown() }, Badge: func() int {
				se, _ := x.front()
				return len(se.Asks)
			}}))
	}
	x.add(h.View(AgentConsoleView, consoleView{x}))
	x.add(h.View(TitleRight+"40-usage", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "usage", Fn: x.usageTat} })))
	x.add(h.View(OverlayPrefix+"asks", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "asks", Fn: x.interrupts}
	})))
	return nil
}

type consoleView struct{ x *agentConsole }

func (v consoleView) Title(string) string {
	if se, ok := v.x.front(); ok && se.Agent.Provider != "" {
		return "Agent · " + providerName(se.Agent.Provider)
	}
	return "Agent"
}
func (v consoleView) EdgeToEdge() {}
func (v consoleView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "console", Fn: func(c *ui.Ctx) { v.x.build(c, vc) }}
}

func (x *agentConsole) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

func (x *agentConsole) state(id core.SessionID) *consoleState {
	if id == "" {
		return nil
	}
	st := x.by[id]
	if st == nil {
		st = &consoleState{flip: map[string]bool{}, chosen: map[string]map[int][]string{}, typed: map[string]string{}, scroll: scrollState{follow: true}}
		x.by[id] = st
	}
	return st
}

func (x *agentConsole) shown() bool {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == AgentConsoleView {
			return true
		}
	}
	return false
}

func (x *agentConsole) client(id core.SessionID) (worker.Client, error) {
	if x.sh.sessions == nil {
		return worker.Client{}, errors.New("no worker is connected")
	}
	return x.sh.sessions.client(id)
}

// move selects the next or previous block.
func (x *agentConsole) move(d int) {
	st := x.state(x.h.State().Focus)
	if st == nil || len(st.blocks) == 0 {
		return
	}
	i := slices.Index(st.blocks, st.sel)
	switch {
	case i < 0 && d < 0:
		i = len(st.blocks) - 1
	case i < 0:
		i = 0
	default:
		i = min(max(i+d, 0), len(st.blocks)-1)
	}
	st.sel = st.blocks[i]
	st.scroll.reveal = st.sel
}

// ask is the ask id of session id, or the one selected in its console, or its first.
func (x *agentConsole) ask(id core.SessionID, ask string) (core.Ask, bool) {
	se, ok := x.h.State().Session(id)
	if !ok {
		return core.Ask{}, false
	}
	asks := se.Snapshot().Asks
	if ask == "" {
		if st := x.by[id]; st != nil {
			ask, _ = strings.CutPrefix(st.sel, "ask:")
		}
	}
	for _, a := range asks {
		if a.ID == ask {
			return a, true
		}
	}
	if len(asks) > 0 {
		return asks[0], true
	}
	return core.Ask{}, false
}

// answer sends the user's answer to an ask to its session's worker.
func (x *agentConsole) answer(ctx context.Context, id core.SessionID, ask core.Ask, a registry.Args) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	r := worker.AnswerReq{Session: id, Ask: ask.ID, Decision: a.String("decision"), Note: a.String("note"), Scope: a.String("scope")}
	switch m := a["answers"].(type) {
	case map[string]string:
		r.Answers = m
	case map[string]any:
		r.Answers = map[string]string{}
		for k, v := range m {
			r.Answers[k] = fmt.Sprint(v)
		}
	}
	if r.Decision == "" {
		r.Decision = "allow"
	}
	if len(r.Answers) > 0 {
		r.Decision = "answer"
	}
	if st := x.by[id]; st != nil && st.noting == ask.ID {
		st.noting, st.note = "", ""
	}
	return c.Answer(ctx, r)
}

func (x *agentConsole) startNote(id core.SessionID, ask string) {
	if st := x.state(id); st != nil {
		st.noting, st.note, st.focusNow = ask, "", "note"
	}
}

// fork asks a side question, and shows the answer in the console when it comes.
func (x *agentConsole) fork(id core.SessionID, prompt string) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	st := x.state(id)
	st.forks = append(st.forks, forkAnswer{q: prompt})
	n := len(st.forks) - 1
	go func() {
		out, err := c.Fork(context.Background(), id, prompt)
		x.h.Do(func() {
			if n < len(st.forks) {
				st.forks[n].a, st.forks[n].err, st.forks[n].done = out, err, true
			}
		})
		x.h.Redraw()
	}()
	return nil
}

// rawTerminal shows the agent's own terminal, streamed from its worker, in a terminal view.
func (x *agentConsole) rawTerminal(ctx registry.Ctx, id core.SessionID) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	src, err := newAgentTerm(c, id)
	if err != nil {
		return err
	}
	return x.h.Run(ctx, "term.open", registry.Args{"source": src, "title": "agent · " + string(id), "place": "tab", "instance": "agent-" + string(id)})
}

// build draws the console for the session in front.
func (x *agentConsole) build(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	root := c.Next("console")
	c.Column("console", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tConsoleBg), Role: ui.RoleRegion, Label: "Agent console", Help: "agent/console"},
		func(c *ui.Ctx) {
			if !ok {
				c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("t", "No session in front: start one with ⌘N, and its agent shows here.", uiFont(12.5, th.C(theme.FgFaint)))
				})
				return
			}
			st := x.state(se.ID)
			x.header(c, se)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				width := float32(820)
				if r, ok := c.Rect(c.Next("left")); ok {
					width = min(max(r.Dx()-48, 240), 900)
				}
				c.Column("left", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					x.threadCard(c, se, width)
					x.transcript(c, se, st, width, vc.Focused && !c.FocusWithin(root))
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					x.composer(c, se, st)
				})
				c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				x.side(c, se)
			})
		})
}

func providerName(p string) string {
	switch p {
	case "claude":
		return "Claude"
	case "codex":
		return "Codex"
	case "":
		return "Agent"
	}
	return strings.ToUpper(p[:1]) + p[1:]
}

func uiFont(size float32, col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: size}, Color: col}
}

func monoFont(size float32, col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: col}
}

func kickerFont(col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14}, Color: col}
}

// consoleStatus is the header's chip: what the agent is doing, in the status's colour.
func consoleStatus(se core.SessionState) (string, theme.TokenID) {
	switch {
	case slices.ContainsFunc(se.Asks, func(a core.Ask) bool { return a.Kind == core.AskQuestion }):
		return "needs you", theme.StatusNeedsYou
	case len(se.Asks) > 0:
		return "wants permission", theme.StatusPermission
	case se.Agent.Working:
		return "working", theme.StatusWorking
	case se.Agent.Error != "":
		return "failed", theme.Error
	case se.Agent.Provider != "" && !se.Agent.Running:
		return "stopped", theme.StatusOff
	}
	return "idle", theme.StatusQuiet
}

func (x *agentConsole) header(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	a := se.Agent
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(16, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		name := providerName(a.Provider)
		c.Box("badge", ui.Style{W: ui.Px(22), H: ui.Px(22), Radius: 5, Fill: th.C(theme.BgOverlay), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(name[:min(2, len(name))]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold},
				Color: th.C(theme.Fg)})
		})
		c.Text("name", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		if a.Model != "" {
			c.Text("model", a.Model, monoFont(11, th.C(theme.FgFaint)))
		}
		if a.Effort != "" {
			c.Text("effort", a.Effort, monoFont(11, th.C(theme.FgFaint)))
		}
		word, tok := consoleStatus(se)
		widget.Chip(c, "status", word, th.C(tok), "agent/console#header")
		var where []string
		if se.Ticket != "" {
			where = append(where, se.Ticket)
		}
		where = append(where, cmpOr(se.Name, string(se.ID)))
		branch := se.Branch
		if p, ok := se.Primary(); ok && p.Branch != "" {
			branch = p.Branch
		}
		if branch != "" {
			where = append(where, branch)
		}
		c.Text("where", strings.Join(where, " · "), monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		ctx := context.Background()
		if widget.Button(c, "fork", "Fork", widget.ButtonOpts{Help: "agent/console#fork"}) {
			x.run(ctx, "agent.fork", nil)
		}
		if widget.Button(c, "interrupt", "Interrupt", widget.ButtonOpts{Key: "esc", Disabled: !a.Working, Help: "agent/console#interrupt"}) {
			x.run(ctx, "session.interrupt", nil)
		}
		if widget.Button(c, "raw", "Raw terminal", widget.ButtonOpts{Help: "agent/console#raw-terminal"}) {
			x.run(ctx, "agent.terminal", nil)
		}
	})
}

// run runs a command from a click, saying what went wrong in a notice.
func (x *agentConsole) run(ctx context.Context, id string, a registry.Args) {
	if err := x.h.Run(ctx, id, a); err != nil {
		x.h.Notify(core.Notice{ID: "agent.console", Severity: core.SevError, Source: "agent", Text: err.Error(), Time: time.Now()})
	}
}

// threadCard is the request the turn is working on: its anchor, and what you asked.
func (x *agentConsole) threadCard(c *ui.Ctx, se core.SessionState, width float32) {
	i := slices.IndexFunc(se.Threads, func(t core.Thread) bool { return t.Busy })
	if i < 0 {
		return
	}
	t := se.Threads[i]
	th := c.Theme()
	where := t.Title
	if t.Anchor != nil {
		where = fmt.Sprintf("%s:%d", filepath.Base(t.Anchor.File), t.Anchor.Line)
	}
	body := ""
	for _, cm := range t.Comments {
		if cm.Author == core.AuthorYou {
			body = cm.Body
		}
	}
	c.Row("threadwrap", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 12}, Main: ui.Center}, func(c *ui.Ctx) {
		c.Row("thread", ui.Style{W: ui.Px(width), Pad: ui.PadXY(14, 10), Gap: 10, Cross: ui.Center, Fill: th.C(tBlockBg), Stroke: th.C(tBlockLine), Border: 1,
			Radius: 6, Role: ui.RoleGroup, Label: "Thread " + where}, func(c *ui.Ctx) {
			c.Text("k", strings.ToUpper("thread · "+where), kickerFont(th.C(tThreadKicker)))
			c.Text("b", firstLine(strings.TrimSpace(plainMarkdown(body))), uiFont(12.5, th.C(theme.Fg)))
		})
	})
}

// scrollState is how a scrolled column follows its end and keeps a block in view.
type scrollState struct {
	follow bool   // stay at the end as it grows
	reveal string // a block to bring into view next frame
}

// scrolled is a column that scrolls (with the toolkit's scrollbars), following its end or bringing a block keyed in
// it into view on request; everything the console scrolls goes through it.
func scrolled(c *ui.Ctx, key any, st *scrollState, s, content ui.Style, body func(c *ui.Ctx, block func(key string) ui.ID)) {
	id := c.Next(key)
	inner := ui.Child(id, "content")
	view, okV := c.Rect(id)
	all, okC := c.Rect(inner)
	if okV && okC {
		at := c.Scroll(id).Y
		if st.follow && at+view.Dy() < all.Dy()-2 {
			c.ScrollTo(id, render.Point{Y: all.Dy()})
		}
	}
	blocks := map[string]ui.ID{}
	s.ScrollY, s.Clip = true, true
	c.Column(key, s, func(c *ui.Ctx) {
		content.W = ui.Flex(1)
		c.Column("content", content, func(c *ui.Ctx) {
			body(c, func(k string) ui.ID {
				blocks[k] = ui.Child(inner, k)
				return blocks[k]
			})
		})
	})
	if k := st.reveal; k != "" && okV && okC {
		c.ScrollIntoView(id, blocks[k])
		st.follow, st.reveal = false, ""
	}
}

// transcript is the session's transcript as blocks, with its asks where they arose, its subagents and side answers.
// focus is whether the console's pane has focus with nothing in it focused: the transcript takes it.
func (x *agentConsole) transcript(c *ui.Ctx, se core.SessionState, st *consoleState, width float32, focus bool) {
	th := c.Theme()
	bodyID := c.Next("transcript")
	if focus || st.focus {
		c.Focus(bodyID)
		st.focus = false
	}
	if st.sel == "" {
		st.scroll.follow = true
	}
	var keys []string
	asks := slices.Clone(se.Asks)
	scrolled(c, "transcript", &st.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: AgentKeyMode, Help: "agent/console#transcript",
		Role: ui.RoleList, Label: "Transcript"}, ui.Style{Gap: 10, Pad: ui.Insets{T: 12, B: 16}, Cross: ui.Center}, func(c *ui.Ctx, block func(string) ui.ID) {
		emitAsks := func(before time.Time) {
			for len(asks) > 0 && (before.IsZero() || !asks[0].Created.IsZero() && asks[0].Created.Before(before)) {
				a := asks[0]
				asks = asks[1:]
				k := "ask:" + a.ID
				block(k)
				keys = append(keys, k)
				c.Box(k, ui.Style{W: ui.Px(width)}, func(c *ui.Ctx) { x.askCard(c, "card", se, a, false, st.sel == k) })
			}
		}
		if len(se.Transcript) == 0 && len(asks) == 0 {
			c.Text("empty", "Nothing yet: the agent's turns show here as they happen.", uiFont(12.5, th.C(theme.FgGhost)))
		}
		for _, it := range se.Transcript {
			if it.Agent != "" {
				continue
			}
			emitAsks(it.Time)
			k := "item:" + it.ID
			block(k)
			keys = append(keys, k)
			x.item(c, k, se, it, st, width)
		}
		if len(se.Subagents) > 0 {
			block("subagents")
			keys = append(keys, "subagents")
			x.subagents(c, se, width, st.sel == "subagents")
		}
		emitAsks(time.Time{})
		for i, f := range st.forks {
			k := fmt.Sprint("fork:", i)
			block(k)
			keys = append(keys, k)
			x.forkCard(c, k, f, width, st.sel == k)
		}
	})
	st.blocks = keys
	if st.sel != "" && !slices.Contains(keys, st.sel) {
		st.sel = ""
	}
}

// open is whether a block shows its body: its default, flipped by ⏎ or a click.
func (st *consoleState) open(key string, def bool) bool { return def != st.flip[key] }

func (st *consoleState) toggle(key string) {
	st.flip[key] = !st.flip[key]
	st.sel = key
}

// blockStyle is a transcript block's box: bordered, lit while selected.
func blockStyle(th *theme.Resolved, width float32, selected bool) ui.Style {
	stroke := th.C(tBlockLine)
	if selected {
		stroke = th.C(tBlockSelected)
	}
	return ui.Style{W: ui.Px(width), Fill: th.C(tBlockBg), Stroke: stroke, Border: 1, Radius: 6, Clip: true, Role: ui.RoleListItem, Selected: selected}
}

func isShell(tool string) bool {
	switch tool {
	case "Bash", "shell", "commandExecution", "exec_command", "local_shell":
		return true
	}
	return false
}

func lineCount(s string) int {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return 0
	}
	return strings.Count(s, "\n") + 1
}

func msText(ms int64) string {
	switch {
	case ms <= 0:
		return ""
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	d := time.Duration(ms) * time.Millisecond
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// item draws one transcript item.
func (x *agentConsole) item(c *ui.Ctx, k string, se core.SessionState, it core.Item, st *consoleState, width float32) {
	th := c.Theme()
	sel := st.sel == k
	switch it.Kind {
	case core.ItemAssistant:
		c.Row(k, ui.Style{W: ui.Px(width), Role: ui.RoleListItem, Selected: sel}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: map[bool]render.Color{true: th.C(tBlockSelected)}[sel]}, nil)
			c.Box("gap", ui.Style{W: ui.Px(6)}, nil)
			markdown(c, "md", lspMarkdown(it.Text), width-8)
		})
	case core.ItemThinking:
		open := st.open(k, false)
		c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
			if widget.Clickable(c, "head", ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 7), Gap: 8, Cross: ui.Center, Help: "agent/console#thinking",
				Label: "Thinking", Expanded: &open}, func(c *ui.Ctx) {
				c.Text("t", map[bool]string{true: "▾ thinking", false: "▸ thinking"}[open], monoFont(11, th.C(tThinking)))
				c.Text("n", fmt.Sprintf("%d lines", lineCount(it.Text)), monoFont(10, th.C(tThinking)))
			}).Clicked {
				st.toggle(k)
			}
			if open {
				c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 8}}, func(c *ui.Ctx) {
					widget.Wrap(c, "w", it.Text, width-24, uiFont(12, th.C(tThinking)))
				})
			}
		})
	case core.ItemUser:
		open := st.open(k, lineCount(it.Text) <= 3)
		c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
			if widget.Clickable(c, "head", ui.Style{Dir: ui.Row, W: ui.Flex(1), Pad: ui.PadXY(12, 7), Gap: 8, Cross: ui.Center, Help: "agent/console#transcript",
				Label: "Prompt", Expanded: &open}, func(c *ui.Ctx) {
				c.Text("k", "YOU", kickerFont(th.C(theme.Accent)))
				if !open {
					c.Text("t", firstLine(strings.TrimSpace(it.Text)), uiFont(12, th.C(theme.FgMuted)))
				}
			}).Clicked {
				st.toggle(k)
			}
			if open {
				c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, B: 8}}, func(c *ui.Ctx) {
					widget.Wrap(c, "w", it.Text, width-24, uiFont(12.5, th.C(theme.Fg)))
				})
			}
		})
	case core.ItemError:
		s := blockStyle(th, width, sel)
		s.Stroke, s.Pad, s.Fill = th.C(theme.Error), ui.PadXY(12, 8), theme.WithAlpha(th.C(theme.Error), .06)
		c.Column(k, s, func(c *ui.Ctx) { widget.Wrap(c, "w", it.Text, width-24, uiFont(12.5, th.C(theme.Error))) })
	case core.ItemSystem:
		c.Row(k, ui.Style{W: ui.Px(width), Pad: ui.PadXY(8, 0), Role: ui.RoleListItem, Selected: sel}, func(c *ui.Ctx) {
			c.Text("t", it.Text, monoFont(10.5, th.C(theme.FgFaint)))
		})
	default:
		x.tool(c, k, se, it, st, width)
	}
}

// tool draws a tool call: a dot for how it went, its title, a summary at the right, and its edit as a mini-diff or
// its output folded.
func (x *agentConsole) tool(c *ui.Ctx, k string, se core.SessionState, it core.Item, st *consoleState, width float32) {
	th := c.Theme()
	sel := st.sel == k
	dot := th.C(tToolRunning)
	switch {
	case it.IsError:
		dot = th.C(tToolFailed)
	case it.Done:
		dot = th.C(tToolOK)
	}
	title := cmpOr(it.Title, it.Tool)
	verb, rest, _ := strings.Cut(title, " ")
	if verb == "Grep" {
		verb = "Search"
	}
	hasOutput := it.Output != "" && it.Diff == nil
	open := st.open(k, it.Diff != nil || it.IsError && hasOutput)
	var summary []string
	if it.Diff == nil && it.Done {
		if n := lineCount(it.Output); n > 0 {
			switch {
			case verb == "Search" || verb == "Find":
				summary = append(summary, fmt.Sprintf("%d %s", n, plural(n, "hit", "hits")))
			default:
				summary = append(summary, fmt.Sprintf("%d %s", n, plural(n, "line", "lines")))
			}
		}
	}
	if d := msText(it.Duration); d != "" {
		summary = append(summary, d)
	}
	c.Column(k, blockStyle(th, width, sel), func(c *ui.Ctx) {
		head := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Help: "agent/console#tool-calls",
			Label: title}
		if hasOutput || it.Diff != nil {
			head.Expanded = &open
		}
		if widget.Clickable(c, "head", head, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: dot}, nil)
			c.Text("verb", verb, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("rest", truncate(rest, 90), monoFont(11.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if it.Diff != nil {
				widget.DiffStat(c, "stat", it.Diff.Added, it.Diff.Removed, widget.DiffStatOpts{})
				if widget.Link(c, "diff", "view in diff ↗", "agent/console#tool-calls") {
					x.openDiff(se, it.Diff.Path)
				}
			}
			if len(summary) > 0 {
				c.Text("sum", strings.Join(summary, " · "), monoFont(10, th.C(theme.FgGhost)))
			}
			if r, ok := runOf(se, it.ID); ok {
				evidenceChip(c, "run", x.h, se.ID, r.ID, runLabel(r), r.OK)
			}
			if hasOutput && isShell(it.Tool) {
				c.Text("fold", map[bool]string{true: "▾", false: "▸"}[open], monoFont(11, th.C(theme.FgFaint)))
			}
		}).Clicked {
			st.toggle(k)
		}
		if !open {
			return
		}
		if it.Diff != nil {
			miniDiff(c, it.Diff)
			return
		}
		if hasOutput {
			c.Column("out", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 8), Gap: 1, Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
				lines := strings.Split(strings.TrimRight(it.Output, "\n"), "\n")
				if len(lines) > 200 {
					lines = append(lines[:100:100], append([]string{fmt.Sprintf("… %d lines …", len(lines)-200)}, lines[len(lines)-100:]...)...)
				}
				tok := theme.FgMuted
				if it.IsError {
					tok = theme.Error
				}
				for i, l := range lines {
					c.Text(i, strings.ReplaceAll(l, "\t", "    "), monoFont(11.5, th.C(tok)))
				}
			})
		}
	})
}

// runOf is the test run a transcript item ran: the evidence it's a link to.
func runOf(se core.SessionState, item string) (core.TestRun, bool) {
	for _, r := range se.Runs {
		if r.Item == item && item != "" {
			return r, true
		}
	}
	return core.TestRun{}, false
}

// miniDiff is an edit's lines: numbers, the op, the text, on the diff colours.
func miniDiff(c *ui.Ctx, d *core.FileDiff) {
	th := c.Theme()
	c.Column("diff", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 2, B: 6}, Role: ui.RoleText, Label: fmt.Sprintf("+%d -%d", d.Added, d.Removed)}, func(c *ui.Ctx) {
		for i, l := range d.Lines {
			fill, num, tok := render.Color{}, l.New, theme.FgMuted
			switch l.Op {
			case "+":
				fill, tok = th.C(theme.DiffAdd), theme.Fg
			case "-":
				fill, num, tok = th.C(theme.DiffDel), l.Old, theme.Fg
			}
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(19), Pad: ui.PadXY(12, 0), Gap: 12, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
				n := ""
				if num > 0 {
					n = fmt.Sprint(num)
				}
				numTok := theme.FgGhost
				switch l.Op {
				case "+":
					numTok = tToolOK
				case "-":
					numTok = tToolFailed
				}
				c.Box("n", ui.Style{W: ui.Px(32)}, func(c *ui.Ctx) { c.Text("t", n, monoFont(11, th.C(numTok))) })
				c.Box("op", ui.Style{W: ui.Px(10)}, func(c *ui.Ctx) { c.Text("t", strings.TrimSpace(l.Op), monoFont(11.5, th.C(numTok))) })
				c.Text("t", strings.ReplaceAll(l.Text, "\t", "    "), monoFont(11.5, th.C(tok)))
			})
		}
	})
}

// openDiff opens an edited file's diff, or the file until something registers diff.open.
func (x *agentConsole) openDiff(se core.SessionState, path string) {
	if !filepath.IsAbs(path) {
		if p, ok := se.Primary(); ok {
			path = filepath.Join(p.Dir, path)
		}
	}
	a := registry.Args{"path": path}
	ctx := context.Background()
	if x.h.Available("diff.open", a) {
		x.run(ctx, "diff.open", a)
		return
	}
	x.run(ctx, "editor.open", a)
}

// subagents is the box of the helpers the agent started: what each does, for how long, with how many tools.
func (x *agentConsole) subagents(c *ui.Ctx, se core.SessionState, width float32, sel bool) {
	th := c.Theme()
	running := 0
	for _, s := range se.Subagents {
		if s.Running {
			running++
		}
	}
	now := c.Now()
	subs := se.Subagents[max(0, len(se.Subagents)-6):]
	c.Column("subagents", blockStyle(th, width, sel), func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "SUBAGENTS", kickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d running", running), monoFont(10, th.C(theme.FgFaint)))
		})
		for i, s := range subs {
			c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 10, Cross: ui.Center, Role: ui.RoleListItem, Label: s.Description},
				func(c *ui.Ctx) {
					tone := th.C(tToolOK)
					if s.Running {
						tone = th.C(tToolRunning)
					}
					c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: tone}, nil)
					c.Text("type", cmpOr(s.Type, "agent"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.Medium},
						Color: th.C(theme.Fg)})
					c.Text("desc", truncate(s.Description, 80), uiFont(12, th.C(theme.FgMuted)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					end := now
					if !s.Ended.IsZero() {
						end = s.Ended
					}
					el := ""
					if !s.Started.IsZero() {
						d := end.Sub(s.Started)
						el = fmt.Sprintf("%d:%02d · ", int(d.Minutes()), int(d.Seconds())%60)
					}
					c.Text("n", fmt.Sprintf("%s%d %s", el, s.Tools, plural(s.Tools, "tool", "tools")), monoFont(10, th.C(theme.FgGhost)))
				})
		}
	})
}

// forkCard is a side question and its answer, from a fork that changes nothing.
func (x *agentConsole) forkCard(c *ui.Ctx, k string, f forkAnswer, width float32, sel bool) {
	th := c.Theme()
	s := blockStyle(th, width, sel)
	s.Pad, s.Gap = ui.PadXY(14, 10), 6
	c.Column(k, s, func(c *ui.Ctx) {
		c.Row("q", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "BTW", kickerFont(th.C(theme.Accent)))
			c.Text("t", f.q, uiFont(12.5, th.C(theme.Fg)))
		})
		switch {
		case !f.done:
			c.Text("wait", "asking a fork…", monoFont(11, th.C(theme.FgFaint)))
		case f.err != nil:
			c.Text("err", f.err.Error(), uiFont(12, th.C(theme.Error)))
		default:
			markdown(c, "a", lspMarkdown(f.a), width-28)
		}
	})
}

// composer is where you write to the agent: queued messages over it, the input, and its chips and keys.
func (x *agentConsole) composer(c *ui.Ctx, se core.SessionState, st *consoleState) {
	th := c.Theme()
	c.Column("composer", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(24, 10), Gap: 8, Help: "agent/console#composer"}, func(c *ui.Ctx) {
		for i, q := range se.Agent.Queue {
			c.Row(fmt.Sprint("q", i), ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				widget.Chip(c, "chip", "queued", th.C(tQueued), "agent/console#composer")
				c.Text("t", truncate(firstLine(strings.TrimSpace(q)), 120), uiFont(12, th.C(theme.FgMuted)))
			})
		}
		if st.forking {
			fid := c.Next("fork")
			if st.focusNow == "fork" {
				c.Focus(fid)
				st.focusNow = ""
			}
			r := widget.Input(c, "fork", &st.forkQ, widget.InputOpts{Label: "Side question", Placeholder: "Ask on the side: answered from the agent's context, not part of the conversation",
				Help: "agent/console#fork"})
			switch {
			case r.Submitted && strings.TrimSpace(st.forkQ) != "":
				x.run(context.Background(), "agent.fork", registry.Args{"prompt": st.forkQ})
				st.forkQ, st.forking, st.focus = "", false, true
			case r.Cancelled:
				st.forking, st.focus = false, true
			}
		}
		id := c.Next("input")
		if st.compose {
			c.Focus(id)
			st.compose = false
		}
		placeholder := "Write to the agent: ⏎ sends"
		if se.Agent.Working {
			placeholder = "Write to the agent: ⏎ queues it for the turn's end, ⌘⏎ sends now"
		}
		r := widget.Input(c, "input", &st.draft, widget.InputOpts{Label: "Message the agent", Placeholder: placeholder, Help: "agent/console#composer",
			KeyMode: ComposerKeyMode})
		switch {
		case r.Submitted:
			x.run(context.Background(), "agent.console.send", nil)
		case r.Cancelled:
			st.focus = true
		}
		c.Row("chips", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			if m := se.Agent.Model; m != "" {
				widget.Chip(c, "model", m, th.C(theme.FgMuted), "agent/console#composer")
			}
			if e := se.Agent.Effort; e != "" {
				widget.Chip(c, "effort", "effort "+e, th.C(theme.FgMuted), "agent/console#composer")
			}
			if p := se.Agent.Policy; p != "" {
				widget.Chip(c, "policy", "gate "+p, th.C(theme.FgMuted), "agent/console#composer")
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "@path names a file · ⏎ send · ⌘⏎ send now", monoFont(10, th.C(theme.FgGhost)))
		})
	})
}
