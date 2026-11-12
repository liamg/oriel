package rpcagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/jsonrpc"
)

// Factory makes the provider a plugin serves, from what initialize says: Oriel's name for it and its config.
type Factory func(Init) (agent.Provider, error)

// drainTimeout bounds how long closing a session waits for its last events to be sent.
const drainTimeout = 5 * time.Second

// Serve serves the provider f makes over r and w (a process plugin's stdin and stdout) until the connection closes.
// Its sessions get a StartSpec whose Tools and Exec call back to Oriel, so their processes run where Oriel runs
// the session's: in its sandbox, when it has one.
func Serve(r io.Reader, w io.Writer, f Factory) error {
	s := &server{f: f, sessions: map[string]*served{}, procs: map[string]*remote{}, logins: map[string]context.CancelFunc{},
		ready: make(chan struct{})}
	// The connection reads as it's made: what arrives waits for s.peer to be set before it's handled.
	set := make(chan struct{})
	s.peer = NewConn(r, w, Handler{
		Request: func(ctx context.Context, method string, raw json.RawMessage) (any, error) {
			<-set
			return s.request(ctx, method, raw)
		},
		Notify: func(method string, raw json.RawMessage) {
			<-set
			s.notify(method, raw)
		}})
	close(set)
	<-s.peer.Done()
	s.mu.Lock()
	ss := s.sessions
	s.sessions = nil
	for _, cancel := range s.logins {
		cancel()
	}
	procs := s.procs
	s.procs = nil
	s.mu.Unlock()
	for _, pr := range procs {
		pr.exited(exit{Error: "Oriel went away"})
	}
	for _, x := range ss {
		x.s.Close()
		x.stop()
		x.cleanup()
	}
	return nil
}

type server struct {
	f    Factory
	peer Peer

	mu       sync.Mutex
	p        agent.Provider
	ready    chan struct{} // closed once initialize has made p
	seq      int
	sessions map[string]*served
	procs    map[string]*remote
	logins   map[string]context.CancelFunc
}

func (s *server) provider(ctx context.Context) (agent.Provider, error) {
	select {
	case <-s.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.p == nil {
		return nil, errors.New("initialize failed")
	}
	return s.p, nil
}

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
	}
	return nil
}

func (s *server) request(ctx context.Context, method string, raw json.RawMessage) (any, error) {
	if method == "initialize" {
		return s.initialize(raw)
	}
	p, err := s.provider(ctx)
	if err != nil {
		return nil, err
	}
	switch method {
	case "detect":
		in, err := p.Detect(ctx)
		return installOf(in), err
	case "models":
		ms, err := p.Models(ctx)
		if ms == nil {
			ms = []agent.Model{}
		}
		return ms, err
	case "usage":
		return p.Usage(ctx)
	case "sandbox/needs":
		var w place
		if err := decode(raw, &w); err != nil {
			return nil, err
		}
		sb, ok := p.(agent.Sandboxed)
		if !ok {
			return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "the provider doesn't run in a sandbox"}
		}
		at := w.place()
		at.Report = func(step, note string) {
			s.peer.Notify("sandbox/report", placeCall{Call: w.Call, Step: step, Note: note})
		}
		at.Env = func(name string) string {
			var r placeCall
			callOn(ctx, s.peer, "sandbox/env", placeCall{Call: w.Call, Name: name}, &r)
			return r.Value
		}
		n, err := sb.SandboxNeeds(ctx, at)
		return needsOf(n), err
	case "sandbox/mcpServers":
		out := []mcpServer{}
		if sb, ok := p.(agent.Sandboxed); ok {
			for _, m := range sb.MCPServers() {
				out = append(out, mcpServer{Name: m.Name, Source: m.Source, Config: m.Config, Vars: m.Vars})
			}
		}
		return out, nil
	case "logs":
		var l logsParams
		if err := decode(raw, &l); err != nil {
			return nil, err
		}
		if lp, ok := p.(agent.Logged); ok {
			return logsOf(lp.Logs(l.Cwd, l.Session)), nil
		}
		return []log{}, nil
	case "discover":
		out := []transcript{}
		if d, ok := p.(agent.Discovered); ok {
			for _, t := range d.Discover() {
				out = append(out, transcript{Session: t.Session, Logs: logsOf(t.Logs), Modified: t.Modified})
			}
		}
		return out, nil
	case "login/start":
		var l loginParams
		if err := decode(raw, &l); err != nil {
			return nil, err
		}
		return s.login(p, l.Login)
	case "session/start":
		var sp startParams
		if err := decode(raw, &sp); err != nil {
			return nil, err
		}
		return s.start(ctx, p, sp)
	}
	var sp sessionParams
	if err := decode(raw, &sp); err != nil {
		return nil, err
	}
	s.mu.Lock()
	x := s.sessions[sp.Session]
	s.mu.Unlock()
	if x == nil {
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no session " + sp.Session}
	}
	return x.request(ctx, method, sp)
}

