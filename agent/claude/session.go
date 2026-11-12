package claude

import (
	"bufio"
	"cmp"
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
	"github.com/liamg/oriel/agent/eventq"
	"github.com/liamg/oriel/agent/price"
	"github.com/liamg/oriel/core"
)

// waitPoll is how long the mod's long poll is held before it's told to ask again.
const waitPoll = 45 * time.Second

// session is one interactive claude in a PTY, driven through the mod.
type session struct {
	prices price.Books
	usage  *usageBook // its provider's
	spec   agent.StartSpec
	ctx    context.Context
	cancel context.CancelFunc
	pty    agent.PTY
	term   *terminal
	ln     net.Listener
	dir    string // the socket's directory
	temp   string // a state directory made for this session, removed on Close
	tail   *tailer
	events *eventq.Queue
	done   chan struct{} // closed once the process has exited
	keep   func()        // lets go of the sandboxes' login keeper (claudebox.Box.KeepLogins); nil on the host

	mu       sync.Mutex
	id       string
	ready    bool
	working  bool
	cmds     []waitOut
	wake     chan struct{}
	asks     map[string]chan agent.Decision // permissions and questions waiting on Answer
	forks    map[string]chan forkAnswerIn
	seq      int
	base     transcript.Usage // the tally when the running turn started
	screen   screen
	offered  []string  // the tools' names hello last handed the mod
	shown    string    // the end of the terminal's text, for saying what was on screen
	usageErr string    // why $.session.usage() last failed; "" when it didn't
	out      bool      // its login has been said to be gone, and not seen back
	dialog   bool      // its terminal has been said to show a dialog, not since answered
	heard    time.Time // the mod's last call: it long-polls, so it calls at least each waitPoll
	once     sync.Once

	unattendedWait time.Duration // how long an unattended agent's ask waits for an answer
}

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Events() <-chan agent.Event { return s.events.Out() }

// quiet is how long the mod may go without a call before the session counts as not answering: a few polls.
const quiet = 3*waitPoll + 15*time.Second

// Responsive says the mod has called lately: since the start, at the latest.
func (s *session) Responsive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heard.IsZero() || time.Since(s.heard) < quiet
}
func (s *session) Terminal() agent.Terminal { return s.term }

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
	if s.keep != nil {
		s.keep()
	}
	_ = s.ln.Close()
	_ = os.RemoveAll(s.dir)
	s.tail.flush(s.push)
	ev := agent.Exited{}
	if err != nil {
		ev.Err = err.Error()
	}
	s.events.Push(ev)
	close(s.done)
	s.events.Close()
}

// push says ev, with the account a login event is about.
func (s *session) push(ev agent.Event) {
	switch e := ev.(type) {
	case agent.AuthExpired:
		e.Account, _ = account(s.accountEnv())
		// A sandbox signs in as the host does: a /login typed in it would sign in that sandbox alone.
		if s.spec.Sandbox != nil {
			e.Terminal = ""
		}
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
	s.events.Push(ev)
}

// accountEnv is the environment claude signs in from: the host's for a sandbox, which signs in as the host does.
func (s *session) accountEnv() []string {
	if s.spec.Sandbox != nil {
		return nil
	}
	return s.spec.Env
}

// answered is a request that worked: a login that had gone is back.
func (s *session) answered(emit func(agent.Event)) {
	s.mu.Lock()
	out := s.out
	s.mu.Unlock()
	if out {
		emit(agent.AuthRestored{Provider: "claude"})
	}
}

// hostSignIn signs the host's claude in again, and with it the sandboxes, which share its sign-in.
var hostSignIn = []string{"claude", "auth", "login"}

// loginLost is the event for a login claude says is gone.
func loginLost(detail string) agent.AuthExpired {
	return agent.AuthExpired{Provider: "claude", Detail: detail, Terminal: "/login", Shell: "claude auth login", HostCommand: hostSignIn}
}

// watchScreen reads the terminal for the login going, once while it stays gone, and coming back.
func (s *session) watchScreen(p []byte) {
	s.keepShown(p)
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
	if says.dialog {
		s.dialogShown(says.title)
	}
}

// dialogShown says the terminal shows a dialog asking title, once while it stays.
func (s *session) dialogShown(title string) {
	s.mu.Lock()
	shown := s.dialog
	s.dialog = true
	s.mu.Unlock()
	if !shown {
		s.push(agent.Dialog{Title: title, Shown: true})
	}
}

// callTitle is what the engine's dialog asks of a call: its tool, and what it's on.
func callTitle(tool string, input json.RawMessage) string {
	if sum := summary(tool, input); sum != "" {
		return "Allow " + tool + ": " + sum + "?"
	}
	return "Allow " + tool + "?"
}

// typed sees what's typed into the terminal: Enter or Esc answers a dialog it shows.
func (s *session) typed(p []byte) {
	if answersDialog(p) {
		s.dialogGone()
	}
}

// answersDialog is input with Enter or Esc in it; an escape sequence (an arrow key) isn't Esc.
func answersDialog(p []byte) bool {
	for i, c := range p {
		if c == '\r' || c == 0x1b && (i+1 == len(p) || p[i+1] != '[' && p[i+1] != 'O') {
			return true
		}
	}
	return false
}

// dialogGone says a dialog the terminal showed is answered.
func (s *session) dialogGone() {
	s.mu.Lock()
	shown := s.dialog
	s.dialog = false
	s.mu.Unlock()
	if shown {
		s.push(agent.Dialog{})
	}
}

// shownMax is how much of the terminal's text keepShown keeps.
const shownMax = 400

// keepShown keeps the end of the terminal's text, its whitespace collapsed.
func (s *session) keepShown(p []byte) {
	t := strings.Join(strings.Fields(plain(p)), " ")
	if t == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.shown = strings.TrimSpace(s.shown + " " + t)
	if len(s.shown) > shownMax {
		s.shown = strings.ToValidUTF8(s.shown[len(s.shown)-shownMax:], "")
	}
}

func (s *session) lastScreen() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shown
}

