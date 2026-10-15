package dap

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"slices"
	"sync"
	"time"
)

// ErrClosed is returned for a request made once the adapter has gone.
var ErrClosed = errors.New("dap: the adapter has gone")

// Error is an adapter's error response to a request.
type Error struct {
	Command string
	Message string
}

func (e *Error) Error() string { return e.Command + ": " + e.Message }

// Options are how a client starts its adapter and where what the adapter says goes.
type Options struct {
	Client string   // the clientID sent in initialize: "oriel"
	Launch Launcher // starts the adapter; Exec by default, and the spec's Connect wins over both
	Stderr io.Writer
	// OnEvent gets each event in the order the adapter sent it, on the client's own goroutine: it may call the
	// client.
	OnEvent func(Event)
	// Breakpoints are set before the debuggee runs, by path.
	Breakpoints map[string][]Breakpoint
	// Exceptions are the exception filters to stop on; nil takes those the adapter turns on by default.
	Exceptions []string
}

// Client is a session with one debug adapter, for one launch config.
type Client struct {
	spec AdapterSpec
	cfg  LaunchConfig
	proc Process
	opts Options

	wmu sync.Mutex // one write at a time

	mu       sync.Mutex
	seq      int
	pending  map[int]chan *Message
	caps     Capabilities
	bps      map[string][]BreakpointState
	ended    bool // the adapter said terminated
	err      error
	queue    []Event
	queued   chan struct{}
	initDone chan struct{}
	initOnce sync.Once

	done chan struct{}
}

// Start launches the adapter, initializes it, sends the launch or attach request, sets the breakpoints and exception
// filters once it's ready for them and says configuration is done (overview.md, "Launch Sequencing"). It returns once
// the launch or attach has succeeded.
func Start(ctx context.Context, spec AdapterSpec, cfg LaunchConfig, o Options) (*Client, error) {
	launch := o.Launch
	if spec.Connect != nil {
		launch = spec.Connect
	}
	if launch == nil {
		launch = Exec
	}
	stderr := o.Stderr
	if stderr == nil {
		stderr = io.Discard
	}
	p, err := launch(ctx, spec, cfg.Cwd, stderr)
	if err != nil {
		return nil, err
	}
	c := &Client{spec: spec, cfg: cfg, proc: p, opts: o, pending: map[int]chan *Message{}, bps: map[string][]BreakpointState{},
		queued: make(chan struct{}, 1), initDone: make(chan struct{}), done: make(chan struct{})}
	go c.read()
	go c.dispatch()
	if err := c.start(ctx); err != nil {
		c.kill()
		return nil, err
	}
	return c, nil
}

type initializeArgs struct {
	ClientID                     string `json:"clientID"`
	ClientName                   string `json:"clientName"`
	AdapterID                    string `json:"adapterID"`
	Locale                       string `json:"locale"`
	LinesStartAt1                bool   `json:"linesStartAt1"`
	ColumnsStartAt1              bool   `json:"columnsStartAt1"`
	PathFormat                   string `json:"pathFormat"`
	SupportsVariableType         bool   `json:"supportsVariableType"`
	SupportsVariablePaging       bool   `json:"supportsVariablePaging"`
	SupportsRunInTerminalRequest bool   `json:"supportsRunInTerminalRequest"`
}