func (s *server) initialize(raw json.RawMessage) (any, error) {
	var in Init
	if err := decode(raw, &in); err != nil {
		return nil, err
	}
	s.mu.Lock()
	select {
	case <-s.ready:
		s.mu.Unlock()
		return nil, &jsonrpc.Error{Code: jsonrpc.InvalidRequest, Message: "already initialized"}
	default:
	}
	p, err := s.f(in)
	s.p = p
	close(s.ready)
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	h := Hello{ProtocolVersion: Version, ID: p.ID(), Features: FeaturesOf(agent.FeaturesOf(p))}
	_, h.Logged = p.(agent.Logged)
	_, h.Discovered = p.(agent.Discovered)
	return h, nil
}

func (s *server) login(p agent.Provider, id string) (any, error) {
	ls, ok := p.(agent.LoginStarter)
	if !ok {
		return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "the provider doesn't sign in from Oriel"}
	}
	ctx, cancel := context.WithCancel(context.Background())
	l, err := ls.StartLogin(ctx)
	if err != nil {
		cancel()
		return nil, err
	}
	s.mu.Lock()
	s.logins[id] = cancel
	s.mu.Unlock()
	go func() {
		defer cancel()
		var err error
		select {
		case e, ok := <-l.Done:
			if ok {
				err = e
			}
		case <-ctx.Done():
			err = ctx.Err()
		}
		s.mu.Lock()
		delete(s.logins, id)
		s.mu.Unlock()
		d := loginParams{Login: id, Done: true}
		if err != nil {
			d.Error = err.Error()
		}
		s.peer.Notify("login/done", d)
	}()
	return loginParams{Login: id, URL: l.URL}, nil
}

func (s *server) notify(method string, raw json.RawMessage) {
	switch method {
	case "exec/output", "exec/exit":
		var o output
		json.Unmarshal(raw, &o)
		s.mu.Lock()
		pr := s.procs[o.Process]
		if method == "exec/exit" {
			delete(s.procs, o.Process)
		}
		s.mu.Unlock()
		if pr == nil {
			return
		}
		if method == "exec/exit" {
			var x exit
			json.Unmarshal(raw, &x)
			pr.exited(x)
			return
		}
		pr.output(o.Stream, unb64(o.Data))
	case "login/cancel":
		var l loginParams
		json.Unmarshal(raw, &l)
		s.mu.Lock()
		cancel := s.logins[l.Login]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	case "session/stop":
		var sp sessionParams
		json.Unmarshal(raw, &sp)
		s.mu.Lock()
		x := s.sessions[sp.Session]
		s.mu.Unlock()
		if x != nil {
			x.stop()
		}
	}
}

// served is a session the provider runs, served to Oriel.
type served struct {
	srv     *server
	handle  string
	s       agent.Session
	stop    context.CancelFunc // ends the context the provider started it in
	sent    chan struct{}      // closed once its last event is sent
	tmp     string             // what it was handed as files: the gateway's config
	cleanup func()
}

