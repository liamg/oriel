package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/sandbox/gateway"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// askCards are the asks waiting on you, from every session, as the inbox's cards (ui.md 12): stacked over the
// window, except the session in front's while its console shows them; a security-key touch on its own at the top.
func (x *agentConsole) askCards() []registry.InboxCard {
	st := x.h.State()
	inConsole := x.shown()
	var out []registry.InboxCard
	for _, se := range st.Sessions {
		for _, a := range se.Asks {
			card := registry.InboxCard{ID: string(se.ID) + "/" + a.ID, Type: string(a.Kind), Group: askGroup(a.Kind), Mine: true, Session: string(se.ID),
				Icon: string(a.Kind), Tone: askTone(a.Kind).Name(), Created: a.Created,
				Interrupt: se.ID != st.Focus || !inConsole || a.Kind == core.AskKey, Banner: a.Kind == core.AskKey,
				Draw: func(c *ui.Ctx, key string, p registry.CardPlace) {
					x.askCard(c, key, se, a, p.Stacked, false, p.Dismiss)
				}}
			title, sub := x.askRow(se, a)
			card.Title, card.Sub = title, joinParts(sub, cmpOr(se.Name, string(se.ID)))
			out = append(out, card)
		}
	}
	return out
}

// askGroup is the Inbox filter an ask is under.
func askGroup(k core.AskKind) string {
	switch k {
	case core.AskNetwork, core.AskGateway:
		return registry.GroupSandbox
	case core.AskKey:
		return ""
	}
	return registry.GroupAgents
}

// askRow is an ask as a row of the Inbox panel: its title, and what it's about.
func (x *agentConsole) askRow(se core.SessionState, a core.Ask) (title, sub string) {
	prov := providerName(se.Agent.Provider)
	switch a.Kind {
	case core.AskPermission:
		return "Permission: " + a.Tool + "(" + cmpOr(a.Summary, mainInput(a.Input)) + ")", strings.ToLower(prov)
	case core.AskNetwork:
		return "Network: " + a.Host, joinParts("sandbox", a.Detail)
	case core.AskGateway:
		return cmpOr(a.Summary, a.Tool), joinParts("gateway", a.Detail)
	case core.AskQuestion:
		q := ""
		if len(a.Questions) > 0 {
			q = ": " + a.Questions[0].Question
		}
		return prov + " asks" + q, "question"
	case core.AskLogin:
		return prov + " needs you to log in", "login"
	case core.AskKey:
		return "Touch your security key", a.Summary
	}
	return cmpOr(a.Summary, string(a.Kind)), string(a.Kind)
}

// joinParts joins the parts that say something with " · ", once each.
func joinParts(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return strings.Join(out, " · ")
}

// askTone is an ask's colour, by kind.
func askTone(k core.AskKind) theme.TokenID {
	switch k {
	case core.AskNetwork:
		return tAskNetwork
	case core.AskGateway:
		return tAskGateway
	case core.AskQuestion, core.AskLogin:
		return tAskQuestion
	case core.AskKey:
		return tAskKey
	}
	return tAskPermission
}

// domainOf is a host's registered domain, roughly: its last two labels.
func domainOf(hostport string) (domain, port string) {
	h, port, err := net.SplitHostPort(hostport)
	if err != nil {
		h = hostport
	}
	parts := strings.Split(h, ".")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "."), port
}

// reviewState is the review ledger where a gateway call commits, pushes or raises a PR (ui.md 12, "Approvals show
// the review state right where you approve a push"): "21 agent lines unread", or "0 unreviewed lines ✓".
func reviewState(h plugin.Host, se core.SessionState, a core.Ask) string {
	tool := strings.TrimPrefix(a.Tool, gateway.Prefix)
	if tool != "commit" && tool != "push" && tool != "pr_create" {
		return ""
	}
	var in struct {
		Project string `json:"project"`
	}
	_ = json.Unmarshal([]byte(a.Input), &in)
	p, ok := se.Primary()
	for _, q := range se.Projects {
		if in.Project != "" && q.Name == in.Project {
			p, ok = q, true
		}
	}
	if !ok || p.Dir == "" {
		return ""
	}
	l, found := registry.LedgerOf(h, p.Dir)
	if !found || !l.Ready {
		return ""
	}
	if n := l.All.NotRead(); n > 0 {
		return fmt.Sprintf("%d agent %s unread", n, plural(n, "line", "lines"))
	}
	return "0 unreviewed lines ✓"
}

