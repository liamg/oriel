package update

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/wire"
)

// What the app and a worker say to each other about updates. Workers never fetch a release themselves: the app
// offers one, and the worker pulls its platform's artifact back over the same connection.
const (
	MethodOffer    = "update.offer"    // app → worker: a Release; the worker fetches, verifies, runs and stages its artifact
	MethodArtifact = "update.artifact" // worker → app: a stream of an offered artifact's bytes, as Chunks
	MethodSwap     = "update.swap"     // app → worker: swap the staged version in, drain and restart onto it
	MethodStatus   = "update.status"   // app → worker: what it runs, what's staged, the last rollback
)

// chunkSize is how much of an artifact goes in one stream message.
const chunkSize = 256 << 10

// ArtifactReq asks for one artifact of an offered release.
type ArtifactReq struct {
	Version string `json:"version"`
	Name    string `json:"name"`
}

// Chunk is the next part of an artifact.
type Chunk struct {
	Data []byte `json:"data"`
}

// Status is a worker's side of an update.
type Status struct {
	Version    string    `json:"version"`
	Platform   string    `json:"platform"`
	Staged     string    `json:"staged,omitempty"`
	Pending    string    `json:"pending,omitempty"` // swapped in, health not yet confirmed
	RolledBack *Rollback `json:"rolledBack,omitempty"`
}

// Opener opens an artifact's bytes: a cached download, usually.
type Opener func(ctx context.Context, a Artifact) (io.ReadCloser, error)

// Publisher is the app's side: it offers releases to workers and serves them their artifacts.
type Publisher struct {
	Keys []ed25519.PublicKey

	mu      sync.Mutex
	offered map[string]offered // by version
}

type offered struct {
	m    Manifest
	open Opener
}

// Serve answers conn's worker when it asks for an offered artifact.
func (p *Publisher) Serve(conn wire.Conn) {
	conn.HandleStream(MethodArtifact, wire.StreamFunc(p.serveArtifact))
}

func (p *Publisher) serveArtifact(ctx context.Context, req ArtifactReq, send func(any) error) error {
	p.mu.Lock()
	o, ok := p.offered[req.Version]
	p.mu.Unlock()
	if !ok {
		return fmt.Errorf("%s was not offered", req.Version)
	}
	i := slices.IndexFunc(o.m.Artifacts, func(a Artifact) bool { return a.Name == req.Name })
	if i < 0 {
		return fmt.Errorf("%s has no artifact %s", req.Version, req.Name)
	}
	r, err := o.open(ctx, o.m.Artifacts[i])
	if err != nil {
		return err
	}
	defer r.Close()
	buf := make([]byte, chunkSize)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			if err := send(Chunk{Data: buf[:n]}); err != nil {
				return err
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Push offers rel to conn's worker and waits while it fetches, verifies and runs its artifact. Nothing on the
// worker changes until Swap.
func (p *Publisher) Push(ctx context.Context, conn wire.Conn, rel Release, open Opener) (Status, error) {
	m, err := rel.Verify(p.Keys)
	if err != nil {
		return Status{}, err
	}
	p.mu.Lock()
	if p.offered == nil {
		p.offered = map[string]offered{}
	}
	p.offered[m.Version] = offered{m: m, open: open}
	p.mu.Unlock()
	var st Status
	err = conn.Call(ctx, MethodOffer, rel, &st)
	return st, err
}

// Swap tells conn's worker to swap in what it staged. It drains and restarts, so conn ends soon after.
func Swap(ctx context.Context, conn wire.Conn) error { return conn.Call(ctx, MethodSwap, nil, nil) }

// StatusOf asks conn's worker for its Status.
func StatusOf(ctx context.Context, conn wire.Conn) (Status, error) {
	var st Status
	err := conn.Call(ctx, MethodStatus, nil, &st)
	return st, err
}

// Receiver is a worker's side: it takes releases the app offers, and swaps and restarts when told.
type Receiver struct {
	Installer *Installer
	Keys      []ed25519.PublicKey
	Version   string // the running version
	Platform  string // Platform() if ""
	Kind      string // KindBinary if ""
	// Restart drains the worker and re-execs it onto Installer.Executable(); it runs after a swap is answered.
	Restart func()

	mu sync.Mutex // one stage or swap at a time
}

// Serve answers the app on conn.
func (r *Receiver) Serve(conn wire.Conn) {
	conn.Handle(MethodOffer, wire.Func(func(ctx context.Context, rel Release) (Status, error) {
		if err := r.Accept(ctx, conn, rel); err != nil {
			return Status{}, err
		}
		return r.Status(), nil
	}))
	conn.Handle(MethodSwap, wire.Func(func(context.Context, struct{}) (any, error) {
		r.mu.Lock()
		defer r.mu.Unlock()
		if err := r.Installer.Swap(); err != nil {
			return nil, err
		}
		if r.Restart != nil {
			go func() {
				time.Sleep(100 * time.Millisecond) // the answer goes out first
				r.Restart()
			}()
		}
		return nil, nil
	}))
	conn.Handle(MethodStatus, wire.Func(func(context.Context, struct{}) (Status, error) { return r.Status(), nil }))
}

// Accept verifies rel, streams this platform's artifact from the app on conn, and stages it.
func (r *Receiver) Accept(ctx context.Context, conn wire.Conn, rel Release) error {
	m, err := rel.Verify(r.Keys)
	if err != nil {
		return err
	}
	if !Newer(m.Version, r.Version) {
		return fmt.Errorf("%s is not newer than %s", m.Version, r.Version)
	}
	a, ok := m.Artifact(r.platform(), r.kind())
	if !ok {
		return fmt.Errorf("%s has nothing for %s", m.Version, r.platform())
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, err := conn.Stream(ctx, MethodArtifact, ArtifactReq{Version: m.Version, Name: a.Name})
	if err != nil {
		return err
	}
	defer s.Close()
	return r.Installer.Stage(ctx, m, a, &chunks{s: s})
}

// Status is what this worker runs and has staged.
func (r *Receiver) Status() Status {
	st := Status{Version: r.Version, Platform: r.platform()}
	st.Staged, _ = r.Installer.Staged()
	if p, ok := r.Installer.Pending(); ok {
		st.Pending = p.Version
	}
	if rb, ok := r.Installer.Last(); ok {
		st.RolledBack = &rb
	}
	return st
}

func (r *Receiver) platform() string {
	if r.Platform == "" {
		return Platform()
	}
	return r.Platform
}

func (r *Receiver) kind() string {
	if r.Kind == "" {
		return KindBinary
	}
	return r.Kind
}

// chunks reads a stream of Chunks as bytes.
type chunks struct {
	s    wire.Stream
	rest []byte
}

func (c *chunks) Read(p []byte) (int, error) {
	for len(c.rest) == 0 {
		var ch Chunk
		if err := c.s.Recv(&ch); err != nil {
			if errors.Is(err, io.EOF) {
				return 0, io.EOF
			}
			return 0, err
		}
		c.rest = ch.Data
	}
	n := copy(p, c.rest)
	c.rest = c.rest[n:]
	return n, nil
}