func (s *server) start(_ context.Context, p agent.Provider, sp startParams) (any, error) {
	w := sp.Spec
	x := &served{srv: s, handle: sp.Session, sent: make(chan struct{})}
	// The session outlives the request that started it, until Oriel's context for it ends (session/stop).
	sctx, stop := context.WithCancel(context.Background())
	x.stop = stop
	x.cleanup = func() {
		if x.tmp != "" {
			os.RemoveAll(x.tmp)
		}
	}
	s.mu.Lock()
	s.sessions[sp.Session] = x
	s.mu.Unlock()
	fail := func(err error) (any, error) {
		s.mu.Lock()
		delete(s.sessions, sp.Session)
		s.mu.Unlock()
		stop()
		x.cleanup()
		return nil, err
	}
	spec := agent.StartSpec{Cwd: w.Cwd, Model: w.Model, Effort: w.Effort, Prompt: w.Prompt, Policy: w.Policy, Mode: w.Mode, Protect: w.Protect,
		Env: w.Env, StateDir: w.StateDir, Memory: w.Memory, NoMCP: w.NoMCP, Exec: remoteExec{srv: s, session: sp.Session},
		Pack: agent.Pack{Name: w.Pack.Name, Skill: w.Pack.Skill, Text: w.Pack.Text, Tools: w.Pack.Tools}}
	for _, t := range w.Tools {
		spec.Tools = append(spec.Tools, agent.Tool{Name: t.Name, Description: t.Description, Schema: t.Schema, ReadOnly: t.ReadOnly,
			Call: x.callTool(t.Name)})
	}
	if sb := w.Sandbox; sb != nil {
		spec.Sandbox = &agent.Sandbox{Name: sb.Name, HookSocket: sb.HookSocket, GatewayMCP: sb.GatewayMCP}
		if len(sb.Gateway) > 0 {
			dir, err := os.MkdirTemp("", "oriel-agent-")
			if err != nil {
				return fail(err)
			}
			x.tmp = dir
			path := filepath.Join(dir, "gateway.json")
			if err := os.WriteFile(path, sb.Gateway, 0o600); err != nil {
				return fail(err)
			}
			spec.Sandbox.GatewayMCP = path
		}
	}
	var sess agent.Session
	var err error
	if sp.Resume != "" {
		sess, err = p.Resume(sctx, sp.Resume, spec)
	} else {
		sess, err = p.Start(sctx, spec)
	}
	if err != nil {
		return fail(err)
	}
	x.s = sess
	go x.forward()
	r := startResult{ID: sess.ID()}
	if t := sess.Terminal(); t != nil {
		r.Terminal = true
		go x.watchTerminal(t)
	}
	return r, nil
}

// forward sends the session's events on until they end.
func (x *served) forward() {
	defer close(x.sent)
	for ev := range x.s.Events() {
		w, err := eventOf(ev)
		if err != nil {
			continue
		}
		x.srv.peer.Notify("session/event", eventParams{Session: x.handle, Event: w})
	}
	x.srv.mu.Lock()
	_, open := x.srv.sessions[x.handle]
	x.srv.mu.Unlock()
	if open {
		x.srv.peer.Notify("session/closed", sessionParams{Session: x.handle})
	}
}

func (x *served) watchTerminal(t agent.Terminal) {
	backlog, live, detach := t.Attach()
	defer detach()
	if len(backlog) > 0 {
		x.srv.peer.Notify("terminal/output", sessionParams{Session: x.handle, Data: b64(backlog)})
	}
	for {
		select {
		case b, ok := <-live:
			if !ok {
				return
			}
			x.srv.peer.Notify("terminal/output", sessionParams{Session: x.handle, Data: b64(b)})
		case <-x.sent:
			return
		}
	}
}

func (x *served) callTool(name string) func(context.Context, json.RawMessage) (string, error) {
	return func(ctx context.Context, args json.RawMessage) (string, error) {
		var r toolResult
		err := callOn(ctx, x.srv.peer, "session/tool", toolParams{Session: x.handle, Name: name, Arguments: args}, &r)
		return r.Output, err
	}
}

func (x *served) request(ctx context.Context, method string, sp sessionParams) (any, error) {
	ok := struct{}{}
	switch method {
	case "session/send":
		var in agent.Input
		if sp.Input != nil {
			in = agent.Input{Text: sp.Input.Text, Images: sp.Input.Images, Urgent: sp.Input.Urgent}
		}
		return ok, x.s.Send(ctx, in)
	case "session/interrupt":
		return ok, x.s.Interrupt(ctx)
	case "session/fork":
		a, err := x.s.Fork(ctx, agent.ForkSpec{Prompt: sp.Prompt})
		return forkResult{Answer: a}, err
	case "session/answer":
		var d agent.Decision
		if sp.Decision != nil {
			d = agent.Decision{Allow: sp.Decision.Allow, Always: sp.Decision.Always, Note: sp.Decision.Note, Answers: sp.Decision.Answers}
		}
		return ok, x.s.Answer(ctx, sp.ID, d)
	case "session/stopProcess":
		ps, can := x.s.(agent.ProcessStopper)
		if !can {
			return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "the session doesn't stop processes"}
		}
		return ok, ps.StopProcess(ctx, sp.Process)
	case "terminal/write", "terminal/resize":
		t := x.s.Terminal()
		if t == nil {
			return nil, errors.New("the session has no terminal")
		}
		if method == "terminal/resize" {
			return ok, t.Resize(sp.Cols, sp.Rows)
		}
		_, err := t.Write(unb64(sp.Data))
		return ok, err
	case "session/close":
		x.srv.mu.Lock()
		delete(x.srv.sessions, x.handle)
		x.srv.mu.Unlock()
		err := x.s.Close()
		select {
		case <-x.sent:
		case <-time.After(drainTimeout):
		}
		x.stop()
		x.cleanup()
		return ok, err
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
}

