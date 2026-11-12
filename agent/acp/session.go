package acp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/guard"
	"github.com/liamg/oriel/agent/mcptools"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// stopTimeout is how long Close waits for the agent to exit once its stdin closes, before killing it.
const stopTimeout = 3 * time.Second

type session struct {
	p     *Provider
	spec  agent.StartSpec
	ex    agent.Exec
	proc  agent.Process
	tools *mcptools.Server
	errs  *tail
	rpc   *jsonrpc.Conn
	caps  agentCaps
	term  *terminals

	events   chan agent.Event
	exited   chan struct{} // closed once the process is gone and its last event said
	turns    sync.Mutex    // one prompt turn at a time
	closing  sync.Once
	finished chan struct{} // closed once Close has closed events

	mu        sync.Mutex
	id        string
	history   bool // it was resumed: the pack is in its history
	replaying bool // session/load is replaying the history Oriel already has
	closed    bool
	seq       int
	msg       *message                // the message being said
	calls     map[string]*toolCall    // by ACP tool call id
	asks      map[string]chan decided // permissions and questions waiting, by Oriel's id
	notes     []string                // denials' reasons, for the agent to read before the turn ends
	used      usageUpdate
	cancelled bool // the turn under way was interrupted
	prompting bool // its prompt is out
}

type message struct {
	id   string
	role core.ItemKind
	text string
}

type decided struct {
	d         agent.Decision
	cancelled bool
}

type toolCall struct {
	ID       string            `json:"toolCallId"`
	Name     string            `json:"name"`
	Title    string            `json:"title"`
	Kind     string            `json:"kind"`
	Status   string            `json:"status"`
	Content  []json.RawMessage `json:"content"`
	RawInput json.RawMessage   `json:"rawInput"`
	RawOut   json.RawMessage   `json:"rawOutput"`
	said     bool              // its ToolCall is out
	done     bool              // its ToolResult is out
}

type usageUpdate struct {
	Used int `json:"used"`
	Size int `json:"size"`
	Cost *struct {
		Amount float64 `json:"amount"`
	} `json:"cost"`
}

func newSession(p *Provider, spec agent.StartSpec, ex agent.Exec, proc agent.Process, tools *mcptools.Server, errs *tail) *session {
	s := &session{p: p, spec: spec, ex: ex, proc: proc, tools: tools, errs: errs, events: make(chan agent.Event, 256),
		exited: make(chan struct{}), finished: make(chan struct{}), calls: map[string]*toolCall{}, asks: map[string]chan decided{}}
	s.term = &terminals{s: s, open: map[string]*term{}}
	s.rpc = jsonrpc.NewConn(proc.Stdout(), proc.Stdin(), jsonrpc.Handlers{Request: s.onRequest, Notify: s.onNotify},
		jsonrpc.Options{Version: "2.0", InOrder: true})
	go s.wait()
	return s
}

// wait says the process gone, once everything it said is passed on.
func (s *session) wait() {
	err := s.proc.Wait()
	select {
	case <-s.rpc.Done():
	case <-time.After(2 * time.Second):
	}
	s.rpc.Close()
	ev := agent.Exited{}
	if err != nil {
		ev.Err = err.Error()
		if t := s.errs.String(); t != "" {
			ev.Err += ": " + t
		}
	}
	s.emit(ev)
	close(s.exited)
}

func (s *session) emit(ev agent.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.events <- ev
	}
}

func (s *session) next(kind string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seq++
	return fmt.Sprintf("%s-%d", kind, s.seq)
}

func (s *session) setReplaying(on bool) {
	s.mu.Lock()
	s.replaying = on
	s.mu.Unlock()
}

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Events() <-chan agent.Event { return s.events }

// Terminal: an ACP agent has none Oriel can show.
func (s *session) Terminal() agent.Terminal { return nil }

// Fork: ACP has no way to ask a side question.
func (s *session) Fork(context.Context, agent.ForkSpec) (string, error) { return "", agent.ErrNoFork }

func (s *session) Send(_ context.Context, in agent.Input) error {
	blocks, err := s.prompt(in)
	if err != nil {
		return err
	}
	go s.turn(blocks)
	return nil
}

