package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
)

// waitPoll is how long the mod's long poll is held before it's told to ask again.
const waitPoll = 45 * time.Second

// session is one interactive claude in a PTY, driven through the mod.
type session struct {
	spec   agent.StartSpec
	ctx    context.Context
	cancel context.CancelFunc
	pty    agent.PTY
	term   *terminal
	ln     net.Listener
	dir    string // the socket's directory
	temp   string // a state directory made for this session, removed on Close
	tail   *tailer
	events *queue
	done   chan struct{} // closed once the process has exited

	mu      sync.Mutex
	id      string
	ready   bool
	working bool
	cmds    []waitOut
	wake    chan struct{}
	asks    map[string]chan agent.Decision // permissions and questions waiting on Answer
	forks   map[string]chan forkAnswerIn
	seq     int
	base    transcript.Usage // the tally when the running turn started
	screen  screen
	out     bool // its login has been said to be gone, and not seen back
	once    sync.Once
}

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Events() <-chan agent.Event { return s.events.out }
func (s *session) Terminal() agent.Terminal   { return s.term }

// queueCmd hands the mod something to do at its next wait.
func (s *session) queueCmd(c ...waitOut) {
	s.mu.Lock()
	s.cmds = append(s.cmds, c...)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *session) Send(ctx context.Context, in agent.Input) error {
	if err := s.alive(); err != nil {
		return err
	}
	text := in.Text
	for _, img := range in.Images {
		text += "\n\n[Image: " + img + "]"
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("claude: nothing to send")
	}
	s.mu.Lock()
	working := s.working
	s.mu.Unlock()
	var cmds []waitOut
	if in.Urgent && working {
		cmds = append(cmds, waitOut{Kind: "abort"})
	}
	s.queueCmd(append(cmds, waitOut{Kind: "submit", Text: text, Urgent: in.Urgent})...)
	return nil
}

func (s *session) Interrupt(ctx context.Context) error {
	if err := s.alive(); err != nil {
		return err
	}
	s.mu.Lock()
	working := s.working
	s.mu.Unlock()
	if working {
		s.queueCmd(waitOut{Kind: "abort"})
	}
	return nil
}

func (s *session) Fork(ctx context.Context, f agent.ForkSpec) (string, error) {
	if err := s.alive(); err != nil {
		return "", err
	}
	ch := make(chan forkAnswerIn, 1)
	s.mu.Lock()
	s.seq++
	id := "fork-" + strconv.Itoa(s.seq)
	s.forks[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.forks, id)
		s.mu.Unlock()
	}()
	s.queueCmd(waitOut{Kind: "fork", ID: id, Prompt: f.Prompt})
	select {
	case a := <-ch:
		switch {
		case a.Answered:
			return a.Text, nil
		case a.Reason == "nothing-to-fork":
			return "", agent.ErrNoFork
		default:
			return "", fmt.Errorf("claude: the fork got no answer: %s", a.Reason)
		}
	case <-ctx.Done():
		return "", ctx.Err()
	case <-s.done:
		return "", errors.New("claude: the session ended")
	}
}

func (s *session) Answer(ctx context.Context, id string, d agent.Decision) error {
	s.mu.Lock()
	ch, ok := s.asks[id]
	delete(s.asks, id)
	s.mu.Unlock()
	if !ok {
		return fmt.Errorf("claude: no permission or question %q is waiting", id)
	}
	ch <- d
	return nil
}

func (s *session) alive() error {
	select {
	case <-s.done:
		return errors.New("claude: the session has ended")
	default:
		return nil
	}
}

func (s *session) Close() error {
	s.once.Do(func() {
		s.cancel()
		_ = s.pty.Close()
		select {
		case <-s.done:
		case <-time.After(10 * time.Second):
		}
		if s.temp != "" {
			_ = os.RemoveAll(s.temp)
		}
	})
	return nil
}

// run waits for the process, and ends the session with it.
func (s *session) run() {
	err := s.pty.Wait()
	s.cancel()
	_ = s.ln.Close()
	_ = os.RemoveAll(s.dir)
	s.tail.flush(s.push)
	ev := agent.Exited{}
	if err != nil {
		ev.Err = err.Error()
	}
	s.events.push(ev)
	close(s.done)
	s.events.close()
}

// push says ev, with the account a login event is about.
func (s *session) push(ev agent.Event) {
	switch e := ev.(type) {
	case agent.AuthExpired:
		e.Account, _ = account(s.accountEnv())
		s.mu.Lock()
		s.out = true
		s.mu.Unlock()
		ev = e
	case agent.AuthRestored:
		e.Account, _ = account(s.accountEnv())
		s.mu.Lock()
		s.out = false
		s.mu.Unlock()
		ev = e
	}
	s.events.push(ev)
}

