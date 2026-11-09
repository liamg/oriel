package agentui_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
)

// A permission over the window takes the keys as it appears, from the editor being typed in: y answers it there and
// then, and nothing typed reaches the editor under it. A key typed in the moment it appears answers nothing. Once
// it's answered, the keys are the editor's again.
func TestAPermissionOverTheWindowTakesItsKeys(t *testing.T) {
	lw := rig.NewHost(t)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": lw.Repo})
	a := lw.Fake.WaitStarted(t)
	s.Until("the session in front", func() bool { return s.Store().State().Focus == "w" })
	se, _ := s.Store().State().Session("w")
	p, _ := se.Snapshot().Primary()
	file := filepath.Join(p.Dir, "focus.go")
	rig.WriteFiles(t, filepath.Dir(file), map[string]string{"focus.go": "package focus\n"})
	s.Run("editor.open", registry.Args{"path": file})
	s.Until("the editor", func() bool { return strings.Contains(s.Text(), "focus") })
	s.ClickText("focus")
	s.Quiet()

	allow := a.Ask(agent.Permission{ID: "p1", Tool: "Bash", Input: rig.ToolInput(map[string]string{"command": "make e2e"})})
	s.Until("the permission card", func() bool { s.RunQueued(); return strings.Contains(s.Text(), "Allow once") })
	s.Type("y") // on its way to the editor as the card came
	s.Frame()
	select {
	case d := <-allow:
		t.Fatalf("a key typed as the card appeared answered it: %+v", d)
	default:
	}
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Type("y")
	var d agent.Decision
	s.Until("the agent's answer", func() bool {
		s.RunQueued()
		select {
		case d = <-allow:
			return true
		default:
			return false
		}
	})
	if !d.Allow || d.Always {
		t.Errorf("y answered %+v, want Allow once", d)
	}
	s.Until("the card gone", func() bool { s.RunQueued(); return !strings.Contains(s.Text(), "Allow once") })
	s.Type("i")
	s.Frame()
	if !strings.Contains(s.Text(), "INSERT") {
		t.Errorf("i after answering didn't reach the editor:\n%s", s.Text())
	}
}