func (c *Client) start(ctx context.Context) error {
	id := c.opts.Client
	if id == "" {
		id = "oriel"
	}
	var caps Capabilities
	if err := c.call(ctx, "initialize", initializeArgs{ClientID: id, ClientName: "Oriel", AdapterID: c.spec.Type, Locale: "en",
		LinesStartAt1: true, ColumnsStartAt1: true, PathFormat: "path", SupportsVariableType: true}, &caps); err != nil {
		return err
	}
	c.mu.Lock()
	c.caps = caps
	c.mu.Unlock()
	launched := c.async(c.cfg.request(), c.cfg.Arguments(c.spec))
	ready := false
	for !ready {
		select {
		case <-c.initDone:
			ready = true
		case err := <-launched:
			if err != nil {
				return err
			}
			launched = nil
		case <-c.done:
			return c.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	paths := make([]string, 0, len(c.opts.Breakpoints))
	for p := range c.opts.Breakpoints {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	for _, p := range paths {
		if _, err := c.SetBreakpoints(ctx, p, c.opts.Breakpoints[p]); err != nil {
			return err
		}
	}
	if len(caps.ExceptionBreakpointFilters) > 0 {
		filters := c.opts.Exceptions
		if filters == nil {
			filters = []string{}
			for _, f := range caps.ExceptionBreakpointFilters {
				if f.Default {
					filters = append(filters, f.Filter)
				}
			}
		}
		if err := c.SetExceptionBreakpoints(ctx, filters); err != nil {
			return err
		}
	}
	if caps.SupportsConfigurationDoneRequest {
		if err := c.call(ctx, "configurationDone", nil, nil); err != nil {
			return err
		}
	}
	if launched != nil {
		select {
		case err := <-launched:
			return err
		case <-c.done:
			return c.Err()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

// Spec is the adapter the client runs.
func (c *Client) Spec() AdapterSpec { return c.spec }

// Config is the launch config it was started with.
func (c *Client) Config() LaunchConfig { return c.cfg }

// Capabilities are what the adapter said it supports.
func (c *Client) Capabilities() Capabilities {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.caps
}

// Done is closed once the connection to the adapter has ended.
func (c *Client) Done() <-chan struct{} { return c.done }

// Err is why the connection ended: ErrClosed after Stop.
func (c *Client) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err == nil {
		return ErrClosed
	}
	return c.err
}

// Ended reports whether the adapter has said the session is over.
func (c *Client) Ended() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ended
}

// Breakpoints are the breakpoints the adapter has for a file, as it last said.
func (c *Client) Breakpoints(path string) []BreakpointState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.bps[path])
}

// SetBreakpoints replaces a file's breakpoints (setBreakpoints is per source, not incremental) and returns what
// the adapter made of each, in order.
func (c *Client) SetBreakpoints(ctx context.Context, path string, bps []Breakpoint) ([]BreakpointState, error) {
	args := struct {
		Source      wireSource             `json:"source"`
		Breakpoints []wireSourceBreakpoint `json:"breakpoints"`
	}{Source: wireSource{Name: filepath.Base(path), Path: path}, Breakpoints: []wireSourceBreakpoint{}}
	for _, b := range bps {
		args.Breakpoints = append(args.Breakpoints, wireSourceBreakpoint{Line: b.Line + 1, Condition: b.Condition,
			HitCondition: b.HitCondition, LogMessage: b.LogMessage})
	}
	var out struct {
		Breakpoints []wireBreakpoint `json:"breakpoints"`
	}
	if err := c.call(ctx, "setBreakpoints", args, &out); err != nil {
		return nil, err
	}
	states := make([]BreakpointState, len(out.Breakpoints))
	for i, b := range out.Breakpoints {
		states[i] = breakpointState(b, path)
	}
	c.mu.Lock()
	if len(states) == 0 {
		delete(c.bps, path)
	} else {
		c.bps[path] = states
	}
	c.mu.Unlock()
	return slices.Clone(states), nil
}

func breakpointState(b wireBreakpoint, path string) BreakpointState {
	s := BreakpointState{ID: b.ID, Verified: b.Verified, Line: b.Line - 1, Message: b.Message, Path: path}
	if b.Line == 0 {
		s.Line = -1
	}
	if b.Source != nil && b.Source.Path != "" {
		s.Path = b.Source.Path
	}
	return s
}

// SetExceptionBreakpoints sets the kinds of exception to stop on, by the adapter's filter names.
func (c *Client) SetExceptionBreakpoints(ctx context.Context, filters []string) error {
	if filters == nil {
		filters = []string{}
	}
	return c.call(ctx, "setExceptionBreakpoints", map[string]any{"filters": filters}, nil)
}

// Threads are the debuggee's threads.
func (c *Client) Threads(ctx context.Context) ([]Thread, error) {
	var out struct {
		Threads []Thread `json:"threads"`
	}
	if err := c.call(ctx, "threads", nil, &out); err != nil {
		return nil, err
	}
	return out.Threads, nil
}

// stackLevels is how many frames Stack asks for.
const stackLevels = 64

// Stack is a stopped thread's frames, innermost first.
func (c *Client) Stack(ctx context.Context, thread int) ([]Frame, error) {
	var out struct {
		StackFrames []wireStackFrame `json:"stackFrames"`
	}
	if err := c.call(ctx, "stackTrace", map[string]any{"threadId": thread, "startFrame": 0, "levels": stackLevels}, &out); err != nil {
		return nil, err
	}
	frames := make([]Frame, len(out.StackFrames))
	for i, f := range out.StackFrames {
		frames[i] = Frame{ID: f.ID, Name: f.Name, Line: max(f.Line-1, 0), Col: max(f.Column-1, 0), Hint: f.PresentationHint}
		if f.Source != nil {
			frames[i].Path = f.Source.Path
		}
	}
	return frames, nil
}

// Scopes are a frame's groups of variables.
func (c *Client) Scopes(ctx context.Context, frame int) ([]Scope, error) {
	var out struct {
		Scopes []struct {
			Name               string `json:"name"`
			VariablesReference int    `json:"variablesReference"`
			Expensive          bool   `json:"expensive"`
			PresentationHint   string `json:"presentationHint"`
		} `json:"scopes"`
	}
	if err := c.call(ctx, "scopes", map[string]any{"frameId": frame}, &out); err != nil {
		return nil, err
	}
	scopes := make([]Scope, len(out.Scopes))
	for i, s := range out.Scopes {
		scopes[i] = Scope{Name: s.Name, Ref: s.VariablesReference, Expensive: s.Expensive, Hint: s.PresentationHint}
	}
	return scopes, nil
}

// Variables are the children of a scope or a structured variable.
func (c *Client) Variables(ctx context.Context, ref int) ([]Variable, error) {
	var out struct {
		Variables []wireVariable `json:"variables"`
	}
	if err := c.call(ctx, "variables", map[string]any{"variablesReference": ref}, &out); err != nil {
		return nil, err
	}
	vars := make([]Variable, len(out.Variables))
	for i, v := range out.Variables {
		vars[i] = Variable{Name: v.Name, Value: v.Value, Type: v.Type, Ref: v.VariablesReference, Named: v.NamedVariables,
			Indexed: v.IndexedVariables, Evaluate: v.EvaluateName}
	}
	return vars, nil
}

// Evaluate evaluates an expression in a frame (0: no frame, the global scope) for a context: ContextWatch,
// ContextREPL or ContextHover. The result is a Variable named by the expression.
func (c *Client) Evaluate(ctx context.Context, frame int, expr, context string) (Variable, error) {
	args := map[string]any{"expression": expr, "context": context}
	if frame != 0 {
		args["frameId"] = frame
	}
	var out struct {
		Result             string `json:"result"`
		Type               string `json:"type"`
		VariablesReference int    `json:"variablesReference"`
		NamedVariables     int    `json:"namedVariables"`
		IndexedVariables   int    `json:"indexedVariables"`
	}
	if err := c.call(ctx, "evaluate", args, &out); err != nil {
		return Variable{}, err
	}
	return Variable{Name: expr, Value: out.Result, Type: out.Type, Ref: out.VariablesReference, Named: out.NamedVariables,
		Indexed: out.IndexedVariables, Evaluate: expr}, nil
}

// Continue runs the debuggee again.
func (c *Client) Continue(ctx context.Context, thread int) error {
	return c.call(ctx, "continue", map[string]any{"threadId": thread}, nil)
}

// Step steps a thread over, into or out of the current line.
func (c *Client) Step(ctx context.Context, thread int, kind StepKind) error {
	return c.call(ctx, kind.command(), map[string]any{"threadId": thread}, nil)
}

// Pause stops a running thread.
func (c *Client) Pause(ctx context.Context, thread int) error {
	return c.call(ctx, "pause", map[string]any{"threadId": thread}, nil)
}

// Stop ends the session: a disconnect, which also ends a debuggee the adapter launched. It waits for the adapter to
// exit, and kills it if it doesn't.
func (c *Client) Stop(ctx context.Context) error {
	err := c.call(ctx, "disconnect", map[string]any{"restart": false, "terminateDebuggee": c.cfg.request() == RequestLaunch}, nil)
	if errors.Is(err, ErrClosed) {
		err = nil
	}
	c.mu.Lock()
	if c.err == nil {
		c.err = ErrClosed
	}
	c.mu.Unlock()
	exited := make(chan struct{})
	go func() {
		c.proc.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		c.proc.Kill()
	case <-ctx.Done():
		c.proc.Kill()
	}
	c.proc.Close()
	<-c.done
	return err
}

func (c *Client) kill() {
	c.proc.Kill()
	c.proc.Close()
}

// call sends a request and decodes its response's body into out.
func (c *Client) call(ctx context.Context, command string, args, out any) error {
	ch, err := c.send(command, args)
	if err != nil {
		return err
	}
	select {
	case m := <-ch:
		if m == nil {
			return c.Err()
		}
		if !m.Success {
			return &Error{Command: command, Message: errorMessage(m)}
		}
		if out != nil && len(m.Body) > 0 {
			if err := json.Unmarshal(m.Body, out); err != nil {
				return fmt.Errorf("dap: %s: %v", command, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.forget(ch)
		return ctx.Err()
	}
}

// async sends a request and reports how it went on the channel it returns.
func (c *Client) async(command string, args any) chan error {
	out := make(chan error, 1)
	ch, err := c.send(command, args)
	if err != nil {
		out <- err
		return out
	}
	go func() {
		m := <-ch
		switch {
		case m == nil:
			out <- c.Err()
		case !m.Success:
			out <- &Error{Command: command, Message: errorMessage(m)}
		default:
			out <- nil
		}
	}()
	return out
}

func (c *Client) send(command string, args any) (chan *Message, error) {
	c.mu.Lock()
	select {
	case <-c.done:
		c.mu.Unlock()
		return nil, c.Err()
	default:
	}
	c.seq++
	seq := c.seq
	ch := make(chan *Message, 1)
	c.pending[seq] = ch
	c.mu.Unlock()
	body, err := json.Marshal(request{Seq: seq, Type: "request", Command: command, Arguments: args})
	if err != nil {
		return nil, err
	}
	if err := c.write(body); err != nil {
		c.mu.Lock()
		delete(c.pending, seq)
		c.mu.Unlock()
		return nil, err
	}
	return ch, nil
}

func (c *Client) forget(ch chan *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for k, v := range c.pending {
		if v == ch {
			delete(c.pending, k)
		}
	}
}

func (c *Client) write(body []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return WriteFrame(c.proc, body)
}

var formatVar = regexp.MustCompile(`\{([^}]+)\}`)

// errorMessage is an error response's message: its body's formatted error when there is one.
func errorMessage(m *Message) string {
	var body struct {
		Error *struct {
			Format    string            `json:"format"`
			Variables map[string]string `json:"variables"`
		} `json:"error"`
	}
	if json.Unmarshal(m.Body, &body) == nil && body.Error != nil && body.Error.Format != "" {
		return formatVar.ReplaceAllStringFunc(body.Error.Format, func(s string) string {
			if v, ok := body.Error.Variables[s[1:len(s)-1]]; ok {
				return v
			}
			return s
		})
	}
	if m.Message != "" {
		return m.Message
	}
	return "failed"
}

// read takes messages off the connection until it ends: responses to their requests, events to the queue, and
// requests from the adapter answered.
func (c *Client) read() {
	r := bufio.NewReader(c.proc)
	var err error
	for {
		var body []byte
		body, err = ReadFrame(r)
		if err != nil {
			break
		}
		var m Message
		if json.Unmarshal(body, &m) != nil {
			continue
		}
		switch m.Type {
		case "response":
			c.mu.Lock()
			ch := c.pending[m.RequestSeq]
			delete(c.pending, m.RequestSeq)
			c.mu.Unlock()
			if ch != nil {
				ch <- &m
			}
		case "event":
			c.event(&m)
		case "request":
			c.reverse(&m)
		}
	}
	c.mu.Lock()
	if c.err == nil {
		if errors.Is(err, io.EOF) {
			err = ErrClosed
		}
		c.err = fmt.Errorf("dap: %s: %w", c.spec.Name, err)
	}
	for k, ch := range c.pending {
		close(ch)
		delete(c.pending, k)
	}
	close(c.done)
	c.mu.Unlock()
	c.initOnce.Do(func() { close(c.initDone) })
}

// reverse answers a request from the adapter. The client offers none (supportsRunInTerminalRequest is false), so
// each is refused.
func (c *Client) reverse(m *Message) {
	c.mu.Lock()
	c.seq++
	seq := c.seq
	c.mu.Unlock()
	body, _ := json.Marshal(response{Seq: seq, Type: "response", RequestSeq: m.Seq, Command: m.Command, Message: "not supported by Oriel"})
	c.write(body)
}

func (c *Client) event(m *Message) {
	var ev Event
	switch m.Event {
	case "initialized":
		c.initOnce.Do(func() { close(c.initDone) })
		return
	case "stopped":
		var b struct {
			Reason            string `json:"reason"`
			Description       string `json:"description"`
			ThreadID          int    `json:"threadId"`
			Text              string `json:"text"`
			AllThreadsStopped bool   `json:"allThreadsStopped"`
			HitBreakpointIDs  []int  `json:"hitBreakpointIds"`
		}
		json.Unmarshal(m.Body, &b)
		ev = Stopped{Reason: b.Reason, Description: b.Description, Text: b.Text, Thread: b.ThreadID, AllThreads: b.AllThreadsStopped,
			Breakpoints: b.HitBreakpointIDs}
	case "continued":
		var b struct {
			ThreadID            int  `json:"threadId"`
			AllThreadsContinued bool `json:"allThreadsContinued"`
		}
		json.Unmarshal(m.Body, &b)
		ev = Continued{Thread: b.ThreadID, AllThreads: b.AllThreadsContinued}
	case "output":
		var b struct {
			Category string      `json:"category"`
			Output   string      `json:"output"`
			Source   *wireSource `json:"source"`
			Line     int         `json:"line"`
		}
		json.Unmarshal(m.Body, &b)
		if b.Category == "" {
			b.Category = "console"
		}
		o := Output{Category: b.Category, Text: b.Output, Line: b.Line - 1}
		if b.Source != nil {
			o.Path = b.Source.Path
		}
		if b.Line == 0 {
			o.Line = -1
		}
		ev = o
	case "exited":
		var b struct {
			ExitCode int `json:"exitCode"`
		}
		json.Unmarshal(m.Body, &b)
		ev = Exited{Code: b.ExitCode}
	case "terminated":
		c.mu.Lock()
		c.ended = true
		c.mu.Unlock()
		ev = Terminated{}
	case "thread":
		var b struct {
			Reason   string `json:"reason"`
			ThreadID int    `json:"threadId"`
		}
		json.Unmarshal(m.Body, &b)
		ev = ThreadChanged{Reason: b.Reason, Thread: b.ThreadID}
	case "breakpoint":
		var b struct {
			Reason     string         `json:"reason"`
			Breakpoint wireBreakpoint `json:"breakpoint"`
		}
		json.Unmarshal(m.Body, &b)
		bs := c.updateBreakpoint(b.Breakpoint, b.Reason)
		ev = BreakpointChanged{Reason: b.Reason, Breakpoint: bs}
	case "process":
		var b struct {
			Name            string `json:"name"`
			SystemProcessID int    `json:"systemProcessId"`
		}
		json.Unmarshal(m.Body, &b)
		ev = ProcessStarted{Name: b.Name, PID: b.SystemProcessID}
	case "capabilities":
		var b struct {
			Capabilities json.RawMessage `json:"capabilities"`
		}
		if json.Unmarshal(m.Body, &b) == nil && len(b.Capabilities) > 0 {
			c.mu.Lock()
			json.Unmarshal(b.Capabilities, &c.caps)
			c.mu.Unlock()
		}
		return
	default:
		return
	}
	c.mu.Lock()
	c.queue = append(c.queue, ev)
	c.mu.Unlock()
	select {
	case c.queued <- struct{}{}:
	default:
	}
}

// updateBreakpoint applies a breakpoint event to the breakpoints kept, by id.
func (c *Client) updateBreakpoint(b wireBreakpoint, reason string) BreakpointState {
	c.mu.Lock()
	defer c.mu.Unlock()
	for path, states := range c.bps {
		for i, s := range states {
			if b.ID == 0 || s.ID != b.ID {
				continue
			}
			ns := breakpointState(b, path)
			if b.Line == 0 {
				ns.Line = s.Line
			}
			if reason == "removed" {
				c.bps[path] = slices.Delete(states, i, i+1)
			} else {
				states[i] = ns
			}
			return ns
		}
	}
	path := ""
	if b.Source != nil {
		path = b.Source.Path
	}
	return breakpointState(b, path)
}

// dispatch hands queued events to OnEvent, one at a time and in order, until the connection ends and the queue is
// empty.
func (c *Client) dispatch() {
	for {
		c.mu.Lock()
		q := c.queue
		c.queue = nil
		c.mu.Unlock()
		for _, ev := range q {
			if c.opts.OnEvent != nil {
				c.opts.OnEvent(ev)
			}
		}
		if len(q) > 0 {
			continue
		}
		select {
		case <-c.queued:
		case <-c.done:
			c.mu.Lock()
			empty := len(c.queue) == 0
			c.mu.Unlock()
			if empty {
				return
			}
		}
	}
}

var _ Session = (*Client)(nil)
