package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// AgentConsoleView is the agent console: the session in front's structured transcript, its subagents and
// asks, a composer, and the session's stats.
const AgentConsoleView = kit.AgentConsoleView

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
	tToolRunning   = theme.Declare("agent.tool.running", "status.working")
	tThinking      = theme.Declare("agent.thinking.fg", "fg.ghost")
	tThreadKicker  = theme.Declare("agent.thread.kicker", "amber")
	tQueued        = theme.Declare("agent.queued", "amber")
	tAskPermission = theme.Declare("ask.permission", "status.permission")
	tAskNetwork    = theme.Declare("ask.network", "cyan")
	tAskGateway    = theme.Declare("ask.gateway", "amber")
	tAskQuestion   = theme.Declare("ask.question", "status.needs_you")
	tAskKey        = theme.Declare("ask.key", "cyan")
	tRuleAllow     = theme.Declare("agent.rule.allow", "ok")
	tRuleAsk       = theme.Declare("agent.rule.ask", "pink")
)

// Console is the console view, the asks' cards over every view, and the commands both run.
type Console struct {
	kit.Disposer
	h plugin.Host

	by        map[core.SessionID]*consoleState
	providers *plugin.Load[[]host.ProviderInfo] // the agents and their models, asked for once a composer shows
	detail    *detailOpen                       // the details dialog, while it's open
}

// consoleState is one session's console: what's selected, folded, typed.
type consoleState struct {
	sel      string          // the selected block's key; "" follows the end
	flip     map[string]bool // blocks folded or unfolded against their default
	blocks   []string        // the blocks' keys as last built, for j and k
	scroll   kit.Scroll
	side     kit.Scroll
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
	layout   consoleBlocks   // the transcript's blocks, as last laid out
	stopping map[string]bool // the processes being stopped, by session/id
}

type forkAnswer struct {
	q, a  string
	err   error
	done  bool
	since time.Time
}

func (x *Console) Manifest() plugin.Manifest { return plugin.Manifest{ID: "agentconsole"} }

func (x *Console) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by = map[core.SessionID]*consoleState{}
	for _, c := range append(x.commands(), append(x.processCommands(), x.detailCommand())...) {
		x.Add(h.Command(c))
	}
	x.Add(h.View(kit.OverlayPrefix+"console-detail", x.detailView()))
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	if kit.HasSessions(x.h) {
		// The rail offers the console where there are agents.
		x.Add(h.Contribute(uireg.RailEntry{Name: "agent", Icon: "agent", Title: "Agent console", Command: "agent.console", Order: 70,
			Active: func(string) bool { return x.shown() }, Badge: func() int {
				se, _ := x.front()
				return len(se.Asks)
			}}))
	}
	x.Add(h.View(AgentConsoleView, consoleView{x}))
	x.Add(h.Contribute(uireg.InboxSource{Name: "asks", Cards: x.askCards, Source: "builtin:agentconsole"}))
	return nil
}

