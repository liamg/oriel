package apptest

import (
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
)

// RequestsScene is the Requests fixture's session, pricing-rounding, in front: Claude working on "Negative values round the wrong
// way" at round.go:46, a question waiting on you, a request queued behind the turn, a btw, two resolved, the tasks
// and runs behind them, and Claude's plan usage.
func RequestsScene(t *testing.T) *Scene {
	t.Helper()
	dir := t.TempDir()
	rig.WriteFiles(t, dir, map[string]string{"internal/pricing/round.go": rig.RoundGo()})
	s := New(t, Options{WorkDir: dir})
	s.Now = At(14, 30, 0)
	se := rig.RequestsSession(s.Now, dir)
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID}, rig.ClaudeMaxUsage)
	return s
}

// GitHubCode is the GitHub code host as the window's registry has it; the test fails without one.
func GitHubCode(s *Scene) *registry.CodeHost {
	s.T.Helper()
	h, ok := registry.HostByName(s.Registry(), "github")
	if !ok {
		s.T.Fatal("GitHub isn't a code host")
	}
	return &h
}

// OmniboxOpen says the omnibox is up: its dialog is in the frame.
func (s *Scene) OmniboxOpen() bool { return s.FindRole(render.RoleDialog, "Search everything") != nil }

// Light draws the scene in the built-in theme's light variant from the next frame.
func (s *Scene) Light() {
	s.T.Helper()
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	r, err := theme.Resolve(st, theme.Light)
	if err != nil {
		s.T.Fatal(err)
	}
	s.UI().SetTheme(r)
	s.Frame()
}