// remoteExec is a session's agent.Exec on the plugin's side: Oriel runs the process, through the session's own.
type remoteExec struct {
	srv     *server
	session string
}

func (e remoteExec) StartPTY(ctx context.Context, cmd *exec.Cmd, cols, rows int) (agent.PTY, error) {
	pr, err := e.start(ctx, cmd, &size{Cols: cols, Rows: rows})
	if err != nil {
		return nil, err
	}
	return remotePTY{pr}, nil
}

func (e remoteExec) Start(ctx context.Context, cmd *exec.Cmd) (agent.Process, error) {
	pr, err := e.start(ctx, cmd, nil)
	if err != nil {
		return nil, err
	}
	return remoteProcess{pr}, nil
}

func (e remoteExec) start(ctx context.Context, cmd *exec.Cmd, pty *size) (*remote, error) {
	s := e.srv
	s.mu.Lock()
	s.seq++
	id := "p" + strconv.Itoa(s.seq)
	pr := &remote{srv: s, handle: id, out: newStream(), done: make(chan struct{}), stderr: cmd.Stderr}
	if s.procs != nil {
		s.procs[id] = pr
	}
	s.mu.Unlock()
	p := execParams{Session: e.session, Process: id, Path: cmd.Path, Args: cmd.Args, Dir: cmd.Dir, Env: cmd.Env, PTY: pty,
		Stderr: cmd.Stderr != nil}
	if len(p.Args) == 0 {
		p.Args = []string{cmd.Path}
	}
	if err := callOn(ctx, s.peer, "exec/start", p, nil); err != nil {
		s.mu.Lock()
		delete(s.procs, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("%s: %w", filepath.Base(p.Args[0]), err)
	}
	go func() {
		select {
		case <-ctx.Done():
			pr.Kill()
		case <-pr.done:
		}
	}()
	return pr, nil
}

// remote is a process Oriel runs for the plugin.
type remote struct {
	srv    *server
	handle string
	out    *stream
	stderr io.Writer
	done   chan struct{}
	once   sync.Once
	err    error
}

func (r *remote) output(stream string, b []byte) {
	if stream == "stderr" {
		if r.stderr != nil {
			r.stderr.Write(b)
		}
		return
	}
	r.out.Write(b)
}

func (r *remote) exited(x exit) {
	r.once.Do(func() {
		if x.Error != "" {
			r.err = &ExitError{Code: x.Code, Msg: x.Error}
		}
		r.out.finish()
		close(r.done)
	})
}

func (r *remote) call(method string, p execParams) error {
	p.Process = r.handle
	select {
	case <-r.done:
		return io.ErrClosedPipe
	default:
	}
	return callOn(context.Background(), r.srv.peer, method, p, nil)
}

func (r *remote) Wait() error {
	<-r.done
	return r.err
}

func (r *remote) Kill() error {
	select {
	case <-r.done:
		return nil
	default:
	}
	return r.call("exec/kill", execParams{})
}

// ExitError is how a process Oriel ran for the plugin ended.
type ExitError struct {
	Code int // its exit code; 0 when it was signalled or never ran
	Msg  string
}

func (e *ExitError) Error() string { return e.Msg }

// ExitCode is the process's exit code, as exec.ExitError says it.
func (e *ExitError) ExitCode() int { return e.Code }

type remoteProcess struct{ *remote }

func (p remoteProcess) Stdin() io.WriteCloser { return stdin{p.remote} }
func (p remoteProcess) Stdout() io.ReadCloser { return p.out }

type stdin struct{ r *remote }

func (s stdin) Write(b []byte) (int, error) {
	if err := s.r.call("exec/write", execParams{Data: b64(b)}); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (s stdin) Close() error { return s.r.call("exec/eof", execParams{}) }

type remotePTY struct{ *remote }

func (p remotePTY) Read(b []byte) (int, error) { return p.out.Read(b) }

func (p remotePTY) Write(b []byte) (int, error) {
	if err := p.call("exec/write", execParams{Data: b64(b)}); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (p remotePTY) Resize(cols, rows int) error {
	return p.call("exec/resize", execParams{Cols: cols, Rows: rows})
}

func (p remotePTY) Close() error {
	p.Kill()
	return p.out.Close()
}