// prompt is a turn's content: the pack first, on a new session's first turn; images as images when the agent
// takes them, else their paths.
func (s *session) prompt(in agent.Input) ([]any, error) {
	var blocks []any
	s.mu.Lock()
	first := !s.history && s.spec.Pack.Text != ""
	s.history = true
	s.mu.Unlock()
	if first {
		blocks = append(blocks, text(s.spec.Pack.Text))
	}
	body := in.Text
	for _, img := range in.Images {
		if !s.caps.PromptCapabilities.Image {
			body += "\n\n[image: " + img + "]"
			continue
		}
		b, err := os.ReadFile(img)
		if err != nil {
			return nil, err
		}
		mt := mime.TypeByExtension(filepath.Ext(img))
		if mt == "" {
			mt = "image/png"
		}
		blocks = append(blocks, map[string]any{"type": "image", "mimeType": mt, "data": base64.StdEncoding.EncodeToString(b),
			"uri": "file://" + img})
	}
	return append(blocks, text(body)), nil
}

func text(t string) map[string]any { return map[string]any{"type": "text", "text": t} }

// turn runs a prompt turn, and the turns that hand the agent a denial's reason, as one turn of Oriel's.
func (s *session) turn(blocks []any) {
	s.turns.Lock()
	defer s.turns.Unlock()
	s.mu.Lock()
	s.cancelled = false
	s.mu.Unlock()
	s.emit(agent.TurnStart{})
	began := time.Now()
	for {
		var r struct {
			StopReason string `json:"stopReason"`
		}
		s.mu.Lock()
		cancelled := s.cancelled
		s.prompting = !cancelled
		s.mu.Unlock()
		if cancelled {
			break
		}
		err := s.rpc.Call(context.Background(), "session/prompt", map[string]any{"sessionId": s.ID(), "prompt": blocks}, &r)
		s.mu.Lock()
		s.prompting = false
		s.mu.Unlock()
		s.flush()
		if err != nil {
			s.failure(err)
			break
		}
		s.mu.Lock()
		notes := s.notes
		s.notes = nil
		cancelled = s.cancelled || r.StopReason == "cancelled"
		s.mu.Unlock()
		if r.StopReason == "refusal" {
			s.emit(agent.Failure{Err: s.p.Name + " refused to go on"})
		}
		if len(notes) == 0 || cancelled {
			break
		}
		blocks = []any{text(strings.Join(notes, "\n"))}
	}
	s.mu.Lock()
	u := agent.Usage{Context: s.used.Used, ContextMax: s.used.Size, Duration: time.Since(began)}
	if s.used.Cost != nil {
		u.Cost = s.used.Cost.Amount
	}
	s.mu.Unlock()
	s.emit(agent.TurnEnd{Usage: u})
}

// failure says a prompt that failed, and a lost login with it.
func (s *session) failure(err error) {
	s.emit(agent.Failure{Err: err.Error()})
	var je *jsonrpc.Error
	if errors.As(err, &je) && je.Code == CodeAuthRequired {
		s.emit(s.p.signedOut(je.Message))
	}
}

// Interrupt cancels the turn: questions waiting are answered cancelled, as ACP asks. A turn whose prompt isn't out
// yet sends none.
func (s *session) Interrupt(context.Context) error {
	s.mu.Lock()
	s.cancelled = true
	prompting := s.prompting
	asks := s.asks
	s.asks = map[string]chan decided{}
	id := s.id
	s.mu.Unlock()
	for _, ch := range asks {
		ch <- decided{cancelled: true}
	}
	if !prompting {
		return nil
	}
	return s.rpc.Notify("session/cancel", map[string]any{"sessionId": id})
}

func (s *session) Answer(_ context.Context, id string, d agent.Decision) error {
	s.mu.Lock()
	ch := s.asks[id]
	delete(s.asks, id)
	s.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("%s asked nothing as %s", s.p.Name, id)
	}
	ch <- decided{d: d}
	return nil
}

// ask raises ev and waits for its answer, or for the turn to be cancelled.
func (s *session) ask(ctx context.Context, id string, ev agent.Event) decided {
	ch := make(chan decided, 1)
	s.mu.Lock()
	s.asks[id] = ch
	s.mu.Unlock()
	s.emit(ev)
	select {
	case d := <-ch:
		return d
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.asks, id)
		s.mu.Unlock()
		return decided{cancelled: true}
	}
}

