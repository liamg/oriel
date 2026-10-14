package app

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// maxInterrupts is how many asks' cards stack at once over the window; the rest wait behind a count.
const maxInterrupts = 4

// interrupts are the asks waiting on you, from every session, as cards stacked at the window's bottom right (ui.md
// 12). The session in front's are in its console instead while that shows.
func (x *agentConsole) interrupts(c *ui.Ctx) {
	st := x.h.State()
	inConsole := x.shown()
	type waiting struct {
		se  core.SessionState
		ask core.Ask
	}
	var all []waiting
	for _, s := range st.Sessions {
		if s.ID == st.Focus && inConsole {
			continue
		}
		for _, a := range s.Asks {
			all = append(all, waiting{s, a})
		}
	}
	if len(all) == 0 {
		return
	}
	th := c.Theme()
	corner := render.Point{X: c.Size().X - 16, Y: c.Size().Y - 36}
	c.Column("asks", ui.Style{W: ui.Px(440), Gap: 8, Z: ui.LayerToast, Float: &ui.Float{Point: &corner, Self: render.Point{X: 1, Y: 1}},
		Role: ui.RoleGroup, Label: "Waiting on you"}, func(c *ui.Ctx) {
		for _, w := range all[:min(len(all), maxInterrupts)] {
			c.Box(string(w.se.ID)+"/"+w.ask.ID, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) { x.askCard(c, "card", w.se, w.ask, true, false) })
		}
		if n := len(all) - maxInterrupts; n > 0 {
			c.Row("more", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("+%d more waiting", n), monoFont(10, th.C(theme.FgFaint)))
			})
		}
	})
}

// askTone is an ask's colour, by kind.
func askTone(k core.AskKind) theme.TokenID {
	switch k {
	case core.AskNetwork:
		return tAskNetwork
	case core.AskGateway:
		return tAskGateway
	case core.AskQuestion:
		return tAskQuestion
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
// with its session's name (kicker).
func (x *agentConsole) askCard(c *ui.Ctx, key string, se core.SessionState, a core.Ask, kicker bool, selected bool) {
	th := c.Theme()
	st := x.state(se.ID)
	prov := providerName(se.Agent.Provider)
	tone := th.C(askTone(a.Kind))
	o := widget.ActionCardOpts{Tone: tone, Selected: selected, Help: "agent/asks#" + string(a.Kind)}
	if kicker {
		o.Kicker = cmpOr(se.Name, string(se.ID)) + " · " + string(a.Kind)
	}
	type act struct {
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
		add("Allow", "y", true, registry.Args{"decision": "allow"})
		if a.Rule != "" {
			add("Always", "a", false, registry.Args{"decision": "always"})
		}
		add("Deny", "n", false, registry.Args{"decision": "deny"})
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
	x.run(context.Background(), "ask.answer", args)
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

// agentTerm is an agent's raw terminal on its worker, as a terminal view's source: what it shows streams in, and
// what you type goes back.
type agentTerm struct {
	c      worker.Client
	id     core.SessionID
	s      wire.Stream
	cancel context.CancelFunc

	mu   sync.Mutex
	rest []byte
}

func newAgentTerm(c worker.Client, id core.SessionID) (*agentTerm, error) {
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
		var b []byte
		if err := t.s.Recv(&b); err != nil {
			return 0, err
		}
		t.rest = b
	}
	n := copy(p, t.rest)
	t.rest = t.rest[n:]
	return n, nil
}

func (t *agentTerm) Write(p []byte) (int, error) {
	err := t.c.TermInput(context.Background(), worker.TermReq{Session: t.id, Data: append([]byte(nil), p...)})
	if err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *agentTerm) Resize(cols, rows int) error {
	return t.c.TermInput(context.Background(), worker.TermReq{Session: t.id, Cols: cols, Rows: rows})
}

func (t *agentTerm) Close() error {
	t.cancel()
	return t.s.Close()
}