// askCard is an ask as a card, with what answers it: in the console where it arose, or stacked over the window
// with its session's name (kicker). With dismiss, its ✕ and Escape take it off the stack.
func (x *agentConsole) askCard(c *ui.Ctx, key string, se core.SessionState, a core.Ask, kicker bool, selected bool, dismiss func()) {
	th := c.Theme()
	st := x.state(se.ID)
	prov := providerName(se.Agent.Provider)
	tone := th.C(askTone(a.Kind))
	o := widget.ActionCardOpts{Tone: tone, Selected: selected, Help: "agent/asks#" + string(a.Kind), Dismiss: dismiss != nil}
	if kicker {
		o.Kicker = cmpOr(se.Name, string(se.ID)) + " · " + string(a.Kind)
	}
	type act struct {
		cmd  string // ask.answer if ""
		args registry.Args
		note bool
	}
	var acts []act
	add := func(label, key string, primary bool, args registry.Args) {
		o.Actions = append(o.Actions, widget.CardAction{Label: label, Key: key, Primary: primary})
		acts = append(acts, act{args: args})
	}
	var body func(c *ui.Ctx)
	switch a.Kind {
	case core.AskPermission:
		o.Title = prov + " wants to use " + a.Tool
		if isShell(a.Tool) {
			o.Title = prov + " wants to run a command"
		}
		o.Code = "$ " + cmpOr(a.Summary, mainInput(a.Input))
		if !isShell(a.Tool) {
			o.Code = cmpOr(a.Summary, mainInput(a.Input))
		}
		if se.Agent.Sandboxed {
			o.Meta = "sandbox · network " + cmpOr(se.Agent.Network, "ask")
		} else {
			o.Meta = "on this machine"
		}
		if a.Rule != "" {
			o.Note = "rule: " + a.Rule
		}
		add("Allow once", "y", true, registry.Args{"decision": "allow"})
		add("Always in this workspace", "a", false, registry.Args{"decision": "always"})
		add("Deny", "n", false, registry.Args{"decision": "deny"})
		o.Actions = append(o.Actions, widget.CardAction{Label: "Deny with note…", Key: "N"})
		acts = append(acts, act{note: true})
	case core.AskNetwork:
		o.Title = "Network: " + a.Host
		o.Detail = a.Detail
		o.Inline = true
		domain, port := domainOf(a.Host)
		h, _, _ := net.SplitHostPort(a.Host)
		add("Allow host", "y", false, registry.Args{"decision": "always", "scope": a.Host})
		if h != "" && h != domain {
			scope := "*." + domain
			if port != "" {
				scope += ":" + port
			}
			add("Allow *."+domain, "a", false, registry.Args{"decision": "always", "scope": scope})
		}
		add("Once", "o", false, registry.Args{"decision": "allow"})
		add("Deny", "n", false, registry.Args{"decision": "deny"})
	case core.AskGateway:
		o.Title = cmpOr(a.Summary, a.Tool)
		o.Detail = a.Detail
		if !kicker {
			o.Kicker = "gateway"
		}
		if a.Rule != "" {
			o.Note = "rule: " + a.Rule
		}
		if r := reviewState(x.h, se, a); r != "" {
			o.Note = strings.TrimPrefix(o.Note+" · "+r, " · ")
		}
		add("Allow", "y", true, registry.Args{"decision": "allow"})
		if a.Rule != "" {
			add("Always", "a", false, registry.Args{"decision": "always"})
		}
		add("Deny", "n", false, registry.Args{"decision": "deny"})
	case core.AskLogin:
		l := core.Login{Provider: se.Agent.Provider}
		if a.Login != nil {
			l = *a.Login
		}
		o.Title = providerName(l.Provider) + " needs you to log in"
		o.Detail = l.Detail
		if !kicker {
			o.Kicker = "login"
		}
		if canLogIn(l) {
			o.Note = "opens its terminal with " + l.Terminal
			o.Actions = append(o.Actions, widget.CardAction{Label: "Log in", Key: "l", Primary: true})
			acts = append(acts, act{cmd: "agent.login"})
		} else if l.Shell != "" {
			o.Note = "run " + l.Shell + " in a terminal"
		}
		add("Check again", "c", !canLogIn(l), registry.Args{"decision": "recheck"})
	case core.AskKey:
		o.Kicker = ""
		o.Title = "Touch your security key"
		o.Detail = joinParts(a.Summary, a.Detail, cmpOr(se.Name, string(se.ID)))
		o.Icon = func(c *ui.Ctx, tone render.Color) { icon(c, "icon", "key", 15, tone) }
		add("Cancel", "c", false, registry.Args{"decision": "deny"})
	case core.AskQuestion:
		o.Title = prov + " asks"
		body = func(c *ui.Ctx) { x.questions(c, se, a, st) }
		if len(a.Questions) > 1 || len(a.Questions) == 1 && a.Questions[0].Multi {
			o.Actions = append(o.Actions, widget.CardAction{Label: "Answer", Primary: true})
			acts = append(acts, act{args: registry.Args{"answers": x.answers(st, a)}})
		}
	}
	if st.noting == a.ID {
		inner := body
		body = func(c *ui.Ctx) {
			if inner != nil {
				inner(c)
			}
			x.noteInput(c, se, a, st)
		}
	}
	res := widget.ActionCard(c, key, o, body)
	if res.Dismissed && dismiss != nil {
		dismiss()
	}
	if res.Action < 0 {
		return
	}
	if acts[res.Action].note {
		x.startNote(se.ID, a.ID)
		return
	}
	args := registry.Args{"id": string(se.ID), "ask": a.ID}
	for k, v := range acts[res.Action].args {
		args[k] = v
	}
	x.run(context.Background(), cmpOr(acts[res.Action].cmd, "ask.answer"), args)
}