func (x *Console) commands() []registry.Command {
	h := x.h
	open := registry.Args{"id": AgentConsoleView, "place": "dock", "edge": "bottom", "size": 460, "title": "Agent"}
	session := func(a registry.Args) core.SessionID {
		if id := a.String("id"); id != "" {
			return core.SessionID(id)
		}
		return h.State().Focus
	}
	inFront := func(registry.Args) bool { return h.State().Focus != "" }
	return []registry.Command{
		{ID: "agent.console", Title: "Show the agent console of the session in front, or hide it", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.toggle", open)
		}},
		{ID: "agent.unqueue", Title: "Take a message out of {id}'s queue before the agent gets it: the one at {index}, still {text}",
			Run: func(_ registry.Ctx, a registry.Args) error {
				c, err := x.client(session(a))
				if err != nil {
					return err
				}
				return c.Unqueue(host.UnqueueReq{Session: session(a), Index: a.Int("index"), Text: a.String("text")})
			}},
		{ID: "agent.console.show", Title: "Show {id}'s agent console: its session in front, and the console open on it",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				return h.Run(ctx, "session.open", registry.Args{"id": string(session(a)),
					"then": []any{map[string]any{"command": "view.open", "args": map[string]any(open)}}})
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
				return c.Send(host.SendReq{Session: id, Text: text, Urgent: a.Bool("urgent")})
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
		{ID: "agent.terminal", Title: "Show the raw terminal of {id}'s agent, or the session in front's",
			Available: func(a registry.Args) bool { return inFront(a) && x.hasTerminal(session(a)) },
			Run:       func(ctx registry.Ctx, a registry.Args) error { return x.rawTerminal(ctx, session(a), "") }},
		{ID: "agent.login", Title: "Sign the agent of {id} in again, or the one waiting on {provider}'s login: its sign-in in the browser, or its own terminal with the login typed",
			Available: func(a registry.Args) bool { _, _, ok := x.loginWaiting(a); return ok },
			Run:       x.logIn},
		{ID: "ask.answer", Title: "Answer an ask: {ask} of {id} (the first of the session in front by default) with {decision} allow, always, deny, answer or recheck (a login), {note}, {scope}, {answers}",
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
}

func (x *Console) keymaps() []registry.Keymap {
	var out []registry.Keymap
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
		out = append(out, k)
	}
	return append(out,
		registry.Keymap{Mode: ComposerKeyMode, Keys: "<D-CR>", Command: "agent.console.send", Args: registry.Args{"urgent": true},
			Desc: "Send now, interrupting the turn"},
		registry.Keymap{Mode: "global", Keys: "<D-A>", Command: "agent.console", Desc: "Agent console"})
}

type consoleView struct{ x *Console }

func (v consoleView) Title(string) string {
	if se, ok := v.x.front(); ok && se.Agent.Provider != "" {
		return "Agent · " + kit.ProviderName(v.x.h, se.Agent.Provider)
	}
	return "Agent"
}
func (v consoleView) EdgeToEdge() {}
func (v consoleView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "console", Fn: func(c *ui.Ctx) { v.x.build(c, vc) }}
}

func (x *Console) front() (core.SessionState, bool) {
	st := x.h.State()
	if se, ok := st.Session(st.Focus); ok {
		return se.Snapshot(), true
	}
	return core.SessionState{}, false
}

func (x *Console) state(id core.SessionID) *consoleState {
	if id == "" {
		return nil
	}
	st := x.by[id]
	if st == nil {
		st = &consoleState{flip: map[string]bool{}, stopping: map[string]bool{}, chosen: map[string]map[int][]string{}, typed: map[string]string{}, scroll: kit.Scroll{Follow: true}}
		x.by[id] = st
	}
	return st
}

func (x *Console) shown() bool {
	for _, v := range x.h.Layout().Visible() {
		if v.ID == AgentConsoleView {
			return true
		}
	}
	return false
}

// agentChips are the composer's model and effort menus, which switch the agent (session.configure) while it's
// idle, and where it runs: the gate's policy outside a sandbox, the sandbox and its network inside one.
func (x *Console) agentChips(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	a := se.Agent
	const help = "agent/console#composer"
	if p, ok := x.provider(se.ID, a.Provider); ok && len(p.Models) > 0 {
		if m, e, picked := kit.ModelSelects(c, p, cmp.Or(a.Model, kit.DefaultModel(p)), a.Effort, a.Working, help); picked {
			x.run(context.Background(), "session.configure", registry.Args{"id": string(se.ID), "model": m, "effort": e})
		}
		if a.Working {
			c.Text("idle", "switch when the agent is idle", kit.MonoFont(10, th.C(theme.FgGhost)))
		}
	} else {
		if a.Model != "" {
			widget.Chip(c, "model", a.Model, th.C(theme.FgMuted), help)
		}
		if a.Effort != "" {
			widget.Chip(c, "effort", "effort "+a.Effort, th.C(theme.FgMuted), help)
		}
	}
	switch {
	case a.Sandboxed:
		where := kit.SandboxNoun(cmp.Or(a.Backend, kit.SettingBackend(x.h)))
		if a.Network != "" {
			where += " · net " + a.Network
		}
		widget.Chip(c, "sandbox", where, th.C(theme.FgMuted), help)
	case a.Policy != "":
		widget.Chip(c, "policy", "gate "+a.Policy, th.C(theme.FgMuted), help)
	}
}

