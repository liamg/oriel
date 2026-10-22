package rpcagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/jsonrpc"
)

// Options make a Client.
type Options struct {
	Name   string          // its ID, which Oriel registers it by; the plugin's own if ""
	Config json.RawMessage // handed to the plugin in initialize
	Dial   Dialer
	// Assume is what it's taken to do until the plugin has said, in initialize.
	Assume agent.Features
	// Logged and Discovered say it answers logs and discover: they decide which of agent.Logged and
	// agent.Discovered the provider New makes is, before the plugin is running.
	Logged, Discovered bool
	// Allow checks a program the plugin runs outside a sandbox, by its path; nil allows any. Inside one the
	// sandbox holds it.
	Allow func(path string) error
	Self  string // the binary that serves `oriel hook mcp` on the host; os.Executable if ""
}

// callTimeout bounds the calls with no context of their own: MCPServers, Logs, Discover.
const callTimeout = 10 * time.Second

// Client is a plugin's agent provider as Oriel sees it: every call crosses to the plugin, which it starts on first
// use (Options.Dial) and again after it stops.
type Client struct {
	o Options

	mu       sync.Mutex
	peer     Peer
	hello    Hello
	dialing  chan struct{} // closed once a dial under way ends
	seq      int
	sessions map[string]*session
	procs    map[string]*proc
	logins   map[string]chan error
	places   map[string]agent.SandboxPlace
}

// New is a Client that starts the plugin on first use: a provider that's agent.Logged and agent.Discovered as the
// options say.
func New(o Options) agent.Provider {
	return wrap(newClient(o), o.Logged, o.Discovered)
}

// Dial connects to the plugin now, and is the provider it says it is.
func Dial(ctx context.Context, o Options) (agent.Provider, error) {
	c := newClient(o)
	if _, err := c.connect(ctx); err != nil {
		return nil, err
	}
	h := c.said()
	return wrap(c, h.Logged, h.Discovered), nil
}

func newClient(o Options) *Client {
	return &Client{o: o, sessions: map[string]*session{}, procs: map[string]*proc{}, logins: map[string]chan error{},
		places: map[string]agent.SandboxPlace{}}
}

func wrap(c *Client, logged, discovered bool) agent.Provider {
	switch {
	case logged && discovered:
		return loggedDiscovered{c}
	case logged:
		return logged_{c}
	case discovered:
		return discovered_{c}
	}
	return c
}

type logged_ struct{ *Client }
type discovered_ struct{ *Client }
type loggedDiscovered struct{ *Client }

func (c logged_) Logs(cwd, session string) []agent.Log          { return c.logs(cwd, session) }
func (c loggedDiscovered) Logs(cwd, session string) []agent.Log { return c.logs(cwd, session) }
func (c discovered_) Discover() []agent.Transcript              { return c.discover() }
func (c loggedDiscovered) Discover() []agent.Transcript         { return c.discover() }

var (
	_ agent.Featured     = (*Client)(nil)
	_ agent.LoginStarter = (*Client)(nil)
	_ agent.Sandboxed    = (*Client)(nil)
	_ agent.Logged       = loggedDiscovered{}
	_ agent.Discovered   = loggedDiscovered{}
)

func (c *Client) next(kind string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seq++
	return kind + strconv.Itoa(c.seq)
}

func (c *Client) said() Hello {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hello
}