// canLogIn is a login the app signs in again itself: the agent's own terminal.
func canLogIn(l core.Login) bool { return l.Terminal != "" }

// loginWaiting is the session waiting on a login, and its ask: session {id}, else the one in front, else any
// waiting on {provider}'s login.
func (x *agentConsole) loginWaiting(a registry.Args) (core.SessionState, core.Login, bool) {
	st := x.h.State()
	id, provider := core.SessionID(a.String("id")), a.String("provider")
	ss := slices.Clone(st.Sessions)
	slices.SortStableFunc(ss, func(p, q core.SessionState) int {
		switch {
		case p.ID == st.Focus:
			return -1
		case q.ID == st.Focus:
			return 1
		}
		return 0
	})
	for _, se := range ss {
		if id != "" && se.ID != id {
			continue
		}
		for _, ask := range se.Asks {
			if ask.Kind != core.AskLogin || ask.Login == nil {
				continue
			}
			l := *ask.Login
			if provider == "" || l.Provider == provider {
				return se, l, true
			}
		}
	}
	return core.SessionState{}, core.Login{}, false
}

// logIn signs an agent in again: its own terminal, opened with the provider's login typed; for an agent with no
// terminal to type it in, it says what to run.
func (x *agentConsole) logIn(ctx registry.Ctx, a registry.Args) error {
	se, l, ok := x.loginWaiting(a)
	if !ok {
		return errors.New("no agent is waiting on a login")
	}
	if !canLogIn(l) {
		return fmt.Errorf("run %s in a terminal", cmpOr(l.Shell, "the agent's login"))
	}
	return x.rawTerminal(ctx, se.ID, l.Terminal+"\r")
}

// noteInput is a denial's note, which goes back to the agent with it.
func (x *agentConsole) noteInput(c *ui.Ctx, se core.SessionState, a core.Ask, st *consoleState) {
	c.Row("note", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		id := c.Next("in")
		if st.focusNow == "note" {
			c.Focus(id)
			st.focusNow = ""
		}
		r := widget.Input(c, "in", &st.note, widget.InputOpts{Label: "A note for the agent", Placeholder: "Why not, and what to do instead: ⏎ denies with it",
			Help: "agent/asks#deny-with-a-note"})
		send := widget.Button(c, "send", "Deny with this note", widget.ButtonOpts{Help: "agent/asks#deny-with-a-note"})
		switch {
		case r.Submitted || send:
			x.run(context.Background(), "ask.answer", registry.Args{"id": string(se.ID), "ask": a.ID, "decision": "deny", "note": st.note})
			st.focus = true
		case r.Cancelled:
			st.noting, st.note, st.focus = "", "", true
		}
	})
}

