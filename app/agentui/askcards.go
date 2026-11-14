package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// askCards are the asks waiting on you, from every session, as the inbox's cards: stacked over the
// window, except the session in front's while its console shows them and a question, drawn in its thread; a
// security-key touch on its own at the top.
// A permission is a prompt: it comes up in the middle of the window while its session is in front, and calls from
// its tab while it's behind.
func (x *Console) askCards() []uireg.InboxCard {
	st := x.h.State()
	inConsole := x.shown()
	var out []uireg.InboxCard
	for _, se := range st.Sessions {
		for _, a := range se.Asks {
			card := uireg.InboxCard{ID: string(se.ID) + "/" + a.ID, Type: string(a.Kind), Group: askGroup(a.Kind), Mine: true, Session: string(se.ID),
				Icon: string(a.Kind), Tone: askTone(a.Kind).Name(), Created: a.Created,
				Interrupt: (se.ID != st.Focus || !inConsole || a.Kind == core.AskKey) && a.Thread == "",
				Draw: func(c *ui.Ctx, key string, p uireg.CardPlace) {
					x.askCard(c, key, se, a, p, false)
				}}
			// What holds the agent until you allow or deny it (its own gate, the gateway's call, a connection the egress
			// proxy holds) comes up as a prompt; a key touch comes at the top of the window, where it's seen.
			card.Prompt = asksYou(a.Kind)
			card.Top = a.Kind == core.AskKey
			if a.Thread != "" { // asked in a thread, it's drawn there, and the inbox takes you to it
				card.Open = func() { x.openThread(se.ID, a.Thread) }
			}
			title, sub := x.askRow(se, a)
			card.Title, card.Sub = title, kit.JoinParts(sub, kit.Label(se))
			out = append(out, card)
		}
	}
	return out
}

// asksYou says asks of kind k come up as prompts in the middle of the window: those that hold the agent until you
// allow or deny them.
func asksYou(k core.AskKind) bool {
	return k == core.AskPermission || k == core.AskGateway || k == core.AskNetwork
}

// askGroup is the Inbox filter an ask is under.
func askGroup(k core.AskKind) string {
	switch k {
	case core.AskNetwork, core.AskGateway:
		return uireg.GroupSandbox
	case core.AskKey:
		return ""
	}
	return uireg.GroupAgents
}

// askRow is an ask as a row of the Inbox panel: its title, and what it's about.
func (x *Console) askRow(se core.SessionState, a core.Ask) (title, sub string) {
	prov := services.ProviderTitle(x.h, se.Agent.Provider)
	switch a.Kind {
	case core.AskPermission:
		return "Permission: " + a.Tool + "(" + cmp.Or(a.Summary, kit.MainInput(a.Input)) + ")", strings.ToLower(prov)
	case core.AskNetwork:
		return "Network: " + a.Host, kit.JoinParts("sandbox", a.Detail)
	case core.AskGateway:
		return cmp.Or(a.Summary, a.Tool), kit.JoinParts("gateway", a.Detail)
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
	case core.AskTerminal:
		return prov + " waits on its terminal", a.Summary
	}
	return cmp.Or(a.Summary, string(a.Kind)), string(a.Kind)
}