// connect is the connection to the plugin, dialled and initialized if there's none.
func (c *Client) connect(ctx context.Context) (Peer, error) {
	for {
		c.mu.Lock()
		if c.peer != nil {
			select {
			case <-c.peer.Done():
				c.peer = nil
			default:
				p := c.peer
				c.mu.Unlock()
				return p, nil
			}
		}
		if wait := c.dialing; wait != nil {
			c.mu.Unlock()
			select {
			case <-wait:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		done := make(chan struct{})
		c.dialing = done
		c.mu.Unlock()
		p, h, err := c.dial(ctx)
		c.mu.Lock()
		c.dialing = nil
		if err == nil {
			c.peer, c.hello = p, h
		}
		c.mu.Unlock()
		close(done)
		if err != nil {
			return nil, err
		}
		go c.watch(p)
		return p, nil
	}
}

func (c *Client) dial(ctx context.Context) (Peer, Hello, error) {
	if c.o.Dial == nil {
		return nil, Hello{}, errors.New("rpcagent: nothing to dial")
	}
	p, err := c.o.Dial(ctx, Handler{Request: c.request, Notify: c.notify})
	if err != nil {
		return nil, Hello{}, err
	}
	var h Hello
	raw, err := p.Call(ctx, "initialize", Init{ProtocolVersion: Version, Name: c.o.Name, Config: c.o.Config})
	if err == nil {
		err = json.Unmarshal(raw, &h)
	}
	if err == nil && h.ProtocolVersion != Version {
		err = fmt.Errorf("the plugin speaks version %d of the agent protocol, not %d", h.ProtocolVersion, Version)
	}
	if err != nil {
		p.Close()
		return nil, Hello{}, fmt.Errorf("%s: initialize: %w", c.name(), localError(err))
	}
	return p, h, nil
}

func (c *Client) name() string {
	if c.o.Name != "" {
		return c.o.Name
	}
	return "agent plugin"
}

// watch ends what lived on p once it stops: its sessions exit, its processes are killed, its logins fail.
func (c *Client) watch(p Peer) {
	<-p.Done()
	c.mu.Lock()
	var ss []*session
	for k, s := range c.sessions {
		if s.peer == p {
			ss = append(ss, s)
			delete(c.sessions, k)
		}
	}
	var ps []*proc
	for k, pr := range c.procs {
		if pr.peer == p {
			ps = append(ps, pr)
			delete(c.procs, k)
		}
	}
	logins := c.logins
	c.logins = map[string]chan error{}
	c.mu.Unlock()
	for _, s := range ss {
		s.gone("the agent plugin stopped")
	}
	for _, pr := range ps {
		pr.kill()
	}
	for _, ch := range logins {
		ch <- errors.New("the agent plugin stopped")
	}
}

// Close stops the plugin's connection.
func (c *Client) Close() error {
	c.mu.Lock()
	p := c.peer
	c.peer = nil
	c.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.Close()
}

// call makes a call on the plugin, starting it if it isn't running, and decodes its result into out.
func (c *Client) call(ctx context.Context, method string, params, out any) error {
	p, err := c.connect(ctx)
	if err != nil {
		return err
	}
	return callOn(ctx, p, method, params, out)
}

func callOn(ctx context.Context, p Peer, method string, params, out any) error {
	raw, err := p.Call(ctx, method, params)
	if err != nil {
		return localError(err)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) ID() string {
	if c.o.Name != "" {
		return c.o.Name
	}
	return c.said().ID
}

// Features are what the plugin said in initialize; Options.Assume until it has.
func (c *Client) Features() agent.Features {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peer == nil {
		return c.o.Assume
	}
	return c.hello.Features.Agent()
}

func (c *Client) Detect(ctx context.Context) (agent.Install, error) {
	var w install
	err := c.call(ctx, "detect", struct{}{}, &w)
	return w.install(), err
}

func (c *Client) Models(ctx context.Context) ([]agent.Model, error) {
	var out []agent.Model
	err := c.call(ctx, "models", struct{}{}, &out)
	return out, err
}

func (c *Client) Usage(ctx context.Context) (core.ProviderUsage, error) {
	var u core.ProviderUsage
	err := c.call(ctx, "usage", struct{}{}, &u)
	if err == nil {
		u.Provider = c.ID()
	}
	var pf probeFailed
	if errors.As(err, &pf) {
		pf.provider = c.ID()
		err = pf
	}
	return u, err
}

func (c *Client) SandboxNeeds(ctx context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	call := c.next("n")
	c.mu.Lock()
	c.places[call] = at
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.places, call)
		c.mu.Unlock()
	}()
	var w needs
	err := c.call(ctx, "sandbox/needs", placeOf(call, at), &w)
	return w.needs(), err
}

func (c *Client) MCPServers() []agent.MCPServer {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var ws []mcpServer
	if c.call(ctx, "sandbox/mcpServers", struct{}{}, &ws) != nil {
		return nil
	}
	var out []agent.MCPServer
	for _, w := range ws {
		out = append(out, agent.MCPServer{Name: w.Name, Source: w.Source, Config: w.Config, Vars: w.Vars})
	}
	return out
}

func (c *Client) StartLogin(ctx context.Context) (agent.Login, error) {
	id := c.next("l")
	done := make(chan error, 1)
	c.mu.Lock()
	c.logins[id] = done
	c.mu.Unlock()
	var r loginParams
	if err := c.call(ctx, "login/start", loginParams{Login: id}, &r); err != nil {
		c.mu.Lock()
		delete(c.logins, id)
		c.mu.Unlock()
		return agent.Login{}, err
	}
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		_, waiting := c.logins[id]
		p := c.peer
		c.mu.Unlock()
		if waiting && p != nil {
			p.Notify("login/cancel", loginParams{Login: id})
		}
	}()
	return agent.Login{URL: r.URL, Done: done}, nil
}

