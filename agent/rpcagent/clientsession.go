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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/mcptools"
	"github.com/liamg/oriel/jsonrpc"
)

// closeTimeout bounds how long Close waits for the plugin to close a session.
const closeTimeout = 10 * time.Second

func (c *Client) Start(ctx context.Context, spec agent.StartSpec) (agent.Session, error) {
	return c.start(ctx, "", spec)
}

func (c *Client) Resume(ctx context.Context, id string, spec agent.StartSpec) (agent.Session, error) {
	return c.start(ctx, id, spec)
}

func (c *Client) start(ctx context.Context, resume string, spec agent.StartSpec) (agent.Session, error) {
	p, err := c.connect(ctx)
	if err != nil {
		return nil, err
	}
	s := &session{c: c, peer: p, handle: c.next("s"), spec: spec, id: resume, events: newEvents(), term: newTerminal(),
		done: make(chan struct{})}
	s.term.write = func(b []byte) error {
		return s.call(context.Background(), "terminal/write", sessionParams{Data: b64(b)}, nil)
	}
	s.term.resize = func(cols, rows int) error {
		return s.call(context.Background(), "terminal/resize", sessionParams{Cols: cols, Rows: rows}, nil)
	}
	s.exec = spec.Exec
	if s.exec == nil {
		s.exec = hostexec.Exec{}
	}
	w := startSpec{Cwd: spec.Cwd, Model: spec.Model, Effort: spec.Effort, Prompt: spec.Prompt, Policy: spec.Policy,
		Protect: spec.Protect, Env: spec.Env, StateDir: spec.StateDir, Memory: spec.Memory, NoMCP: spec.NoMCP, Tools: []tool{},
		Pack: pack{Name: spec.Pack.Name, Skill: spec.Pack.Skill, Text: spec.Pack.Text, Tools: spec.Pack.Tools}}
	for _, t := range spec.Tools {
		w.Tools = append(w.Tools, tool{Name: t.Name, Description: t.Description, Schema: t.Schema, ReadOnly: t.ReadOnly})
	}
	if sb := spec.Sandbox; sb != nil {
		w.Sandbox = &sandbox{Name: sb.Name, HookSocket: sb.HookSocket, GatewayMCP: sb.GatewayMCP}
		if sb.GatewayMCP != "" {
			b, err := os.ReadFile(sb.GatewayMCP)
			if err != nil {
				return nil, fmt.Errorf("the gateway's MCP config: %w", err)
			}
			w.Sandbox.Gateway = b
		}
	}
	if c.said().MCP {
		if s.tools, err = mcptools.Serve(spec.Tools); err != nil {
			return nil, fmt.Errorf("serving Oriel's tools: %w", err)
		}
		e := mcptools.Entry(s.tools.Path(), c.self(), spec.Sandbox)
		w.MCP = &stdioServer{Name: e.Name, Command: e.Command, Args: e.Args, Env: e.Env, Socket: s.tools.Path()}
	}
	c.mu.Lock()
	c.sessions[s.handle] = s
	c.mu.Unlock()
	// The plugin's session lives as long as the context it was started in, as a provider's own does.
	go func() {
		select {
		case <-ctx.Done():
			p.Notify("session/stop", sessionParams{Session: s.handle})
		case <-s.done:
		}
	}()
	var r startResult
	if err := callOn(ctx, p, "session/start", startParams{Session: s.handle, Resume: resume, Spec: w}, &r); err != nil {
		c.forget(s)
		s.finish(false)
		return nil, err
	}
	s.mu.Lock()
	s.id = r.ID
	s.terminal = r.Terminal
	s.mu.Unlock()
	return s, nil
}

// session is a session the plugin runs, as Oriel sees it.
type session struct {
	c      *Client
	peer   Peer
	handle string
	spec   agent.StartSpec
	exec   agent.Exec
	tools  *mcptools.Server
	events *events
	term   *terminal
	done   chan struct{} // closed once it's over

	mu       sync.Mutex
	id       string
	terminal bool
	over     bool
}

func (s *session) call(ctx context.Context, method string, p sessionParams, out any) error {
	p.Session = s.handle
	return callOn(ctx, s.peer, method, p, out)
}

func (s *session) ID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

func (s *session) Send(ctx context.Context, in agent.Input) error {
	return s.call(ctx, "session/send", sessionParams{Input: &input{Text: in.Text, Images: in.Images, Urgent: in.Urgent}}, nil)
}

func (s *session) Interrupt(ctx context.Context) error {
	return s.call(ctx, "session/interrupt", sessionParams{}, nil)
}

