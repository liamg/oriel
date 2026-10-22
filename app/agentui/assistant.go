package agentui

import (
	"cmp"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/assistant"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The assistant, Ori: a floating window you ask "what needs me?", "review
// this PR" or "where's that test I was working on?", minimised to its face on the rail with a count of what's unread.
// Its agent is a utility agent (Claude in its PTY with the mod, never claude -p),
// offered only the agent tools for "assistant": the built-ins here, and any another feature or a plugin adds (the
// tickets' my_tickets, mission control's). An act waits for your yes on a confirmation card (registry.AgentOwner), and what its tools did
// can be undone for a few seconds.
const (
	AssistantView     = kit.OverlayPrefix + "assistant"
	AssistantKeyMode  = "assistant"
	assistantHelp     = "agent/assistant"
	assistantPromptID = "assistant.system"
)

//go:embed assistant.md
var assistantPrompt string

// assistantWeb are the provider tools the assistant may use without asking, with assistant.web on.
var assistantWeb = []string{"WebSearch", "WebFetch"}

// Assistant is Ori, the window's own agent: its window, its tools and the commands that drive it.
type Assistant struct {
	kit.Disposer
	ConfigDir, StateDir string
	h                   plugin.Host
	conv                *assistant.Conversation

	// On the loop.
	open       bool
	draft      string
	focus      bool            // the composer takes focus next frame
	focused    map[string]bool // the confirmations that have asked for focus once
	scroll     kit.Scroll
	sideScroll kit.Scroll
	spoken     int // the last reply said aloud
	seen       int // the last entry shown in the window: what's after it is unread
	win        widget.WindowState
	placed     render.Rect // where the window was last saved

	mu  sync.Mutex
	ran string // what the conversation runs on: "claude · haiku"
}

func (x *Assistant) Manifest() plugin.Manifest { return plugin.Manifest{ID: "assistant"} }

func (x *Assistant) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.focused = map[string]bool{}
	if d, err := prompts.Prepare(prompts.Def{ID: assistantPromptID, Group: "Assistant", Label: "The assistant",
		Help:    "What the assistant is told as each conversation starts. A conversation already going keeps the text it started with.",
		Vars:    []prompts.Var{{Name: "date", Desc: "today, with the weekday, the time and the time zone"}},
		Default: strings.TrimSpace(assistantPrompt)}, kit.Prompts(h)...); err == nil {
		x.Add(h.Contribute(d))
	}
	for _, o := range []registry.OptionSpec{
		{Name: "assistant.provider", Type: registry.String, Default: "",
			Help: "The agent the assistant runs on: claude or codex. Empty picks the first installed and signed in."},
		{Name: "assistant.model", Type: registry.String, Default: "",
			Help: "The assistant's model. Empty is the provider's small model (Claude's Haiku), else its default."},
		{Name: "assistant.web", Type: registry.Bool, Default: true,
			Help: "Let the assistant search the web, for news and anything happening in the world."},
		{Name: "assistant.idle", Type: registry.Int, Default: 15, Min: 1, Max: 24 * 60,
			Help: "Minutes of quiet after which a conversation ends; the next starts afresh."},
	} {
		o.Scopes = registry.ScopeUser
		x.Add(h.Option(o))
	}
	x.conv = assistant.New(x.startAgent)
	x.conv.Progress = assistantProgress
	x.conv.Changed = func() { h.Do(x.changed) }
	x.setIdle()
	x.Add(kit.OnDispose(h.Options().Watch("assistant.idle", func() { h.Do(x.setIdle) })))
	x.Add(kit.OnDispose(x.conv.Close))
	x.Add(h.Contribute(registry.AgentOwner{Agent: kit.AssistantAgent, Confirm: x.confirm, Acted: x.acted, Source: "builtin:assistant"}))
	x.Add(h.Contribute(uireg.InboxSource{Name: kit.AssistantAgent, Cards: x.inboxCards, Source: "builtin:assistant"}))
	for _, t := range x.tools() {
		t.For, t.Source = []string{kit.AssistantAgent}, "builtin:assistant"
		x.Add(h.Contribute(t))
	}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-/>", Command: "assistant.toggle", Desc: "Ori"}))
	x.Add(h.Keymap(registry.Keymap{Mode: AssistantKeyMode, Keys: "<Esc>", Command: "assistant.close", Desc: "Minimise"}))
	x.Add(h.Keymap(registry.Keymap{Mode: AssistantKeyMode, Keys: "<D-.>", Command: "assistant.stop", Desc: "Stop its turn"}))
	for n := 1; n <= 9; n++ {
		x.Add(h.Keymap(registry.Keymap{Mode: AssistantKeyMode, Keys: fmt.Sprintf("<D-%d>", n), Command: "assistant.pick",
			Args: registry.Args{"n": n}, Desc: fmt.Sprintf("Pick row %d", n)}))
	}
	x.Add(h.Contribute(uireg.RailEntry{Name: "assistant", Title: "Ori", Command: "assistant.toggle", Scope: uireg.ScopeGlobalEntry,
		Order: 85, Bottom: true, Badge: x.unread, Active: func(string) bool { return x.open }, Source: "builtin:assistant",
		Draw: func(c *ui.Ctx, key any, size float32, _ render.Color) {
			ori(c, key, size, oriMood{busy: x.conv.Busy(), unread: x.unread() > 0})
		}}))
	x.win.Rect = x.loadPlace()
	x.placed = x.win.Rect
	x.Add(h.View(AssistantView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "assistant", Fn: x.window} })))
	return nil
}