// provider is what the session host says of the agent provider id: its models and their efforts. The first ask
// starts the probe, and it's not ok until that answers.
func (x *Console) provider(session core.SessionID, id string) (host.ProviderInfo, bool) {
	if x.providers == nil {
		w, err := x.client(session)
		if err != nil {
			return host.ProviderInfo{}, false
		}
		x.providers = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) ([]host.ProviderInfo, error) {
			return w.ProviderInfo(ctx), nil
		}, plugin.LoadOpts{Timeout: 20 * time.Second})
		x.providers.Start()
	}
	ps, _ := x.providers.Value()
	for _, p := range ps {
		if p.ID == id {
			return p, true
		}
	}
	return host.ProviderInfo{}, false
}

func (x *Console) client(id core.SessionID) (host.Access, error) {
	return kit.SessionHost(x.h)
}

// move selects the next or previous block.
func (x *Console) move(d int) {
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
	st.scroll.Reveal = st.sel
}

// ask is the ask id of session id, or the one selected in its console, or its first.
func (x *Console) ask(id core.SessionID, ask string) (core.Ask, bool) {
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

// answer sends the user's answer to an ask to the session host.
func (x *Console) answer(ctx context.Context, id core.SessionID, ask core.Ask, a registry.Args) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	r := host.AnswerReq{Session: id, Ask: ask.ID, Decision: a.String("decision"), Note: a.String("note"), Scope: a.String("scope")}
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
	return c.Answer(r)
}

func (x *Console) startNote(id core.SessionID, ask string) {
	if st := x.state(id); st != nil {
		st.noting, st.note, st.focusNow = ask, "", "note"
	}
}

// fork asks a side question, and shows the answer in the console when it comes.
func (x *Console) fork(id core.SessionID, prompt string) error {
	c, err := x.client(id)
	if err != nil {
		return err
	}
	st := x.state(id)
	st.forks = append(st.forks, forkAnswer{q: prompt, since: kit.WindowNow(x.h)})
	n := len(st.forks) - 1
	go func() {
		out, err := c.Fork(id, prompt)
		x.h.Do(func() {
			if n < len(st.forks) {
				st.forks[n].a, st.forks[n].err, st.forks[n].done = out, err, true
			}
		})
		x.h.Redraw()
	}()
	return nil
}

// hasTerminal says id's agent has a terminal of its own to show: no only when its provider says so (agent.Features).
func (x *Console) hasTerminal(id core.SessionID) bool {
	se, ok := x.h.Session(id)
	if !ok {
		return true
	}
	f, known := kit.ProviderFeatures(x.h, se.Snapshot().Agent.Provider)
	return !known || f.Terminal
}

// rawTerminal shows the agent's own terminal, streamed from the session host, in a terminal view, and types into it.
func (x *Console) rawTerminal(ctx registry.Ctx, id core.SessionID, typed string) error {
	if se, ok := x.h.Session(id); ok {
		if !x.hasTerminal(id) {
			return fmt.Errorf("%s has no terminal of its own", kit.ProviderName(x.h, se.Snapshot().Agent.Provider))
		}
		if a := se.Snapshot().Agent; a.Starting != nil {
			return fmt.Errorf("%s has no terminal yet: it's still starting (%s)", kit.ProviderName(x.h, a.Provider), a.Starting.Step)
		}
	}
	c, err := x.client(id)
	if err != nil {
		return err
	}
	src, err := newAgentTerm(c, id)
	if err != nil {
		return err
	}
	if err := x.h.Run(ctx, "term.open", registry.Args{"source": src, "title": "agent · " + string(id), "place": "tab", "instance": "agent-" + string(id)}); err != nil {
		return err
	}
	if typed != "" {
		_, err = src.Write([]byte(typed))
	}
	return err
}

// build draws the console for the session in front.
func (x *Console) build(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	c.Column("console", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tConsoleBg), Role: ui.RoleRegion, Label: "Agent console", Help: "agent/console"},
		func(c *ui.Ctx) {
			if !ok {
				c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("t", "No session in front: start one with ⌘N, and its agent shows here.", kit.UIFont(12.5, th.C(theme.FgFaint)))
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
					x.transcript(c, se, st, width)
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					x.composer(c, se, st)
				})
				c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				x.side(c, se)
			})
		})
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
	case len(se.Agent.Background) > 0:
		return "working in the background", theme.StatusWorking
	case se.Agent.Starting != nil:
		return "starting", theme.StatusWorking
	case se.Agent.Error != "":
		return "failed", theme.Error
	case se.Agent.Provider != "" && !se.Agent.Running:
		return "stopped", theme.StatusOff
	}
	return "idle", theme.StatusQuiet
}