// askTone is an ask's colour, by kind.
func askTone(k core.AskKind) theme.TokenID {
	switch k {
	case core.AskNetwork:
		return tAskNetwork
	case core.AskGateway:
		return tAskGateway
	case core.AskQuestion, core.AskLogin, core.AskTerminal:
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

// askCard is an ask as a card, with what answers it: in the console where it arose, or stacked over the window
// with its session's name (kicker). With dismiss, its ✕ and Escape take it off the stack.
func (x *Console) askCard(c *ui.Ctx, key string, se core.SessionState, a core.Ask, p uireg.CardPlace, selected bool) {
	if p.Prompt && asksYou(a.Kind) {
		x.promptCard(c, key, se, a, p)
		return
	}
	th := c.Theme()
	st := x.state(se.ID)
	kicker, dismiss := p.Stacked, p.Dismiss
	k := &askCardParts{o: widget.ActionCardOpts{Tone: th.C(askTone(a.Kind)), Selected: selected, Help: "agent/asks#" + string(a.Kind), Dismiss: dismiss != nil,
		TakeFocus: p.TakeFocus}}
	if kicker {
		k.o.Kicker = kit.Label(se) + " · " + string(a.Kind)
	}
	prov := services.ProviderTitle(x.h, se.Agent.Provider)
	switch a.Kind {
	case core.AskPermission, core.AskNetwork, core.AskGateway:
		x.decisionCard(th, k, se, a, st, kicker)
	case core.AskLogin:
		k.login(x.h, se, a, kicker)
	case core.AskKey:
		x.keyCard(k, se, a)
	case core.AskQuestion:
		x.questionCard(k, se, a, st, prov)
	case core.AskTerminal:
		k.terminal(a, prov, kicker)
	}
	if x.detail == nil || x.detail.ask != a.ID {
		k.action(widget.CardAction{Label: "Details"}, askAct{cmd: "agent.console.details", args: registry.Args{"item": ""}, away: true})
	}
	// A card away from its session's console can take you there, for the context to decide on.
	if kicker && (a.Kind == core.AskPermission || a.Kind == core.AskNetwork || a.Kind == core.AskGateway || a.Kind == core.AskQuestion) {
		k.action(widget.CardAction{Label: "Console", Key: "g"}, askAct{cmd: "agent.console.show", away: true})
	}
	body := k.body
	if st.noting == a.ID && st.notingOn == p.Stacked {
		inner := body
		body = func(c *ui.Ctx) {
			if inner != nil {
				inner(c)
			}
			x.noteInput(c, se, a, st)
		}
	}
	res := widget.ActionCard(c, key, k.o, body)
	if res.Dismissed && dismiss != nil {
		dismiss()
	}
	if res.Action < 0 {
		return
	}
	if k.acts[res.Action].note {
		x.startNote(se.ID, a.ID, p.Stacked)
		return
	}
	// A permission over the window steps aside for the console it takes you to, where it waits to be answered.
	if k.acts[res.Action].away && a.Kind == core.AskPermission && dismiss != nil {
		dismiss()
	}
	args := registry.Args{"id": string(se.ID), "ask": a.ID}
	for n, v := range k.acts[res.Action].args {
		args[n] = v
	}
	x.run(context.Background(), cmp.Or(k.acts[res.Action].cmd, "ask.answer"), args)
}

// askAct is what an ask card's action does.
type askAct struct {
	cmd  string // ask.answer if ""
	args registry.Args
	note bool
	away bool // it takes you to the console
}

// askCardParts is an ask card being made: its options, its actions' acts in order, and its body.
type askCardParts struct {
	o    widget.ActionCardOpts
	acts []askAct
	body func(c *ui.Ctx)
}

func (k *askCardParts) action(ca widget.CardAction, act askAct) {
	k.o.Actions = append(k.o.Actions, ca)
	k.acts = append(k.acts, act)
}

// add is an action that answers the ask with args.
func (k *askCardParts) add(label, key string, primary bool, args registry.Args) {
	k.action(widget.CardAction{Label: label, Key: key, Primary: primary}, askAct{args: args})
}

// decisionCard is an ask that holds the agent as a card in the console or the Inbox panel: its command, the rule an
// always makes, its answers as buttons, and a network ask's scope.
func (x *Console) decisionCard(th *theme.Resolved, k *askCardParts, se core.SessionState, a core.Ask, st *consoleState, kicker bool) {
	d := x.decide(th, se, a)
	o := &k.o
	o.Title, o.Code = d.title, d.code
	switch a.Kind {
	case core.AskPermission:
		if d.code == "" {
			o.Code = cmp.Or(a.Summary, kit.MainInput(a.Input))
		}
		o.Meta = "on this machine"
		if se.Agent.Sandboxed {
			o.Meta = "sandbox · network " + cmp.Or(se.Agent.Network, "ask")
		}
	case core.AskGateway:
		o.Detail = a.Detail
		if !kicker {
			o.Kicker = "gateway"
		}
	case core.AskNetwork:
		o.Detail = d.where
	}
	if a.Rule != "" && a.Kind != core.AskNetwork {
		o.Note = "rule: " + a.Rule
	}
	scope := st.scope[a.ID]
	if len(d.scopes) > 1 {
		k.body = func(c *ui.Ctx) {
			widget.Segmented(c, "scope", d.scopes, &scope, widget.SegmentedOpts{Label: "Allow", Help: "agent/asks#network"})
			st.scope[a.ID] = scope
		}
	}
	for i, opt := range d.options {
		act := opt.act
		if opt.scoped && len(d.scopeArgs) > 0 {
			act.args = registry.Args{"scope": d.scopeArgs[min(scope, len(d.scopeArgs)-1)]}
			for n, v := range opt.act.args {
				act.args[n] = v
			}
		}
		k.action(widget.CardAction{Label: opt.Label, Key: opt.Key, Primary: i == 0}, act)
	}
}
func (k *askCardParts) login(r registry.Contributions, se core.SessionState, a core.Ask, kicker bool) {
	l := core.Login{Provider: se.Agent.Provider}
	if a.Login != nil {
		l = *a.Login
	}
	k.o.Title = services.ProviderTitle(r, l.Provider) + " needs you to log in"
	k.o.Detail = l.Detail
	if !kicker {
		k.o.Kicker = "login"
	}
	switch {
	case l.Browser:
		k.o.Note = "opens its sign-in in your browser"
		k.action(widget.CardAction{Label: "Log in", Key: "l", Primary: true}, askAct{cmd: "agent.login"})
	case l.Terminal != "":
		k.o.Note = "opens its terminal with " + l.Terminal
		k.action(widget.CardAction{Label: "Log in", Key: "l", Primary: true}, askAct{cmd: "agent.login"})
	case len(l.HostCommand) > 0:
		k.o.Note = "signs in on this machine: " + strings.Join(l.HostCommand, " ")
		k.action(widget.CardAction{Label: "Log in", Key: "l", Primary: true}, askAct{cmd: "agent.login"})
	case l.Shell != "":
		k.o.Note = "run " + l.Shell + " in a terminal"
	}
	k.add("Check again", "c", !canLogIn(l), registry.Args{"decision": "recheck"})
}

func (k *askCardParts) terminal(a core.Ask, prov string, kicker bool) {
	k.o.Title = prov + " waits on its terminal"
	k.o.Detail = a.Summary
	k.o.Note = "a dialog only its terminal answers"
	if !kicker {
		k.o.Kicker = "terminal"
	}
	k.action(widget.CardAction{Label: "Go to terminal", Key: "t", Primary: true}, askAct{cmd: "agent.terminal"})
}

func (x *Console) keyCard(k *askCardParts, se core.SessionState, a core.Ask) {
	k.o.Kicker = ""
	k.o.Title = "Touch your security key"
	k.o.Detail = kit.JoinParts(a.Summary, a.Detail, kit.Label(se))
	k.o.Icon = func(c *ui.Ctx, tone render.Color) { kit.Icon(c, x.h, "icon", "key", 15, tone) }
	k.add("Cancel", "c", false, registry.Args{"decision": "deny"})
}

func (x *Console) questionCard(k *askCardParts, se core.SessionState, a core.Ask, st *consoleState, prov string) {
	k.o.Title = prov + " asks"
	k.body = func(c *ui.Ctx) { x.questions(c, se, a, st) }
	if len(a.Questions) > 1 || len(a.Questions) == 1 && a.Questions[0].Multi {
		k.action(widget.CardAction{Label: "Answer", Primary: true}, askAct{args: registry.Args{"answers": x.answers(st, a)}})
	}
}

// canLogIn is a login the app signs in again itself: in a browser it opens, the agent's own terminal, or a terminal
// on the host running its HostCommand.
func canLogIn(l core.Login) bool { return l.Browser || l.Terminal != "" || len(l.HostCommand) > 0 }

// loginWaiting is the session waiting on a login, and its ask: session {id}, else the one in front, else any
// waiting on {provider}'s login.
func (x *Console) loginWaiting(a registry.Args) (core.SessionState, core.Login, bool) {
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

// logIn signs an agent in again: its provider's sign-in, opened in the browser, or its own terminal, opened with
// the provider's login typed; for an agent with neither, it says what to run.
func (x *Console) logIn(ctx registry.Ctx, a registry.Args) error {
	se, l, ok := x.loginWaiting(a)
	if !ok {
		return errors.New("no agent is waiting on a login")
	}
	switch {
	case l.Browser:
		c, err := x.client(se.ID)
		if err != nil {
			return err
		}
		plugin.Go(x.h, "an agent's sign-in", func() {
			url, err := c.StartLogin(context.Background(), l.Provider)
			x.h.Do(func() {
				if err == nil {
					err = kit.OpenURL(x.h, url)
				}
				if err != nil {
					x.h.Notify(core.Notice{ID: "agent.login", Severity: core.SevError, Source: "agent", Text: err.Error(), Time: time.Now()})
				}
			})
		})
		return nil
	case !canLogIn(l):
		return fmt.Errorf("run %s in a terminal", cmp.Or(l.Shell, "the agent's login"))
	case l.Terminal == "":
		return x.h.Run(ctx, "term.host", registry.Args{"cmd": slices.Clone(l.HostCommand)})
	}
	return x.rawTerminal(ctx, se.ID, l.Terminal+"\r")
}

// noteInput is a denial's note, which goes back to the agent with it.
func (x *Console) noteInput(c *ui.Ctx, se core.SessionState, a core.Ask, st *consoleState) {
	c.Row("note", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		id := c.Next("in")
		if st.focusNow == "note" {
			c.RequestFocus(id)
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
func (x *Console) answers(st *consoleState, a core.Ask) map[string]string {
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
func (x *Console) questions(c *ui.Ctx, se core.SessionState, a core.Ask, st *consoleState) {
	th := c.Theme()
	if st.chosen[a.ID] == nil {
		st.chosen[a.ID] = map[int][]string{}
	}
	picked := st.chosen[a.ID]
	help := "agent/asks#question"
	for i, q := range a.Questions {
		c.Column(fmt.Sprint("q", i), ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			if q.Header != "" {
				c.Text("h", strings.ToUpper(q.Header), kit.KickerFont(th.C(theme.FgFaint)))
			}
			widget.Wrap(c, "q", q.Question, 380, kit.UIFont(12.5, th.C(theme.Fg)))
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
					c.Text("g", glyph, kit.MonoFont(11, th.C(theme.Accent)))
					c.Text("t", opt, kit.UIFont(12.5, th.C(theme.Fg)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("n", fmt.Sprint(j+1), kit.MonoFont(10, th.C(theme.FgGhost)))
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
		r := widget.Input(c, "typed", &t, widget.InputOpts{Label: "Your own answer", Placeholder: "or type an answer: ⏎ sends it", Help: help,
			TabMoves: true})
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
	c      host.SessionCalls
	id     core.SessionID
	s      <-chan []byte
	cancel context.CancelFunc

	mu   sync.Mutex
	rest []byte
}

func newAgentTerm(c host.SessionCalls, id core.SessionID) (*agentTerm, error) {
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
	err := t.c.TermInput(host.TermReq{Session: t.id, Data: append([]byte(nil), p...)})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *agentTerm) Resize(cols, rows int) error {
	return t.c.TermInput(host.TermReq{Session: t.id, Cols: cols, Rows: rows})
}

func (t *agentTerm) Close() error {
	t.cancel()
	return nil
}