func (x *Assistant) commands() []registry.Command {
	isOpen := func(registry.Args) bool { return x.open }
	return []registry.Command{
		{ID: "assistant.toggle", Title: "Open Ori, the assistant, or minimise it", Run: func(registry.Ctx, registry.Args) error {
			x.setOpen(!x.open)
			return nil
		}},
		{ID: "assistant.open", Title: "Open Ori, the assistant; with {text}, ask it that", Run: func(_ registry.Ctx, a registry.Args) error {
			x.setOpen(true)
			x.conv.Say(a.String("text"), false)
			return nil
		}},
		{ID: "assistant.close", Title: "Minimise Ori, the assistant; the conversation waits", Available: isOpen,
			Run: func(registry.Ctx, registry.Args) error { x.setOpen(false); return nil }},
		{ID: "assistant.say", Title: "Ask the assistant something: {text}; {voice} when it was spoken, so its answer is",
			Run: func(_ registry.Ctx, a registry.Args) error {
				if strings.TrimSpace(a.String("text")) == "" {
					return errors.New("nothing to say")
				}
				x.conv.Say(a.String("text"), a.Bool("voice"))
				return nil
			}},
		{ID: "assistant.send", Title: "Send what's in the assistant's composer", Available: isOpen, Run: func(registry.Ctx, registry.Args) error {
			x.send()
			return nil
		}},
		{ID: "assistant.new", Title: "Start a new conversation with the assistant", Run: func(registry.Ctx, registry.Args) error {
			x.conv.Clear()
			return nil
		}},
		{ID: "assistant.stop", Title: "Stop the assistant's turn", Available: func(registry.Args) bool { return x.conv.Busy() },
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.conv.Interrupt(ctx) }},
		{ID: "assistant.answer", Title: "Answer the assistant's confirmation: {yes} or not; {id}, else the oldest waiting",
			Available: func(registry.Args) bool { return len(x.conv.Waiting()) > 0 },
			Run:       func(_ registry.Ctx, a registry.Args) error { return x.answer(a.String("id"), a.Bool("yes")) }},
		{ID: "assistant.pick", Title: "Pick row {n} of the assistant's last answer: open its session or link, or ask about it",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.pick(a.Int("n")) }},
		{ID: "assistant.undo", Title: "Undo what the assistant did: {id}, else the latest it can undo",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.undo(a.String("id")) }},
		{ID: "assistant.show", Title: "Open what the assistant's action made: {id}, else the latest",
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.show(ctx, a.String("id")) }},
	}
}