func (s *session) Fork(ctx context.Context, f agent.ForkSpec) (string, error) {
	var r forkResult
	err := s.call(ctx, "session/fork", sessionParams{Prompt: f.Prompt}, &r)
	return r.Answer, err
}

func (s *session) Answer(ctx context.Context, id string, d agent.Decision) error {
	return s.call(ctx, "session/answer", sessionParams{ID: id, Decision: &decision{Allow: d.Allow, Always: d.Always, Note: d.Note,
		Answers: d.Answers}}, nil)
}

// StopProcess stops a process a turn left running; a plugin whose sessions can't answers MethodNotFound.
func (s *session) StopProcess(ctx context.Context, id string) error {
	return s.call(ctx, "session/stopProcess", sessionParams{Process: id}, nil)
}

func (s *session) Events() <-chan agent.Event { return s.events.ch }

func (s *session) Terminal() agent.Terminal {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.terminal {
		return nil
	}
	return s.term
}

func (s *session) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), closeTimeout)
	defer cancel()
	err := s.call(ctx, "session/close", sessionParams{}, nil)
	if errors.Is(err, jsonrpc.ErrClosed) {
		err = nil
	}
	s.c.forget(s)
	s.finish(false)
	return err
}

// event passes on what the plugin's session said, its provider named as Oriel names it.
func (s *session) event(ev agent.Event) {
	id := s.c.ID()
	switch e := ev.(type) {
	case agent.Ready:
		s.mu.Lock()
		s.id = e.ID
		s.mu.Unlock()
	case agent.AuthExpired:
		e.Provider = id
		ev = e
	case agent.AuthRestored:
		e.Provider = id
		ev = e
	case agent.UsageUpdate:
		e.Usage.Provider = id
		ev = e
	}
	s.events.push(ev)
}

// gone is the plugin stopping under the session.
func (s *session) gone(why string) {
	s.events.push(agent.Exited{Err: why})
	s.finish(true)
}

// finish ends the session here: its events close (once taken, if drain), its tools stop, its processes die.
func (s *session) finish(drain bool) {
	s.mu.Lock()
	if s.over {
		s.mu.Unlock()
		return
	}
	s.over = true
	s.mu.Unlock()
	close(s.done)
	if drain {
		s.events.end()
	} else {
		s.events.drop()
	}
	s.term.close()
	s.tools.Close()
	s.c.mu.Lock()
	var ps []*proc
	for k, p := range s.c.procs {
		if p.session == s {
			ps = append(ps, p)
			delete(s.c.procs, k)
		}
	}
	s.c.mu.Unlock()
	for _, p := range ps {
		p.kill()
	}
}

func (s *session) tool(ctx context.Context, name string, args json.RawMessage) (string, error) {
	i := slices.IndexFunc(s.spec.Tools, func(t agent.Tool) bool { return t.Name == name })
	if i < 0 || s.spec.Tools[i].Call == nil {
		return "", fmt.Errorf("no such tool: %s", name)
	}
	if len(args) == 0 {
		args = json.RawMessage("{}")
	}
	return s.spec.Tools[i].Call(ctx, args)
}

// proc is a process Oriel runs for a plugin's session, through the session's Exec.
type proc struct {
	session *session
	peer    Peer
	handle  string
	pty     agent.PTY
	p       agent.Process
	stop    context.CancelFunc
}

func (p *proc) kill() {
	p.stop()
	if p.pty != nil {
		p.pty.Close()
	} else if p.p != nil {
		p.p.Kill()
	}
}

// exec does what the plugin asks of one of its processes.
func (c *Client) exec(method string, e execParams) error {
	c.mu.Lock()
	pr := c.procs[e.Process]
	c.mu.Unlock()
	if method == "exec/start" {
		if pr != nil {
			return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "process " + e.Process + " is running"}
		}
		return c.startProc(e)
	}
	if pr == nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no process " + e.Process}
	}
	switch method {
	case "exec/write":
		var err error
		if pr.pty != nil {
			_, err = pr.pty.Write(unb64(e.Data))
		} else {
			_, err = pr.p.Stdin().Write(unb64(e.Data))
		}
		return err
	case "exec/eof":
		if pr.p != nil {
			return pr.p.Stdin().Close()
		}
		return nil
	case "exec/resize":
		if pr.pty != nil {
			return pr.pty.Resize(e.Cols, e.Rows)
		}
		return nil
	}
	pr.kill()
	return nil
}