// unreadEvent notes a usage report that didn't decode as why there's no reading: the mod sent it, so it's said.
func (s *session) unreadEvent(body json.RawMessage, err error) {
	if k, _ := decode[struct {
		Kind string `json:"kind"`
	}](body); k.Kind != "usage" {
		return
	}
	s.mu.Lock()
	raw := string(body)
	if len(raw) > 600 {
		raw = strings.ToValidUTF8(raw[:600], "") + "…"
	}
	s.usageErr = fmt.Sprintf("its report didn't decode (%v): %s", err, raw)
	s.mu.Unlock()
}

func (s *session) usageFailure() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.usageErr
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
	s.mu.Lock()
	s.heard = time.Now()
	s.mu.Unlock()
	switch op {
	case opHello:
		in, err := decode[helloIn](body)
		if err != nil {
			return nil, nil, err
		}
		return s.hello(in), nil, nil
	case opWait:
		c := s.wait(ctx)
		if c.Kind == "timeout" || c.Kind == "tools" {
			return c, nil, nil
		}
		return c, func() { s.requeue(c) }, nil
	case opEvent:
		in, err := decode[eventIn](body)
		if err != nil {
			s.unreadEvent(body, err)
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
		s.dialogGone() // Oriel asks it, in the dialog's place
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
	tools := s.spec.Offered()
	s.mu.Lock()
	s.offered = agent.ToolNames(tools)
	s.mu.Unlock()
	for _, t := range tools {
		schema := t.Schema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object"}`)
		}
		out.Tools = append(out.Tools, toolSpec{Name: t.Name, Description: t.Description, InputSchema: schema})
	}
	return out
}

func (s *session) wait(ctx context.Context) waitOut {
	if s.toolsChanged() {
		return waitOut{Kind: "tools"}
	}
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
			if s.toolsChanged() {
				return waitOut{Kind: "tools"}
			}
			return waitOut{Kind: "timeout"}
		case <-ctx.Done():
			return waitOut{Kind: "timeout"}
		}
	}
}

// toolsChanged says the tools offered now aren't those hello last handed the mod: it says hello again to get them.
func (s *session) toolsChanged() bool {
	s.mu.Lock()
	ready, offered := s.ready, s.offered
	s.mu.Unlock()
	return ready && !slices.Equal(offered, agent.ToolNames(s.spec.Offered()))
}