func (x *Assistant) opt(name string) any {
	v, _, _ := x.h.Options().Get(name, registry.At{})
	return v
}

func (x *Assistant) str(name string) string { s, _ := x.opt(name).(string); return s }
func (x *Assistant) bool(name string) bool  { b, _ := x.opt(name).(bool); return b }

func (x *Assistant) setOpen(open bool) {
	x.open = open
	if open {
		if log, _ := x.conv.Log(); len(log) > 0 {
			x.seen = max(x.seen, log[len(log)-1].Seq)
		}
		x.focus = true
		x.scroll.Follow = true
	}
}

// unread is how many of its answers, questions and failures came since the window last showed them.
func (x *Assistant) unread() int {
	log, _ := x.conv.Log()
	n := 0
	for _, e := range log {
		if e.Seq > x.seen && (e.Kind == assistant.KindReply || e.Kind == assistant.KindConfirm || e.Kind == assistant.KindError) {
			n++
		}
	}
	return n
}

// send says what's in the composer.
func (x *Assistant) send() {
	if strings.TrimSpace(x.draft) == "" {
		return
	}
	x.conv.Say(x.draft, false)
	x.draft = ""
	x.scroll.Follow = true
}

// setIdle has the conversation's quiet end it after assistant.idle, re-arming one waiting now.
func (x *Assistant) setIdle() {
	x.conv.SetIdle(time.Duration(max(1, x.optInt("assistant.idle"))) * time.Minute)
}

// changed is the log changing: it's redrawn, and a spoken turn's answer is said.
func (x *Assistant) changed() {
	log, _ := x.conv.Log()
	for _, e := range log {
		if e.Seq <= x.spoken || e.Kind != assistant.KindReply {
			continue
		}
		x.spoken = e.Seq
		if e.Voice && e.Text != "" && x.h.Available("voice.speak", nil) {
			_ = x.h.Run(context.Background(), "voice.speak", registry.Args{"text": e.Text})
		}
	}
	x.h.Redraw()
}

func (x *Assistant) optInt(name string) int { n, _ := x.opt(name).(int); return n }

// prompt is what the assistant is told as a conversation starts: the user's version of it, filled in.
func (x *Assistant) Prompt() string {
	registered := kit.Prompts(x.h)
	o := prompts.Store{Dir: x.ConfigDir, Registered: registered}.Load()
	return o.Render(assistantPromptID, map[string]string{
		"date": time.Now().Format("Monday 2 January 2006, 15:04 MST"),
	}, registered...)
}

// startAgent starts the conversation's agent: a utility agent.
func (x *Assistant) startAgent(ctx context.Context, first string) (assistant.Agent, error) {
	c, err := kit.SessionHost(x.h)
	if err != nil {
		return nil, err
	}
	spec := host.UtilitySpec{Name: kit.AssistantAgent, Provider: x.str("assistant.provider"), Model: x.str("assistant.model"),
		Pack: x.Prompt(), Prompt: first}
	if x.bool("assistant.web") {
		spec.Allow = assistantWeb
	}
	u, err := c.StartUtility(context.Background(), spec)
	if err != nil {
		return nil, err
	}
	ready := u.Ready()
	x.mu.Lock()
	x.ran = strings.Trim(ready.Provider+" · "+ready.Model, " ·")
	x.mu.Unlock()
	a := &utilityAgent{u: u, sig: make(chan assistant.Signal, 64)}
	go a.pump()
	return a, nil
}

// runsOn is what the conversation runs on, as the drawer's chip says: the model, else the option's.
func (x *Assistant) runsOn() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	if _, model, ok := strings.Cut(x.ran, " · "); ok && model != "" {
		return model
	}
	return cmp.Or(x.str("assistant.model"), cmp.Or(x.ran, "small model"))
}

// utilityAgent is a utility agent as the conversation drives it.
type utilityAgent struct {
	u   *host.Utility
	sig chan assistant.Signal
}

