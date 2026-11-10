package agentsock

import (
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/clock/clocktest"
)

// fakeAgent echoes whatever it is sent, standing in for ssh-agent.
func fakeAgent(t *testing.T) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "agent.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c); _ = c.Close() }()
		}
	}()
	return sock
}

func sshRoundTrip(t *testing.T, s *SSHAgent) bool {
	t.Helper()
	a, b := net.Pipe()
	go s.Forward(b)
	_ = a.SetDeadline(time.Now().Add(20 * time.Second)) // a hang guard: a refusal closes at once
	if _, err := a.Write([]byte("ping")); err != nil {
		return false
	}
	buf := make([]byte, 4)
	_, err := io.ReadFull(a, buf)
	_ = a.Close()
	return err == nil && string(buf) == "ping"
}

func TestSSHAgentOnlyWhileGranted(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", fakeAgent(t))
	s := &SSHAgent{}

	if sshRoundTrip(t, s) {
		t.Fatal("the agent answered with no grant")
	}
	if s.Refused().IsZero() {
		t.Error("a refused connection was not noted")
	}
	if _, err := s.Grant(time.Minute); err != nil {
		t.Fatal(err)
	}
	if !sshRoundTrip(t, s) {
		t.Fatal("the agent did not answer during the grant")
	}
	s.Revoke()
	if sshRoundTrip(t, s) {
		t.Fatal("the agent answered after the grant was revoked")
	}
}

func TestSSHGrantIsCapped(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", fakeAgent(t))
	clk := clocktest.New(time.Now())
	s := &SSHAgent{Clock: clk}
	until, err := s.Grant(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := until.Sub(clk.Now()); d != SSHMax {
		t.Errorf("grant lasts %s, want %s", d, SSHMax)
	}
	clk.Advance(SSHMax - time.Nanosecond)
	if !s.Live() {
		t.Error("the grant ended early")
	}
	clk.Advance(time.Nanosecond)
	if s.Live() || !s.Until().IsZero() {
		t.Error("the grant outlived its cap")
	}
}

func TestSSHGrantNeedsAHostAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	s := &SSHAgent{}
	if _, err := s.Grant(time.Minute); err == nil {
		t.Fatal("granted with no agent on the host")
	}
}