func (s *session) Close() error {
	s.closing.Do(func() {
		s.term.releaseAll()
		if has(s.caps.SessionCapabilities.Close) && s.ID() != "" {
			ctx, cancel := context.WithTimeout(context.Background(), stopTimeout)
			s.rpc.Call(ctx, "session/close", map[string]any{"sessionId": s.ID()}, nil)
			cancel()
		}
		s.proc.Stdin().Close()
		select {
		case <-s.exited:
		case <-time.After(stopTimeout):
			s.proc.Kill()
			<-s.exited
		}
		s.tools.Close()
		s.mu.Lock()
		s.closed = true
		close(s.events)
		asks := s.asks
		s.asks = map[string]chan decided{}
		s.mu.Unlock()
		for _, ch := range asks {
			ch <- decided{cancelled: true}
		}
		close(s.finished)
	})
	<-s.finished
	return nil
}

// onNotify reads session/update.
func (s *session) onNotify(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n struct {
		Update json.RawMessage `json:"update"`
	}
	if json.Unmarshal(params, &n) != nil {
		return
	}
	s.mu.Lock()
	replaying := s.replaying
	s.mu.Unlock()
	if replaying {
		return
	}
	var u struct {
		Kind      string          `json:"sessionUpdate"`
		MessageID string          `json:"messageId"`
		Content   json.RawMessage `json:"content"`
		Entries   []struct {
			Content  string `json:"content"`
			Priority string `json:"priority"`
			Status   string `json:"status"`
		} `json:"entries"`
	}
	if json.Unmarshal(n.Update, &u) != nil {
		return
	}
	switch u.Kind {
	case "agent_message_chunk", "agent_thought_chunk":
		role := core.ItemAssistant
		if u.Kind == "agent_thought_chunk" {
			role = core.ItemThinking
		}
		var c struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(u.Content, &c) == nil && c.Type == "text" {
			s.chunk(role, u.MessageID, c.Text)
		}
	case "tool_call", "tool_call_update":
		var c toolCall
		if json.Unmarshal(n.Update, &c) == nil {
			s.flush()
			s.toolCall(c, u.Kind == "tool_call", n.Update)
		}
	case "plan":
		s.flush()
		id := s.next("plan")
		in, _ := json.Marshal(map[string]any{"entries": u.Entries})
		s.emit(agent.ToolCall{ID: id, Tool: "plan", Input: in})
		var lines []string
		for _, e := range u.Entries {
			lines = append(lines, "["+e.Status+"] "+e.Content)
		}
		s.emit(agent.ToolResult{ID: id, Output: strings.Join(lines, "\n")})
	case "usage_update":
		var uu usageUpdate
		if json.Unmarshal(n.Update, &uu) == nil {
			s.mu.Lock()
			s.used = uu
			s.mu.Unlock()
		}
	}
}

// chunk streams a message: the whole of it so far each time, a new one when its role or id changes.
func (s *session) chunk(role core.ItemKind, id, delta string) {
	s.mu.Lock()
	m := s.msg
	if m != nil && (m.role != role || (id != "" && m.id != id)) {
		s.mu.Unlock()
		s.flush()
		s.mu.Lock()
		m = nil
	}
	if m == nil {
		if id == "" {
			s.seq++
			id = fmt.Sprintf("m-%d", s.seq)
		}
		m = &message{id: id, role: role}
		s.msg = m
	}
	m.text += delta
	ev := agent.Message{ID: m.id, Role: role, Text: m.text, Partial: true}
	s.mu.Unlock()
	s.emit(ev)
}

// flush says the message being streamed, whole.
func (s *session) flush() {
	s.mu.Lock()
	m := s.msg
	s.msg = nil
	s.mu.Unlock()
	if m != nil {
		s.emit(agent.Message{ID: m.id, Role: m.role, Text: m.text})
	}
}