// queued is whether a command of kind waits for the mod.
func (s *session) queued(kind string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.ContainsFunc(s.cmds, func(c waitOut) bool { return c.Kind == kind })
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
		s.dialogGone()
		s.tail.flush(s.push)
		s.tail.startTurn()
		base := s.tail.usage()
		s.mu.Lock()
		s.working = true
		s.base = base
		s.mu.Unlock()
		s.push(agent.TurnStart{})
	case "turn-complete":
		s.dialogGone()
		s.tail.flushUntil(in.Answer, s.push)
		s.mu.Lock()
		s.working = false
		base := s.base
		s.mu.Unlock()
		var bg []core.BackgroundTask
		for _, t := range in.Background {
			bg = append(bg, core.BackgroundTask{Kind: t.Type, Description: cmp.Or(t.Description, t.Command, t.Type)})
		}
		s.push(agent.TurnEnd{Usage: turnUsageOf(in, base, s.tail.usage(), s.prices.Book()), Background: bg})
		s.planUsage(in.RateLimits)
	case "usage":
		s.mu.Lock()
		s.usageErr = in.Error
		s.mu.Unlock()
		s.planUsage(in.RateLimits)
	case "subagent-start":
		s.push(agent.Subagent{ID: in.ID, Description: in.Description, Type: in.Type, Prompt: in.Prompt})
	case "subagent-run":
		s.push(agent.Subagent{ID: in.ID})
	case "subagent-end":
		s.tail.flush(s.push)
		s.push(agent.Subagent{ID: in.ID, Done: true, Result: in.Text})
	case "mode":
		if in.Mode != "" {
			s.push(agent.ModeChanged{Mode: in.Mode})
		}
	case "dialog":
		s.dialogShown(callTitle(in.Tool, in.Input))
	case "dialog-gone":
		s.dialogGone()
	case "tools-lost":
		s.push(agent.Stale{Why: "its CLI dropped " + strings.Join(in.Servers, ", ")})
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

// planUsage reports the plan's windows the mod read, when it read any.
func (s *session) planUsage(rls []rateLimit) {
	if len(rls) == 0 {
		return
	}
	u := usageOf(rls)
	u.Account, u.AccountName = account(s.accountEnv())
	u.Plan, u.Tier = planOf(s.accountEnv())
	s.usage.remember(u)
	s.push(agent.UsageUpdate{Usage: u})
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
func turnUsageOf(in eventIn, base, now transcript.Usage, prices *price.Book) agent.Usage {
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
		var prompt int64
		if c := in.Context; c != nil {
			prompt = c.Tokens
		}
		u.Cost = price.Tokens{Input: t.Input, CacheWrite5m: t.CacheCreate, CacheRead: t.CacheRead, Output: t.Output}.
			Cost(prices.RateFor(model, false, prompt))
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
	var unanswered <-chan time.Time
	if s.spec.Unattended {
		t := time.NewTimer(cmp.Or(s.unattendedWait, unattendedWait))
		defer t.Stop()
		unanswered = t.C
	}
	forget := func() {
		s.mu.Lock()
		delete(s.asks, id)
		s.mu.Unlock()
	}
	select {
	case d := <-ch:
		return d, true
	case <-unanswered:
		forget()
		return agent.Decision{Note: agent.UnattendedRefusal}, true
	case <-ctx.Done():
		forget()
		return agent.Decision{}, false
	}
}

// unattendedWait is how long an unattended agent's ask waits for whoever drives it before it's refused.
const unattendedWait = 30 * time.Second

func (s *session) permission(ctx context.Context, in permissionIn) permissionOut {
	d, ok := s.ask(ctx, in.ID, agent.Permission{
		ID: in.ID, Tool: in.Tool, Summary: summary(in.Tool, in.Input), Input: in.Input, Rule: rule(in.Suggestions), Diff: transcript.Diff(in.Tool, in.Input, nil),
	})
	switch {
	case !ok:
		return permissionOut{}
	case d.Allow:
		return permissionOut{Decision: "allow", Always: d.Always}
	case d.Note == agent.UnattendedRefusal:
		return permissionOut{Decision: "deny", Message: d.Note}
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
	tools := s.spec.Offered()
	i := slices.IndexFunc(tools, func(t agent.Tool) bool { return t.Name == in.Name })
	if i < 0 || tools[i].Call == nil {
		return toolOut{Result: "Oriel has no tool " + in.Name + ".", IsError: true}
	}
	input := in.Input
	if len(input) == 0 {
		input = json.RawMessage(`{}`)
	}
	out, err := tools[i].Call(ctx, input)
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
		return agent.ToolCall{ID: e.ID, Tool: e.Tool, Input: e.Input, Agent: e.Agent, Model: e.Model}
	case transcript.ToolResult:
		return agent.ToolResult{ID: e.ID, Output: e.Text, IsError: e.IsError, Diff: e.Diff}
	}
	return nil
}