// answers are a question's answers as picked so far, by question.
func (x *agentConsole) answers(st *consoleState, a core.Ask) map[string]string {
	out := map[string]string{}
	for i, q := range a.Questions {
		if picked := st.chosen[a.ID][i]; len(picked) > 0 {
			out[q.Question] = strings.Join(picked, ", ")
		}
	}
	if t := strings.TrimSpace(st.typed[a.ID]); t != "" && len(a.Questions) > 0 {
		out[a.Questions[0].Question] = t
	}
	return out
}

// questions are an ask's questions, each with its options: a click on one answers a single question that takes
// one; the rest are picked, then answered together.
func (x *agentConsole) questions(c *ui.Ctx, se core.SessionState, a core.Ask, st *consoleState) {
	th := c.Theme()
	if st.chosen[a.ID] == nil {
		st.chosen[a.ID] = map[int][]string{}
	}
	picked := st.chosen[a.ID]
	help := "agent/asks#question"
	for i, q := range a.Questions {
		c.Column(fmt.Sprint("q", i), ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			if q.Header != "" {
				c.Text("h", strings.ToUpper(q.Header), kickerFont(th.C(theme.FgFaint)))
			}
			widget.Wrap(c, "q", q.Question, 380, uiFont(12.5, th.C(theme.Fg)))
			for j, opt := range q.Options {
				on := containsStr(picked[i], opt)
				fill, stroke := render.Color{}, th.C(theme.LineStrong)
				if on {
					fill, stroke = th.C(theme.AccentBg), th.C(theme.Accent)
				}
				in := widget.Clickable(c, fmt.Sprint("o", j), ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center,
					Fill: fill, Stroke: stroke, Border: 1, Radius: 6, Focusable: true, Help: help, Label: opt, Selected: on}, func(c *ui.Ctx) {
					glyph := "○"
					if on {
						glyph = "●"
					}
					c.Text("g", glyph, monoFont(11, th.C(theme.Accent)))
					c.Text("t", opt, uiFont(12.5, th.C(theme.Fg)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("n", fmt.Sprint(j+1), monoFont(10, th.C(theme.FgGhost)))
				})
				if !in.Clicked {
					continue
				}
				switch {
				case q.Multi && on:
					picked[i] = removeStr(picked[i], opt)
				case q.Multi:
					picked[i] = append(picked[i], opt)
				default:
					picked[i] = []string{opt}
				}
				if len(a.Questions) == 1 && !q.Multi {
					x.run(context.Background(), "ask.answer", registry.Args{"id": string(se.ID), "ask": a.ID, "answers": x.answers(st, a)})
				}
			}
		})
	}
	if len(a.Questions) == 1 {
		t := st.typed[a.ID]
		r := widget.Input(c, "typed", &t, widget.InputOpts{Label: "Your own answer", Placeholder: "or type an answer: ⏎ sends it", Help: help})
		st.typed[a.ID] = t
		if r.Submitted && strings.TrimSpace(t) != "" {
			x.run(context.Background(), "ask.answer", registry.Args{"id": string(se.ID), "ask": a.ID, "answers": x.answers(st, a)})
		}
	}
}

func containsStr(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func removeStr(xs []string, s string) []string {
	var out []string
	for _, x := range xs {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// agentTerm is an agent's raw terminal, as a terminal view's source: what it shows streams in, and what you type
// goes back.
type agentTerm struct {
	c      *worker.Worker
	id     core.SessionID
	s      <-chan []byte
	cancel context.CancelFunc

	mu   sync.Mutex
	rest []byte
}

func newAgentTerm(c *worker.Worker, id core.SessionID) (*agentTerm, error) {
	ctx, cancel := context.WithCancel(context.Background())
	s, err := c.Terminal(ctx, id)
	if err != nil {
		cancel()
		return nil, err
	}
	return &agentTerm{c: c, id: id, s: s, cancel: cancel}, nil
}

func (t *agentTerm) Read(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for len(t.rest) == 0 {
		b, ok := <-t.s
		if !ok {
			return 0, io.EOF
		}
		t.rest = b
	}
	n := copy(p, t.rest)
	t.rest = t.rest[n:]
	return n, nil
}

func (t *agentTerm) Write(p []byte) (int, error) {
	err := t.c.TermInput(worker.TermReq{Session: t.id, Data: append([]byte(nil), p...)})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *agentTerm) Resize(cols, rows int) error {
	return t.c.TermInput(worker.TermReq{Session: t.id, Cols: cols, Rows: rows})
}

func (t *agentTerm) Close() error {
	t.cancel()
	return nil
}
