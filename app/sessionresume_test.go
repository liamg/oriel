package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// "Resume a session: pick one": picking an archived session reopens it and starts its agent.
func TestResumePickerResumesAnArchivedSession(t *testing.T) {
	lw := rig.NewHost(t)
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	if err := s.Registry().Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host"); err != nil {
		t.Fatal(err)
	}
	s.Run("session.start", registry.Args{"kind": "plan", "prompt": "Queue depth alert", "provider": "fake"})
	st := s.UntilState("the session in front", func(st core.State) bool { return st.Focus != "" })
	lw.Fake.WaitStarted(t)
	id := st.Focus

	s.Run("session.archive", registry.Args{"id": string(id)})
	s.UntilState("the session archived", func(st core.State) bool {
		se, _ := st.Session(id)
		return se.Snapshot().Archived
	})
	s.Run("session.resume", nil)
	s.Frame()
	s.Frame()
	s.Type("Queue depth")
	s.Until("the archived session offered", func() bool {
		se, _ := s.Store().State().Session(id)
		return se != nil && strings.Contains(s.Text(), se.Name())
	})
	s.Type("CR")
	s.UntilState("the archived session resumed with its agent", func(st core.State) bool {
		se, _ := st.Session(id)
		a := se.Snapshot()
		return st.Focus == id && !a.Archived && a.Agent.Running
	})
}