// accountEnv is the environment claude signs in from: the host's for a sandbox, which signs in as the host does.
func (s *session) accountEnv() []string {
	if s.spec.Sandbox != nil {
		return nil
	}
	return s.spec.Env
}

// loginLost is the event for a login claude says is gone.
func loginLost(detail string) agent.AuthExpired {
	return agent.AuthExpired{Provider: "claude", Detail: detail, Terminal: "/login", Shell: "claude, then /login"}
}

// watchScreen reads the terminal for the login going, once while it stays gone, and coming back.
func (s *session) watchScreen(p []byte) {
	says, ok := s.screen.read(p)
	if !ok {
		return
	}
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if says.signedOut != "" && !out {
		s.push(loginLost(says.signedOut))
	}
	if says.signedIn {
		s.push(agent.AuthRestored{Provider: "claude"})
	}
}

// serve answers the mod's hook calls.
func (s *session) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

func (s *session) serveConn(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	var req request
	if err := json.NewDecoder(r).Decode(&req); err != nil {
		return
	}
	// The client keeps the connection open while it waits: its closing is the hook process gone.
	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	go func() {
		_, _ = conn.Read(make([]byte, 1))
		cancel()
	}()
	out, undo, err := s.handle(ctx, req.Op, req.Body)
	res := response{}
	if err != nil {
		res.Error = err.Error()
	} else if res.Body, err = json.Marshal(out); err != nil {
		res.Error = err.Error()
	}
	if err := json.NewEncoder(conn).Encode(res); err != nil && undo != nil {
		undo()
	}
}

func decode[T any](body json.RawMessage) (T, error) {
	var v T
	if len(body) == 0 {
		return v, nil
	}
	err := json.Unmarshal(body, &v)
	return v, err
}

// handle answers one hook call. undo puts back what a call took when its answer couldn't be delivered.
func (s *session) handle(ctx context.Context, op string, body json.RawMessage) (out any, undo func(), err error) {
	switch op {
	case opHello:
		in, err := decode[helloIn](body)
		if err != nil {
			return nil, nil, err
		}
		return s.hello(in), nil, nil
	case opWait:
		c := s.wait(ctx)
		if c.Kind == "timeout" {
			return c, nil, nil
		}
		return c, func() { s.requeue(c) }, nil
	case opEvent:
		in, err := decode[eventIn](body)
		if err != nil {
			return nil, nil, err
		}
		s.event(in)
		return struct{}{}, nil, nil
	case opCheck:
		in, err := decode[checkIn](body)
		if err != nil {
			return nil, nil, err
		}
		return check(s.spec, in.Tool, in.Input), nil, nil
	case opPermission:
		in, err := decode[permissionIn](body)
		if err != nil {
			return nil, nil, err
		}
		return s.permission(ctx, in), nil, nil
	case opQuestion:
		in, err := decode[questionIn](body)
		if err != nil {
			return nil, nil, err
		}
		return s.question(ctx, in), nil, nil
	case opTool:
		in, err := decode[toolIn](body)
		if err != nil {
			return nil, nil, err
		}
		return s.callTool(ctx, in), nil, nil
	case opForkAnswer:
		in, err := decode[forkAnswerIn](body)
		if err != nil {
			return nil, nil, err
		}
		s.mu.Lock()
		ch := s.forks[in.ID]
		s.mu.Unlock()
		if ch != nil {
			select {
			case ch <- in:
			default:
			}
		}
		return struct{}{}, nil, nil
	}
	return nil, nil, fmt.Errorf("unknown op %q", op)
}