func (a *utilityAgent) Send(ctx context.Context, text string) error { return a.u.Send(ctx, text) }
func (a *utilityAgent) Interrupt(ctx context.Context) error         { return a.u.Interrupt(ctx) }
func (a *utilityAgent) Signals() <-chan assistant.Signal            { return a.sig }
func (a *utilityAgent) Close() error                                { return a.u.Close() }

func (a *utilityAgent) pump() {
	defer close(a.sig)
	for ev := range a.u.Events() {
		var s assistant.Signal
		switch ev.Type {
		case "turn-start":
			s = assistant.Signal{Kind: assistant.TurnStarted}
		case "message":
			s = assistant.Signal{Kind: assistant.Said, Text: ev.Text}
		case "tool-call":
			s = assistant.Signal{Kind: assistant.UsedTool, Tool: ev.Tool}
		case "turn-end":
			s = assistant.Signal{Kind: assistant.TurnEnded}
		case "failure":
			s = assistant.Signal{Kind: assistant.Failed, Text: ev.Text}
		case "exited":
			s = assistant.Signal{Kind: assistant.Exited, Text: ev.Text}
		default:
			continue
		}
		a.sig <- s
	}
}

// confirm is the assistant's owner asking you about an act: its card is in the drawer, and in the inbox, stacked
// over the window while the drawer is away; the act waits for your answer.
func (x *Assistant) confirm(ctx registry.Ctx, tool string, c registry.Confirmation) (bool, error) {
	x.h.Redraw()
	switch x.conv.Confirm(ctx, tool, c.Line, c) {
	case assistant.Yes:
		return true, nil
	case assistant.No:
		return false, errors.New("the user said no, so nothing was done")
	case assistant.NoAnswer:
		return false, fmt.Errorf("the user didn't answer within %s, so nothing was done", x.conv.ConfirmWait.Round(time.Second))
	}
	return false, registry.ErrNotConfirmed
}

// acted is what a tool did, kept in the log with its undo.
func (x *Assistant) acted(_ registry.Ctx, tool string, a registry.Acted) {
	act := assistant.Act{Tool: tool, Line: a.Line, Detail: a.Detail, Open: a.Open, OpenArgs: a.OpenArgs}
	if a.Undo != nil {
		act.Undo = func(ctx context.Context) error { return a.Undo(ctx) }
	}
	x.conv.Acted(act)
}

func (x *Assistant) answer(id string, yes bool) error {
	if id == "" {
		w := x.conv.Waiting()
		if len(w) == 0 {
			return errors.New("nothing is waiting for an answer")
		}
		id = w[0].ID
	}
	if !x.conv.Answered(id, yes) {
		return errors.New("that's no longer waiting for an answer")
	}
	x.focus = true
	return nil
}

// lastItems are the rows of the latest reply that offers some.
func (x *Assistant) lastItems() []assistant.Item {
	log, _ := x.conv.Log()
	for _, e := range slices.Backward(log) {
		if e.Kind == assistant.KindReply && len(e.Items) > 0 {
			return e.Items
		}
	}
	return nil
}

// pick does what row n of the latest answer offers: opens its session or link, else asks about it.
func (x *Assistant) pick(n int) error {
	items := x.lastItems()
	if n < 1 || n > len(items) {
		return fmt.Errorf("there's no row %d", n)
	}
	it := items[n-1]
	switch {
	case it.Session != "":
		return x.h.Run(context.Background(), "session.open", registry.Args{"id": it.Session})
	case it.URL != "":
		return kit.OpenURL(x.h, it.URL)
	case it.File != "":
		args := map[string]any{"path": it.File, "line": float64(it.Line), "project": it.Project}
		go func() {
			if _, err := x.toolFocusFile(context.Background(), args); err != nil {
				x.h.Do(func() { x.report(err) })
			}
		}()
		return nil
	}
	x.conv.Say("Tell me more about "+it.Title, false)
	return nil
}

