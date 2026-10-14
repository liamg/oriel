package agentsock

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox/egress"
)

// SSHMax is the longest a grant lasts.
const SSHMax = 5 * time.Minute

// SSHAgent lets a sandbox reach the host's ssh-agent only while the user has
// granted it: the keys never leave the host, and when the grant runs out
// every open connection is cut.
type SSHAgent struct {
	// Socket is the host's agent; "" is $SSH_AUTH_SOCK.
	Socket string
	// Changed is told when a grant opens or closes.
	Changed func()

	mu    sync.Mutex
	until time.Time
	// refused is the last time something in the sandbox asked for the agent
	// with no grant open.
	refused time.Time
	conns   map[net.Conn]bool
	timer   *time.Timer
}

func (s *SSHAgent) socket() string {
	if s.Socket != "" {
		return s.Socket
	}
	return os.Getenv("SSH_AUTH_SOCK")
}

// Grant opens the agent to the sandbox for d, capped at SSHMax, and says
// until when.
func (s *SSHAgent) Grant(d time.Duration) (time.Time, error) {
	sock := s.socket()
	if sock == "" {
		return time.Time{}, errors.New("there is no ssh-agent on the host (SSH_AUTH_SOCK is not set where Oriel runs)")
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		return time.Time{}, errors.New("the host's ssh-agent is not answering: " + err.Error())
	}
	_ = c.Close()
	if d <= 0 || d > SSHMax {
		d = SSHMax
	}
	s.mu.Lock()
	s.until = time.Now().Add(d)
	if s.timer != nil {
		s.timer.Stop()
	}
	s.timer = time.AfterFunc(d, s.Revoke)
	until := s.until
	s.mu.Unlock()
	s.changed()
	return until, nil
}

// Revoke ends the grant and cuts every connection it let through.
func (s *SSHAgent) Revoke() {
	s.mu.Lock()
	s.until = time.Time{}
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.changed()
}

func (s *SSHAgent) changed() {
	if s.Changed != nil {
		s.Changed()
	}
}

// Live reports whether a grant is open.
func (s *SSHAgent) Live() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().Before(s.until)
}

// Until is when the current grant ends, zero when there is none.
func (s *SSHAgent) Until() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Now().After(s.until) {
		return time.Time{}
	}
	return s.until
}

// Refused is the last time the sandbox asked with no grant open.
func (s *SSHAgent) Refused() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refused
}

// Serve accepts the sandbox's connections on the unix socket at path until
// ctx ends.
func (s *SSHAgent) Serve(ctx context.Context, path string) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	context.AfterFunc(ctx, func() {
		_ = ln.Close()
		_ = os.Remove(path)
	})
	for {
		c, err := ln.Accept()
		if err != nil {
			return nil
		}
		go s.Forward(c)
	}
}

// ServeUpgrade carries an ssh-agent connection the egress proxy relays,
// where the socket cannot be mounted.
func (s *SSHAgent) ServeUpgrade(w http.ResponseWriter, r *http.Request) {
	if !egress.IsUpgrade(r) {
		http.Error(w, "not an upgrade request", http.StatusBadRequest)
		return
	}
	c, err := egress.Hijack(w)
	if err != nil {
		return
	}
	s.Forward(c)
}

// Forward passes c to the host's agent while a grant is live, and closes it
// otherwise.
func (s *SSHAgent) Forward(c net.Conn) {
	if !s.Live() {
		s.mu.Lock()
		s.refused = time.Now()
		s.mu.Unlock()
		_ = c.Close()
		return
	}
	up, err := net.Dial("unix", s.socket())
	if err != nil {
		_ = c.Close()
		return
	}
	s.mu.Lock()
	if s.conns == nil {
		s.conns = map[net.Conn]bool{}
	}
	s.conns[c], s.conns[up] = true, true
	s.mu.Unlock()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	<-done
	_ = c.Close()
	_ = up.Close()
	s.mu.Lock()
	delete(s.conns, c)
	delete(s.conns, up)
	s.mu.Unlock()
}