// toolCall merges a tool call's report into what's known of it, and says its call and its result once each.
func (s *session) toolCall(c toolCall, created bool, raw json.RawMessage) {
	var fields map[string]json.RawMessage
	json.Unmarshal(raw, &fields)
	s.mu.Lock()
	tc := s.calls[c.ID]
	if tc == nil || created {
		if tc == nil {
			tc = &toolCall{ID: c.ID}
			s.calls[c.ID] = tc
		}
	}
	if c.Name != "" {
		tc.Name = c.Name
	}
	if c.Title != "" {
		tc.Title = c.Title
	}
	if c.Kind != "" {
		tc.Kind = c.Kind
	}
	if c.Status != "" {
		tc.Status = c.Status
	}
	if _, ok := fields["content"]; ok {
		tc.Content = c.Content
	}
	if has(c.RawInput) {
		tc.RawInput = c.RawInput
	}
	if has(c.RawOut) {
		tc.RawOut = c.RawOut
	}
	var evs []agent.Event
	if !tc.said {
		tc.said = true
		evs = append(evs, agent.ToolCall{ID: tc.ID, Tool: tc.tool(), Input: tc.input()})
	}
	if !tc.done && (tc.Status == "completed" || tc.Status == "failed") {
		tc.done = true
		evs = append(evs, tc.result())
	}
	s.mu.Unlock()
	for _, ev := range evs {
		s.emit(ev)
	}
}

// tool is the call's tool as Oriel names it: its own name, else Oriel's for its kind, else its title.
func (c *toolCall) tool() string {
	if c.Name != "" {
		return c.Name
	}
	if t, ok := kindTools[c.Kind]; ok {
		return t
	}
	if c.Title != "" {
		return c.Title
	}
	return "tool"
}

// kindTools are Oriel's names for ACP's tool kinds.
var kindTools = map[string]string{"read": "Read", "edit": "Edit", "delete": "Edit", "move": "Edit", "search": "Grep",
	"execute": "Bash", "think": "Think", "fetch": "WebFetch"}

func (c *toolCall) input() json.RawMessage {
	if has(c.RawInput) {
		return c.RawInput
	}
	b, _ := json.Marshal(map[string]string{"title": c.Title})
	return b
}

// result is the call's outcome: its text, and its first diff.
func (c *toolCall) result() agent.ToolResult {
	r := agent.ToolResult{ID: c.ID, IsError: c.Status == "failed"}
	var out []string
	for _, raw := range c.Content {
		var b struct {
			Type    string  `json:"type"`
			Path    string  `json:"path"`
			OldText *string `json:"oldText"`
			NewText string  `json:"newText"`
			Content struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		if json.Unmarshal(raw, &b) != nil {
			continue
		}
		switch b.Type {
		case "content":
			if b.Content.Type == "text" {
				out = append(out, b.Content.Text)
			}
		case "diff":
			if r.Diff == nil {
				old := ""
				if b.OldText != nil {
					old = *b.OldText
				}
				r.Diff = fileDiff(b.Path, old, b.NewText)
			}
		}
	}
	r.Output = strings.Join(out, "\n")
	if r.Output == "" && has(c.RawOut) {
		var str string
		if json.Unmarshal(c.RawOut, &str) == nil {
			r.Output = str
		} else {
			r.Output = string(c.RawOut)
		}
	}
	return r
}

// onRequest answers the agent's requests of its client, each on a goroutine of its own.
func (s *session) onRequest(r *jsonrpc.Request) {
	go func() {
		res, err := s.answer(r.Context(), r.Method, r.Params)
		r.Reply(res, err)
	}()
}

func (s *session) answer(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/request_permission":
		return s.permission(ctx, params)
	case "elicitation/create":
		return s.elicit(ctx, params)
	case "fs/read_text_file":
		return s.readFile(params)
	case "fs/write_text_file":
		return s.writeFile(params)
	case "terminal/create", "terminal/output", "terminal/wait_for_exit", "terminal/kill", "terminal/release":
		if s.spec.Sandbox == nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "terminals aren't offered outside a sandbox"}
		}
		return s.term.answer(ctx, method, params)
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "method not found: " + method}
}