func (x *Console) header(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	a := se.Agent
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(44), Pad: ui.PadXY(16, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		name := kit.ProviderName(x.h, a.Provider)
		c.Box("badge", ui.Style{W: ui.Px(22), H: ui.Px(22), Radius: 5, Fill: th.C(theme.BgOverlay), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(name[:min(2, len(name))]), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold},
				Color: th.C(theme.Fg)})
		})
		c.Text("name", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		word, tok := consoleStatus(se)
		widget.Chip(c, "status", word, th.C(tok), "agent/console#header")
		var where []string
		if se.Ticket != "" {
			where = append(where, se.Ticket)
		}
		where = append(where, kit.Label(se))
		branch := se.Branch
		if p, ok := se.Primary(); ok && p.Branch != "" {
			branch = p.Branch
		}
		if branch != "" {
			where = append(where, branch)
		}
		c.Text("where", strings.Join(where, " · "), kit.MonoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		ctx := context.Background()
		if widget.Button(c, "fork", "Fork", widget.ButtonOpts{Help: "agent/console#fork"}) {
			x.run(ctx, "agent.fork", nil)
		}
		if widget.Button(c, "interrupt", "Interrupt", widget.ButtonOpts{Key: "esc", Disabled: !a.Working, Help: "agent/console#interrupt"}) {
			x.run(ctx, "session.interrupt", nil)
		}
		if x.hasTerminal(se.ID) && widget.Button(c, "raw", "Raw terminal", widget.ButtonOpts{Help: "agent/console#raw-terminal"}) {
			x.run(ctx, "agent.terminal", nil)
		}
	})
}

// run runs a command from a click, saying what went wrong in a notice.
func (x *Console) run(ctx context.Context, id string, a registry.Args) {
	if err := x.h.Run(ctx, id, a); err != nil {
		x.h.Notify(core.Notice{ID: "agent.console", Severity: core.SevError, Source: "agent", Text: err.Error(), Time: time.Now()})
	}
}

// threadCard is the request the turn is working on: its anchor, and what you asked.
func (x *Console) threadCard(c *ui.Ctx, se core.SessionState, width float32) {
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
			// Both fit the card: the kicker takes at most 45% of it, the request the rest.
			inner := width - 28 - 10
			kf, bf := kit.KickerFont(th.C(tThreadKicker)), kit.UIFont(12.5, th.C(theme.Fg))
			kicker := ui.Elide(c.Shaper(), strings.ToUpper("thread · "+where), kf.Font, inner*0.45)
			c.Text("k", kicker, kf)
			left := inner - c.Shaper().Shape(kicker, kf.Font).Advance
			c.Text("b", ui.Elide(c.Shaper(), core.Summary(body), bf.Font, left), bf)
		})
	})
}