// action is the action {id}, else the latest that offers what's asked.
func (x *Assistant) action(id string, ok func(assistant.Entry) bool) (assistant.Entry, error) {
	log, _ := x.conv.Log()
	for _, e := range slices.Backward(log) {
		if e.Kind == assistant.KindAction && (id == e.ID || id == "" && ok(e)) {
			return e, nil
		}
	}
	return assistant.Entry{}, errors.New("no such action")
}

// undo takes an action back, off the loop: what undoes it may wait on the loop.
func (x *Assistant) undo(id string) error {
	e, err := x.action(id, func(e assistant.Entry) bool { return !e.Until.IsZero() && !e.Undone && time.Now().Before(e.Until) })
	if err != nil {
		return errors.New("nothing to undo")
	}
	go func() {
		if err := x.conv.Undo(context.Background(), e.ID); err != nil {
			x.h.Notify(core.Notice{ID: "assistant.undo", Severity: core.SevError, Key: true, Text: "Couldn't undo it: " + err.Error()})
		}
	}()
	return nil
}

func (x *Assistant) show(ctx context.Context, id string) error {
	e, err := x.action(id, func(e assistant.Entry) bool { return e.Open != "" && !e.Undone })
	if err != nil || e.Open == "" {
		return errors.New("nothing to open")
	}
	return x.h.Run(ctx, e.Open, registry.Args(e.OpenArgs))
}

// assistantProgress is the line the drawer shows while a tool runs.
func assistantProgress(tool string) string {
	switch tool {
	case "sessions":
		return "checking your sessions…"
	case "past_sessions":
		return "looking through finished sessions…"
	case "my_tickets":
		return "checking your tickets…"
	case "my_prs":
		return "checking your PRs…"
	case "review_requests":
		return "checking your reviews…"
	case "jobs":
		return "checking your jobs…"
	case "usage":
		return "checking your usage…"
	case "mission_control":
		return "checking mission control…"
	case "changed_files":
		return "looking at what you changed…"
	case "focus_file":
		return "bringing it up…"
	case "open_project":
		return "switching project…"
	case "help_search", "help_read":
		return "looking it up in the help…"
	case "WebSearch", "WebFetch":
		return "looking it up…"
	}
	return ""
}

// inboxCards are the assistant's in the inbox: its confirmations waiting, stacked over the window while
// the drawer is away, and what it did, as history.
func (x *Assistant) inboxCards() []uireg.InboxCard {
	log, _ := x.conv.Log()
	waiting := map[string]bool{}
	for _, e := range x.conv.Waiting() {
		waiting[e.ID] = true
	}
	var out []uireg.InboxCard
	for _, e := range log {
		switch {
		case e.Kind == assistant.KindConfirm && waiting[e.ID]:
			cf, _ := e.Card.(registry.Confirmation)
			id := e.ID
			out = append(out, uireg.InboxCard{ID: id, Type: "assistant-confirm", Group: uireg.GroupAgents, Mine: true, Icon: "◇",
				Tone: "assistant.asks", Title: cmp.Or(cf.Title, "Confirm"), Sub: "assistant · " + e.Text, Created: e.At, Expires: e.Until,
				Interrupt: !x.open, Kicker: "Assistant · " + e.Tool, Detail: cf.Lead, Note: cf.Note,
				Body: func(c *ui.Ctx) { confirmBody(c, x, cf) },
				Actions: []uireg.InboxAction{{Label: "Yes", Key: "y", Primary: true, Run: func() { x.report(x.answer(id, true)) }},
					{Label: "No", Key: "n", Run: func() { x.report(x.answer(id, false)) }}}})
		case e.Kind == assistant.KindAction:
			out = append(out, uireg.InboxCard{ID: e.ID, Type: "assistant-action", Group: uireg.GroupAgents, Mine: true, Icon: "◇",
				Tone: "assistant.done", Title: e.Text, Sub: "assistant · " + e.Detail, Created: e.At, Done: true})
		}
	}
	return out
}