func (s *session) hello(in helloIn) helloOut {
	s.mu.Lock()
	if in.SessionID != "" && in.SessionID != s.id {
		s.id = in.SessionID
		s.tail.setID(in.SessionID)
	}
	first := !s.ready
	s.ready = true
	id := s.id
	s.mu.Unlock()
	if in.Cwd != "" {
		s.tail.setCwd(in.Cwd)
	}
	if first {
		s.push(agent.Ready{ID: id})
	}
	out := helloOut{Tools: []toolSpec{}}
	for _, t := range s.spec.Tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, toolSpec{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out
}

func (s *session) wait(ctx context.Context) waitOut {
	timer := time.NewTimer(waitPoll)
	defer timer.Stop()
	for {
		s.mu.Lock()
		if len(s.cmds) > 0 {
			c := s.cmds[0]
			s.cmds = s.cmds[1:]
			s.mu.Unlock()
			return c
		}
		s.mu.Unlock()
		select {
		case <-s.wake:
		case <-timer.C:
			return waitOut{Kind: "timeout"}
		case <-ctx.Done():
			return waitOut{Kind: "timeout"}
		}
	}
}

func (s *session) requeue(c waitOut) {
	s.mu.Lock()
	s.cmds = append([]waitOut{c}, s.cmds...)
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *session) event(in eventIn) {
	switch in.Kind {
	case "turn-start":
		s.tail.flush(s.push)
		s.tail.startTurn()
		base := s.tail.usage()
		s.mu.Lock()
		s.working = true
		s.base = base
		s.mu.Unlock()
		s.push(agent.TurnStart{})
	case "turn-complete":
		s.tail.flushUntil(in.Answer, s.push)
		s.mu.Lock()
		s.working = false
		base := s.base
		s.mu.Unlock()
		s.push(agent.TurnEnd{Usage: turnUsageOf(in, base, s.tail.usage())})
		if len(in.RateLimits) > 0 {
			u := usageOf(in.RateLimits)
			u.Account, u.AccountName = account(s.accountEnv())
			u.Plan, u.Tier = planOf(s.accountEnv())
			remember(u)
			s.push(agent.UsageUpdate{Usage: u})
		}
	case "subagent-start":
		s.push(agent.Subagent{ID: in.ID, Description: in.Description, Type: in.Type, Prompt: in.Prompt})
	case "subagent-run":
		s.push(agent.Subagent{ID: in.ID})
	case "subagent-end":
		s.tail.flush(s.push)
		s.push(agent.Subagent{ID: in.ID, Done: true, Result: in.Text})
	case "api-error":
		msg := in.Error
		if in.Text != "" {
			msg += ": " + in.Text
		}
		s.push(agent.Failure{Err: msg, Retryable: retryable(in.Error)})
		if needsLogin(in.Text) {
			s.push(loginLost(in.Text))
		}
	}
}

// retryable is an API failure that may pass if the turn is tried again.
func retryable(kind string) bool {
	switch kind {
	case "rate_limit", "overloaded", "server_error", "unknown":
		return true
	}
	return false
}

// turnUsageOf is what a turn used: the hook's own count when it has one, else the log's since the turn started.
func turnUsageOf(in eventIn, base, now transcript.Usage) agent.Usage {
	u := agent.Usage{
		Input:      now.Input - base.Input,
		Output:     now.Output - base.Output,
		CacheRead:  now.CacheRead - base.CacheRead,
		CacheWrite: now.CacheCreate - base.CacheCreate,
		Context:    int(now.Context),
		ContextMax: int(now.Window),
		Cost:       now.Cost - base.Cost,
		Duration:   time.Duration(in.DurationMs) * time.Millisecond,
	}
	if t := in.Usage; t != nil {
		u.Input, u.Output, u.CacheRead, u.CacheWrite = t.Input, t.Output, t.CacheRead, t.CacheCreate
		model := t.Model
		if model == "" {
			model = now.Model
		}
		u.Cost = price.Tokens{Input: t.Input, CacheWrite5m: t.CacheCreate, CacheRead: t.CacheRead, Output: t.Output}.
			Cost(price.RateFor(model, false))
	}
	if c := in.Context; c != nil && c.Window > 0 {
		u.Context, u.ContextMax = int(c.Tokens), int(c.Window)
	}
	return u
}

func (s *session) ask(ctx context.Context, id string, ev agent.Event) (agent.Decision, bool) {
	ch := make(chan agent.Decision, 1)
	s.mu.Lock()
	s.asks[id] = ch
	s.mu.Unlock()
	s.push(ev)
	select {
	case d := <-ch:
		return d, true
	case <-ctx.Done():
		s.mu.Lock()
		delete(s.asks, id)
		s.mu.Unlock()
		return agent.Decision{}, false
	}
}

func (s *session) permission(ctx context.Context, in permissionIn) permissionOut {
	d, ok := s.ask(ctx, in.ID, agent.Permission{
		ID: in.ID, Tool: in.Tool, Summary: summary(in.Tool, in.Input), Input: in.Input, Rule: rule(in.Suggestions),
	})
	switch {
	case !ok:
		return permissionOut{}
	case d.Allow:
		return permissionOut{Decision: "allow", Always: d.Always}
	default:
		return permissionOut{Decision: "deny", Message: denial(d.Note)}
	}
}

// denial is what the agent reads as the tool's result when you deny it.
func denial(note string) string {
	if note = strings.TrimSpace(note); note != "" {
		return "Denied by the user: " + note
	}
	return "Denied by the user."
}

func (s *session) question(ctx context.Context, in questionIn) questionOut {
	d, ok := s.ask(ctx, in.ID, agent.Question{ID: in.ID, Questions: transcript.Questions(in.Input)})
	switch {
	case !ok:
		return questionOut{}
	case len(d.Answers) > 0:
		return questionOut{Answers: d.Answers}
	default:
		note := strings.TrimSpace(d.Note)
		if note == "" {
			note = "The user chose not to answer."
		}
		return questionOut{Deny: note}
	}
}

func (s *session) callTool(ctx context.Context, in toolIn) toolOut {
	i := slices.IndexFunc(s.spec.Tools, func(t agent.Tool) bool { return t.Name == in.Name })
	if i < 0 || s.spec.Tools[i].Call == nil {
		return toolOut{Result: "Oriel has no tool " + in.Name + ".", IsError: true}
	}
	input := in.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	out, err := s.spec.Tools[i].Call(ctx, input)
	if err != nil {
		return toolOut{Result: err.Error(), IsError: true}
	}
	return toolOut{Result: out}
}

// summary is a permission's one line: the command, the file, or the input's most readable part.
func summary(tool string, input json.RawMessage) string {
	var in map[string]any
	_ = json.Unmarshal(input, &in)
	for _, k := range []string{"command", "file_path", "notebook_path", "url"} {
		if v, ok := in[k].(string); ok && v != "" {
			return v
		}
	}
	return transcript.Summary(input)
}

// rule is what "Always" would allow, from the permission's suggestions: Bash(go test:*).
func rule(suggestions json.RawMessage) string {
	var sug []struct {
		Type  string `json:"type"`
		Rules []struct {
			Tool    string `json:"toolName"`
			Content string `json:"ruleContent"`
		} `json:"rules"`
	}
	if json.Unmarshal(suggestions, &sug) != nil {
		return ""
	}
	var out []string
	for _, s := range sug {
		if s.Type != "addRules" {
			continue
		}
		for _, r := range s.Rules {
			if r.Content != "" {
				out = append(out, r.Tool+"("+r.Content+")")
			} else {
				out = append(out, r.Tool)
			}
		}
	}
	return strings.Join(out, ", ")
}

// queue is an unbounded event queue in front of the Events channel, so nothing the session does waits on a reader.
type queue struct {
	mu     sync.Mutex
	items  []agent.Event
	signal chan struct{}
	closed bool
	out    chan agent.Event
	stop   chan struct{}
}

func newQueue() *queue {
	q := &queue{signal: make(chan struct{}, 1), out: make(chan agent.Event, 64), stop: make(chan struct{})}
	go q.pump()
	return q
}

func (q *queue) push(ev agent.Event) {
	q.mu.Lock()
	if !q.closed {
		q.items = append(q.items, ev)
	}
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

// close ends the channel once what's queued is delivered, or dropped if nobody is reading.
func (q *queue) close() {
	q.mu.Lock()
	q.closed = true
	q.mu.Unlock()
	select {
	case q.signal <- struct{}{}:
	default:
	}
}

func (q *queue) pump() {
	for range q.signal {
		for {
			q.mu.Lock()
			if len(q.items) == 0 {
				closed := q.closed
				q.mu.Unlock()
				if closed {
					close(q.out)
					return
				}
				break
			}
			ev := q.items[0]
			q.items = q.items[1:]
			closed := q.closed
			q.mu.Unlock()
			if closed {
				select {
				case q.out <- ev:
				case <-time.After(time.Second):
				}
				continue
			}
			q.out <- ev
		}
	}
}

// fromEntry is a log entry as an event.
func fromEntry(e transcript.Entry) agent.Event {
	switch e.Kind {
	case transcript.Text:
		if e.APIError != "" && needsLogin(e.Text) {
			return loginLost(e.Text)
		}
		role := core.ItemAssistant
		if e.Role == "user" {
			role = core.ItemUser
		}
		return agent.Message{ID: e.ID, Role: role, Text: e.Text, Agent: e.Agent}
	case transcript.Thinking:
		return agent.Message{ID: e.ID, Role: core.ItemThinking, Text: e.Text, Agent: e.Agent}
	case transcript.Plugin:
		if e.Tool == "oriel" {
			return agent.Message{ID: e.ID, Role: core.ItemUser, Text: e.Text, Agent: e.Agent}
		}
		return agent.Message{ID: e.ID, Role: core.ItemSystem, Text: "The " + e.Tool + " plugin: " + e.Text, Agent: e.Agent}
	case transcript.ToolUse:
		return agent.ToolCall{ID: e.ID, Tool: e.Tool, Input: e.Input, Agent: e.Agent}
	case transcript.ToolResult:
		return agent.ToolResult{ID: e.ID, Output: e.Text, IsError: e.IsError, Diff: e.Diff}
	}
	return nil
}