// transcript is the session's transcript as blocks, with its asks where they arose, its subagents and side answers.
// Entering the console focuses it.
func (x *Console) transcript(c *ui.Ctx, se core.SessionState, st *consoleState, width float32) {
	th := c.Theme()
	if st.sel == "" {
		st.scroll.Follow = true
	}
	// The blocks first, as keys and what each draws: only those in view are drawn, so a long session's transcript
	// costs what its screenful does. They're laid out again only when the transcript, the asks or the rest change.
	blocks := st.layout.of(se, len(st.forks))
	draw := func(c *ui.Ctx, b consoleBlock) {
		switch b.kind {
		case blockLead:
			if se.Agent.Starting != nil {
				startingCard(c, "starting", se, width)
			} else {
				c.Text("empty", "Nothing yet: the agent's turns show here as they happen.", kit.UIFont(12.5, th.C(theme.FgGhost)))
			}
		case blockAsk:
			c.Box(b.key, ui.Style{W: ui.Px(width)}, func(c *ui.Ctx) { x.askCard(c, "card", se, se.Asks[b.at], false, st.sel == b.key, nil) })
		case blockItem:
			x.item(c, b.key, se, se.Transcript[b.at], st, width)
		case blockSubagents:
			x.subagents(c, se, width, st.sel == "subagents")
		case blockBackground:
			x.background(c, se, width, st.sel == "background")
		case blockProcesses:
			x.processes(c, se, st, width, st.sel == "processes")
		case blockFork:
			x.forkCard(c, b.key, st.forks[b.at], width, st.sel == b.key)
		}
	}
	keys := st.layout.keys
	bodyID := c.Next("transcript")
	if st.focus {
		c.RequestFocus(bodyID)
		st.focus = false
	}
	reveal := -1
	if k := st.scroll.Reveal; k != "" {
		reveal = slices.IndexFunc(blocks, func(b consoleBlock) bool { return b.key == k })
		st.scroll.Follow, st.scroll.Reveal = false, ""
	}
	c.Items("transcript", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, DefaultFocus: true, KeyMode: AgentKeyMode, Help: "agent/console#transcript",
		Role: ui.RoleList, Label: "Transcript", Selectable: true}, len(blocks),
		ui.ItemsOpts{Estimate: 60, Reveal: reveal, End: st.scroll.Follow, Key: func(i int) any { return blocks[i].key }}, func(c *ui.Ctx, i int) {
			pad := ui.Insets{B: 10}
			if i == 0 {
				pad.T = 12
			}
			if i == len(blocks)-1 {
				pad.B = 16
			}
			c.Column("block", ui.Style{W: ui.Flex(1), Pad: pad, Cross: ui.Center}, func(c *ui.Ctx) { draw(c, blocks[i]) })
		})
	st.blocks = keys
	if st.sel != "" && !slices.Contains(keys, st.sel) {
		st.sel = ""
	}
}

// composer is where you write to the agent: the working line and queued messages over it, the input, and its chips
// and keys.
func (x *Console) composer(c *ui.Ctx, se core.SessionState, st *consoleState) {
	th := c.Theme()
	c.Column("composer", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(24, 10), Gap: 8, Help: "agent/console#composer"}, func(c *ui.Ctx) {
		workingLine(c, "working", se, ui.Flex(1))
		for i, q := range se.Agent.Queue {
			c.Row(fmt.Sprint("q", i), ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				widget.Chip(c, "chip", "queued", th.C(tQueued), "agent/console#composer")
				c.Row("t", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", kit.FirstLine(q), kit.UIFont(12, th.C(theme.FgMuted)))
				})
				// Edit takes it out of the queue into the composer, to send again once changed.
				if widget.Link(c, "edit", "edit", "agent/console#composer") {
					if err := x.h.Run(context.Background(), "agent.unqueue", registry.Args{"id": string(se.ID), "index": i, "text": q}); err == nil {
						st.draft, st.compose = strings.TrimSpace(q+"\n"+st.draft), true
					} else {
						x.h.Notify(core.Notice{ID: "agent.console", Severity: core.SevError, Source: "agent", Text: err.Error()})
					}
				}
				if widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Cancel queued: " + kit.FirstLine(q), Help: "agent/console#composer", Size: 18}) {
					x.run(context.Background(), "agent.unqueue", registry.Args{"id": string(se.ID), "index": i, "text": q})
				}
			})
		}
		if st.forking {
			fid := c.Next("fork")
			if st.focusNow == "fork" {
				c.RequestFocus(fid)
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
			c.RequestFocus(id)
			st.compose = false
		}
		placeholder := "Write to the agent: ⏎ sends"
		if se.Agent.Working {
			placeholder = "Write to the agent: ⏎ queues it for the turn's end, ⌘⏎ sends now"
		}
		r := widget.Prose(c, "input", &st.draft, widget.ProseOpts{Label: "Message the agent", Placeholder: placeholder, Help: "agent/console#composer",
			KeyMode: ComposerKeyMode, EnterSubmits: true})
		switch {
		case r.Submitted:
			x.run(context.Background(), "agent.console.send", nil)
		case r.Cancelled:
			st.focus = true
		}
		c.Row("chips", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			x.agentChips(c, se)
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("keys", "@path names a file · ⏎ send · ⌘⏎ send now", kit.MonoFont(10, th.C(theme.FgGhost)))
		})
	})
}
