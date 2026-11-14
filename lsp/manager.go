package lsp

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/text"
)

// ErrNoServer is Open's answer for a language no installed server serves.
var ErrNoServer = errors.New("lsp: no server for the language")

// ManagerOpts are what a Manager needs from the app.
type ManagerOpts struct {
	// Servers lists the specs to choose from, read on every Open: the registry's lsp.server contributions.
	Servers func() []ServerSpec
	// Installed says whether a spec's server can run; nil looks its command up on PATH.
	Installed func(ServerSpec) bool
	// Stderr is where a server's stderr goes, such as a logs.Lines with the source "lsp:<name>"; nil drops it.
	Stderr func(spec ServerSpec, root string) io.Writer
	// StartTimeout bounds a server's start and initialization; 0 is a minute.
	StartTimeout time.Duration
	// Client has the rest: Launch, ReadFile and the callbacks, shared by every client.
	Client Options
}

// Manager starts servers as files open, one per server and project root, and routes each file's
// notifications and requests to its server. It is safe for concurrent use.
type Manager struct {
	opts ManagerOpts

	mu      sync.Mutex
	clients map[clientKey]*Client
	starts  map[clientKey]*start
	docs    map[string]*openDoc
	closed  bool // shut down: a server still starting is stopped as it comes up, and none starts after
}

type clientKey struct{ server, root string }

type start struct {
	done chan struct{}
	c    *Client
	err  error
}

type openDoc struct {
	key      clientKey
	language string
	snap     text.Snapshot
}

func NewManager(o ManagerOpts) *Manager {
	return &Manager{opts: o, clients: map[clientKey]*Client{}, starts: map[clientKey]*start{}, docs: map[string]*openDoc{}}
}

// ErrShutDown is a server asked for after the manager has shut down.
var ErrShutDown = errors.New("the language servers have shut down")

// SpecFor picks the server for a file of a language: the first by Order that serves it and is installed.
func (m *Manager) SpecFor(language string) (ServerSpec, bool) {
	if m.opts.Servers == nil {
		return ServerSpec{}, false
	}
	installed := m.opts.Installed
	if installed == nil {
		installed = Installed
	}
	specs := slices.Clone(m.opts.Servers())
	slices.SortStableFunc(specs, func(a, b ServerSpec) int { return a.Order - b.Order })
	for _, s := range specs {
		if s.Serves(language) && installed(s) {
			return s, true
		}
	}
	return ServerSpec{}, false
}

// Open opens a document on its server, starting the server for the file's root if it isn't running (or
// crashed). It blocks while a server starts. A document already open on a running server stays as it is.
func (m *Manager) Open(ctx context.Context, path, language string, s text.Snapshot) (*Client, error) {
	if c, ok := m.Client(path); ok && !exited(c) {
		return c, nil
	}
	spec, ok := m.SpecFor(language)
	if !ok {
		return nil, ErrNoServer
	}
	key := clientKey{spec.Name, spec.FindRoot(path)}
	c, err := m.client(ctx, spec, key)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.docs[path] = &openDoc{key: key, language: language, snap: s}
	m.mu.Unlock()
	return c, c.DidOpen(path, language, s)
}

// Warm starts the server for a language's files under root ahead of any of them opening, so the first request
// doesn't wait for it. It blocks while the server starts.
func (m *Manager) Warm(ctx context.Context, language, root string) (*Client, error) {
	spec, ok := m.SpecFor(language)
	if !ok {
		return nil, ErrNoServer
	}
	return m.client(ctx, spec, clientKey{spec.Name, spec.FindRoot(filepath.Join(root, "_"))})
}

// client returns the running client for key, starting one if there's none or it has exited.
func (m *Manager) client(ctx context.Context, spec ServerSpec, key clientKey) (*Client, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrShutDown
	}
	if c := m.clients[key]; c != nil && !exited(c) {
		m.mu.Unlock()
		return c, nil
	}
	st := m.starts[key]
	if st == nil {
		st = &start{done: make(chan struct{})}
		m.starts[key] = st
		m.mu.Unlock()
		go m.start(spec, key, st)
	} else {
		m.mu.Unlock()
	}
	select {
	case <-st.done:
		return st.c, st.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func exited(c *Client) bool {
	select {
	case <-c.Done():
		return true
	default:
		return false
	}
}

// start runs a server for key and reopens the documents that were open on its predecessor.
func (m *Manager) start(spec ServerSpec, key clientKey, st *start) {
	o := m.opts.Client
	if m.opts.Stderr != nil {
		o.Stderr = m.opts.Stderr(spec, key.root)
	}
	timeout := m.opts.StartTimeout
	if timeout == 0 {
		timeout = time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	c, err := Start(ctx, spec, key.root, o)
	cancel()
	m.mu.Lock()
	delete(m.starts, key)
	if m.closed && err == nil {
		m.mu.Unlock()
		sctx, scancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = c.Shutdown(sctx)
		scancel()
		c, err = nil, ErrShutDown
		st.c, st.err = c, err
		close(st.done)
		return
	}
	var reopen []string
	if err == nil {
		m.clients[key] = c
		for p, d := range m.docs {
			if d.key == key {
				reopen = append(reopen, p)
			}
		}
	}
	docs := map[string]openDoc{}
	for _, p := range reopen {
		docs[p] = *m.docs[p]
	}
	m.mu.Unlock()
	slices.Sort(reopen)
	for _, p := range reopen {
		c.DidOpen(p, docs[p].language, docs[p].snap)
	}
	st.c, st.err = c, err
	close(st.done)
}

// Client is the client a document is open on.
func (m *Manager) Client(path string) (*Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := m.docs[path]
	if d == nil {
		return nil, false
	}
	c := m.clients[d.key]
	return c, c != nil
}

// Change sends a buffer change to the document's server.
func (m *Manager) Change(path string, ch text.Change) error {
	m.mu.Lock()
	d := m.docs[path]
	if d != nil && ch.After.Version() > d.snap.Version() {
		d.snap = ch.After
	}
	m.mu.Unlock()
	c, ok := m.Client(path)
	if !ok {
		return ErrNotOpen
	}
	return c.DidChange(path, ch)
}

// Save tells the document's server it was saved.
func (m *Manager) Save(path string, s text.Snapshot) error {
	c, ok := m.Client(path)
	if !ok {
		return ErrNotOpen
	}
	return c.DidSave(path, s)
}

// Close closes a document on its server.
func (m *Manager) Close(path string) error {
	c, ok := m.Client(path)
	m.mu.Lock()
	delete(m.docs, path)
	m.mu.Unlock()
	if !ok {
		return ErrNotOpen
	}
	return c.DidClose(path)
}

// Clients are the clients started, running or not, by server and root.
func (m *Manager) Clients() []*Client {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*Client, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *Client) int {
		if c := strings.Compare(a.spec.Name, b.spec.Name); c != 0 {
			return c
		}
		return strings.Compare(a.root, b.root)
	})
	return out
}

// Statuses are every client's status, for the status line.
func (m *Manager) Statuses() []Status {
	var out []Status
	for _, c := range m.Clients() {
		out = append(out, c.Status())
	}
	return out
}

// Restart stops a client's server and starts it again with the same documents open.
func (m *Manager) Restart(ctx context.Context, c *Client) (*Client, error) {
	if !exited(c) {
		c.Shutdown(ctx)
	}
	return m.client(ctx, c.spec, clientKey{c.spec.Name, c.root})
}

// Shutdown stops every server.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	var errs []error
	for _, c := range m.Clients() {
		if !exited(c) {
			if err := c.Shutdown(ctx); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}
