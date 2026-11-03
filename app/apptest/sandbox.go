package apptest

import (
	"os"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/sandbox"
)

// SandboxedScene is a HostScene whose sandboxes run backend, with a sandboxed session of the fake agent, "w", in
// front and the window on it.
func SandboxedScene(t *testing.T, backend sandbox.Backend) (*Scene, *rig.Host, core.SessionState) {
	t.Helper()
	// The session socket's path must fit the unix limit: a short home.
	home, err := os.MkdirTemp("/tmp", "op")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	f := host.NewSandboxFactory(func() sandbox.Config { return sandbox.Config{} })
	f.Backend = func(sandbox.Config) (sandbox.Backend, error) { return backend, nil }
	f.Available = func() []string { return []string{"test"} }
	h := rig.NewHostWith(t, host.Options{Sandbox: f, Home: home})
	f.Attach(h.W)
	state := t.TempDir()
	s := New(t, Options{Config: func(cfg *Config) {
		Connect(h.W)(cfg)
		cfg.StateDir = state
	}})
	t.Setenv("HOME", home)
	s.Now = time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": h.Repo, "sandbox": true, "network": "ask"})
	h.Fake.WaitStarted(t)
	s.UntilState("the sandboxed session in front", func(st core.State) bool {
		se, ok := st.Session("w")
		return ok && st.Focus == "w" && se.Snapshot().Agent.Sandboxed
	})
	s.Until("the window on the session", func() bool { s.RunQueued(); return s.Reopen() })
	se, _ := s.Store().State().Session("w")
	return s, h, se.Snapshot()
}