type permissionOption struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// permission is the agent's own gate: the session's policy answers what it allows, the user the rest.
func (s *session) permission(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		ToolCall toolCall           `json:"toolCall"`
		Options  []permissionOption `json:"options"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	s.mu.Lock()
	tc := p.ToolCall
	if known := s.calls[tc.ID]; known != nil {
		merged := *known
		if tc.Title != "" {
			merged.Title = tc.Title
		}
		if tc.Kind != "" {
			merged.Kind = tc.Kind
		}
		if has(tc.RawInput) {
			merged.RawInput = tc.RawInput
		}
		tc = merged
	}
	s.mu.Unlock()
	pick := func(kinds ...string) map[string]any {
		for _, k := range kinds {
			if i := slices.IndexFunc(p.Options, func(o permissionOption) bool { return o.Kind == k }); i >= 0 {
				return map[string]any{"outcome": map[string]any{"outcome": "selected", "optionId": p.Options[i].ID}}
			}
		}
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}
	}
	edits := tc.Kind == "edit" || tc.Kind == "delete" || tc.Kind == "move"
	switch {
	case s.spec.Policy == agent.PolicyBypass, (s.spec.Policy == agent.PolicyLight || s.spec.Policy == agent.PolicyAuto) && edits:
		return pick("allow_once", "allow_always"), nil
	case s.spec.Policy == agent.PolicyRefuse && guard.Allowed(s.spec, tc.tool()):
		return pick("allow_once", "allow_always"), nil
	case s.spec.Policy == agent.PolicyRefuse:
		return pick("reject_once", "reject_always"), nil
	}
	// What allow_always remembers is the agent's to say, in words: there's no rule of Oriel's to show for it.
	id := s.next("perm")
	d := s.ask(ctx, id, agent.Permission{ID: id, Tool: tc.tool(), Summary: tc.Title, Input: tc.input()})
	switch {
	case d.cancelled:
		return map[string]any{"outcome": map[string]any{"outcome": "cancelled"}}, nil
	case d.d.Allow && d.d.Always:
		return pick("allow_always", "allow_once"), nil
	case d.d.Allow:
		return pick("allow_once", "allow_always"), nil
	}
	if d.d.Note != "" {
		s.mu.Lock()
		s.notes = append(s.notes, fmt.Sprintf("The user denied %s: %s", cmpOr(tc.Title, tc.tool()), d.d.Note))
		s.mu.Unlock()
	}
	if d.d.Always {
		return pick("reject_always", "reject_once"), nil
	}
	return pick("reject_once", "reject_always"), nil
}

// elicit asks the user a form's questions: each field one, with its choices when it has them.
func (s *session) elicit(ctx context.Context, params json.RawMessage) (any, error) {
	var p struct {
		Mode    string `json:"mode"`
		Message string `json:"message"`
		Schema  struct {
			Properties map[string]struct {
				Title       string   `json:"title"`
				Description string   `json:"description"`
				Type        string   `json:"type"`
				Enum        []string `json:"enum"`
				OneOf       []struct {
					Const string `json:"const"`
				} `json:"oneOf"`
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"properties"`
		} `json:"requestedSchema"`
	}
	if err := json.Unmarshal(params, &p); err != nil || p.Mode != "form" {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "only form elicitations are offered"}
	}
	if s.spec.Policy == agent.PolicyRefuse {
		return map[string]any{"action": "decline"}, nil
	}
	names := slices.Sorted(func(yield func(string) bool) {
		for k := range p.Schema.Properties {
			if !yield(k) {
				return
			}
		}
	})
	var qs []core.Question
	byQuestion := map[string]string{}
	for _, name := range names {
		f := p.Schema.Properties[name]
		q := core.Question{Question: cmpOr(f.Title, f.Description, name), Options: f.Enum}
		if len(names) == 1 && p.Message != "" {
			q.Question = p.Message
		} else {
			q.Header = p.Message
		}
		for _, o := range f.OneOf {
			q.Options = append(q.Options, o.Const)
		}
		if f.Type == "array" {
			q.Options, q.Multi = f.Items.Enum, true
		}
		qs = append(qs, q)
		byQuestion[q.Question] = name
	}
	id := s.next("q")
	d := s.ask(ctx, id, agent.Question{ID: id, Questions: qs})
	if d.cancelled {
		return map[string]any{"action": "cancel"}, nil
	}
	if len(d.d.Answers) == 0 {
		return map[string]any{"action": "decline"}, nil
	}
	content := map[string]any{}
	for q, a := range d.d.Answers {
		name, ok := byQuestion[q]
		if !ok {
			continue
		}
		if f := p.Schema.Properties[name]; f.Type == "array" {
			content[name] = strings.Split(a, ", ")
		} else {
			content[name] = a
		}
	}
	return map[string]any{"action": "accept", "content": content}, nil
}

func cmpOr(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
