package agentsock

import (
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
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
	_ = a.SetDeadline(time.Now().Add(2 * time.Second))
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
	s := &SSHAgent{}
	until, err := s.Grant(time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(until); d > SSHMax || d < SSHMax-time.Second {
		t.Errorf("grant lasts %s, want %s", d, SSHMax)
	}
	s.Revoke()
}

func TestSSHGrantNeedsAHostAgent(t *testing.T) {
	t.Setenv("SSH_AUTH_SOCK", "")
	s := &SSHAgent{}
	if _, err := s.Grant(time.Minute); err == nil {
		t.Fatal("granted with no agent on the host")
	}
}