func (c *Client) logs(cwd, sess string) []agent.Log {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var ls []log
	if c.call(ctx, "logs", logsParams{Cwd: cwd, Session: sess}, &ls) != nil {
		return nil
	}
	return agentLogs(ls)
}

func (c *Client) discover() []agent.Transcript {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var ts []transcript
	if c.call(ctx, "discover", struct{}{}, &ts) != nil {
		return nil
	}
	var out []agent.Transcript
	for _, t := range ts {
		out = append(out, agent.Transcript{Session: t.Session, Logs: agentLogs(t.Logs), Modified: t.Modified})
	}
	return out
}

// notify handles the plugin's notifications, in the order it sent them.
func (c *Client) notify(method string, params json.RawMessage) {
	switch method {
	case "session/event":
		var e eventParams
		if json.Unmarshal(params, &e) != nil {
			return
		}
		if s := c.session(e.Session); s != nil {
			if ev, err := e.Event.agentEvent(); err == nil {
				s.event(ev)
			}
		}
	case "session/closed":
		var e sessionParams
		json.Unmarshal(params, &e)
		if s := c.session(e.Session); s != nil {
			c.forget(s)
			s.finish(true)
		}
	case "terminal/output":
		var e sessionParams
		json.Unmarshal(params, &e)
		if s := c.session(e.Session); s != nil {
			s.term.feed(unb64(e.Data))
		}
	case "login/done":
		var l loginParams
		json.Unmarshal(params, &l)
		c.mu.Lock()
		ch := c.logins[l.Login]
		delete(c.logins, l.Login)
		c.mu.Unlock()
		if ch != nil {
			if l.Error != "" {
				ch <- errors.New(l.Error)
			} else {
				ch <- nil
			}
		}
	case "sandbox/report":
		var r placeCall
		json.Unmarshal(params, &r)
		c.mu.Lock()
		at := c.places[r.Call]
		c.mu.Unlock()
		if at.Report != nil {
			at.Report(r.Step, r.Note)
		}
	}
}

// request answers the plugin's requests, its errors as they cross.
func (c *Client) request(ctx context.Context, method string, params json.RawMessage) (v any, err error) {
	defer func() {
		if r := recover(); r != nil {
			v, err = nil, fmt.Errorf("%s: %v", method, r)
		}
		err = WireError(err)
	}()
	return c.answer(ctx, method, params)
}

// answer answers the plugin's requests: its session's tools, its processes, a sandbox call's environment.
func (c *Client) answer(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "session/tool":
		var t toolParams
		if err := json.Unmarshal(params, &t); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
		s := c.session(t.Session)
		if s == nil {
			return nil, fmt.Errorf("no session %s", t.Session)
		}
		out, err := s.tool(ctx, t.Name, t.Arguments)
		if err != nil {
			return nil, err
		}
		return toolResult{Output: out}, nil
	case "sandbox/env":
		var r placeCall
		json.Unmarshal(params, &r)
		c.mu.Lock()
		at := c.places[r.Call]
		c.mu.Unlock()
		if at.Env == nil {
			return placeCall{Call: r.Call, Name: r.Name}, nil
		}
		return placeCall{Call: r.Call, Name: r.Name, Value: at.Env(r.Name)}, nil
	case "exec/start", "exec/write", "exec/eof", "exec/resize", "exec/kill":
		var e execParams
		if err := json.Unmarshal(params, &e); err != nil {
			return nil, &jsonrpc.Error{Code: jsonrpc.InvalidParams, Message: err.Error()}
		}
		return struct{}{}, c.exec(method, e)
	}
	return nil, &jsonrpc.Error{Code: jsonrpc.MethodNotFound, Message: "no method " + method}
}

func (c *Client) session(handle string) *session {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessions[handle]
}

func (c *Client) forget(s *session) {
	c.mu.Lock()
	if c.sessions[s.handle] == s {
		delete(c.sessions, s.handle)
	}
	c.mu.Unlock()
}

func (c *Client) self() string {
	if c.o.Self != "" {
		return c.o.Self
	}
	exe, err := os.Executable()
	if err != nil {
		return "oriel"
	}
	return exe
}