func (c *Client) startProc(e execParams) error {
	s := c.session(e.Session)
	if s == nil {
		return &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: "no session " + e.Session}
	}
	if len(e.Args) == 0 {
		return &jsonrpc.Error{Code: CodeExec, Message: "no command"}
	}
	var cmd *exec.Cmd
	if filepath.IsAbs(e.Path) {
		cmd = &exec.Cmd{Path: e.Path, Args: e.Args}
	} else {
		cmd = exec.Command(e.Args[0], e.Args[1:]...)
	}
	if s.spec.Sandbox == nil && c.o.Allow != nil {
		path := cmd.Path
		if cmd.Err != nil {
			return &jsonrpc.Error{Code: CodeExec, Message: cmd.Err.Error()}
		}
		if err := c.o.Allow(path); err != nil {
			return &jsonrpc.Error{Code: CodeExec, Message: err.Error()}
		}
	}
	cmd.Dir, cmd.Env = e.Dir, e.Env
	if s.tools != nil && !slices.ContainsFunc(cmd.Env, func(kv string) bool { return strings.HasPrefix(kv, mcptools.SockEnv+"=") }) {
		if cmd.Env == nil {
			cmd.Env = os.Environ()
		}
		cmd.Env = append(cmd.Env, mcptools.SockEnv+"="+s.tools.Path())
	}
	ctx, stop := context.WithCancel(context.Background())
	pr := &proc{session: s, peer: s.peer, handle: e.Process, stop: stop}
	out := func(stream string, b []byte) {
		pr.peer.Notify("exec/output", output{Process: e.Process, Stream: stream, Data: b64(b)})
	}
	var src io.Reader
	var wait func() error
	if e.PTY != nil {
		t, err := s.exec.StartPTY(ctx, cmd, e.PTY.Cols, e.PTY.Rows)
		if err != nil {
			stop()
			return &jsonrpc.Error{Code: CodeExec, Message: err.Error()}
		}
		pr.pty, src, wait = t, t, t.Wait
	} else {
		if e.Stderr {
			cmd.Stderr = writerFunc(func(b []byte) { out("stderr", append([]byte(nil), b...)) })
		}
		p, err := s.exec.Start(ctx, cmd)
		if err != nil {
			stop()
			return &jsonrpc.Error{Code: CodeExec, Message: err.Error()}
		}
		pr.p, src, wait = p, p.Stdout(), p.Wait
	}
	stream := "stdout"
	if pr.pty != nil {
		stream = "pty"
	}
	c.mu.Lock()
	c.procs[e.Process] = pr
	c.mu.Unlock()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := src.Read(buf)
			if n > 0 {
				out(stream, append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				break
			}
		}
		x := exit{Process: e.Process}
		if err := wait(); err != nil {
			x.Error = err.Error()
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				x.Code = ee.ExitCode()
			}
		}
		stop()
		c.mu.Lock()
		if c.procs[e.Process] == pr {
			delete(c.procs, e.Process)
		}
		c.mu.Unlock()
		pr.peer.Notify("exec/exit", x)
	}()
	return nil
}

type writerFunc func([]byte)

func (f writerFunc) Write(p []byte) (int, error) { f(p); return len(p), nil }

// backlogSize is how much of what a terminal showed a newly attached view gets.
const backlogSize = 256 << 10

// terminal is a plugin session's terminal as views watch it: what the plugin said it showed.
type terminal struct {
	write  func([]byte) error
	resize func(cols, rows int) error

	mu      sync.Mutex
	buf     []byte
	viewers map[chan []byte]struct{}
	closed  bool
}

func newTerminal() *terminal { return &terminal{viewers: map[chan []byte]struct{}{}} }

func (t *terminal) feed(p []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return
	}
	t.buf = append(t.buf, p...)
	if len(t.buf) > 2*backlogSize {
		t.buf = append([]byte(nil), t.buf[len(t.buf)-backlogSize:]...)
	}
	for ch := range t.viewers {
		select {
		case ch <- p:
		default:
		}
	}
}

func (t *terminal) Write(p []byte) (int, error) {
	if err := t.write(p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *terminal) Resize(cols, rows int) error { return t.resize(cols, rows) }

func (t *terminal) Attach() ([]byte, <-chan []byte, func()) {
	t.mu.Lock()
	defer t.mu.Unlock()
	backlog := t.buf
	if len(backlog) > backlogSize {
		backlog = backlog[len(backlog)-backlogSize:]
	}
	backlog = append([]byte(nil), backlog...)
	ch := make(chan []byte, 256)
	if t.closed {
		close(ch)
		return backlog, ch, func() {}
	}
	t.viewers[ch] = struct{}{}
	return backlog, ch, func() {
		t.mu.Lock()
		defer t.mu.Unlock()
		if _, ok := t.viewers[ch]; ok {
			delete(t.viewers, ch)
			close(ch)
		}
	}
}

func (t *terminal) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	for ch := range t.viewers {
		close(ch)
	}
	t.viewers = map[chan []byte]struct{}{}
}
