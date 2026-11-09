package apptest

import (
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/ship"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// ShipScene is the Ship page's session: search-api and web-dashboard on GitHub (a fake), a work session on both with the fake
// agent, and the window on it; more changes the app's config besides. Its file watchers see only what the test says
// changed (ScriptedFiles): Scene.WriteFiles writes and says so. It answers the host, the fake GitHub, the
// session and each project's upstream clone.
func ShipScene(t *testing.T, more ...func(*Config)) (s *Scene, lw *rig.Host, hub *ghtest.Server, se core.SessionState, ups map[string]string) {
	t.Helper()
	return ShipSceneWith(t, nil, more...)
}

// ShipSceneWith is ShipScene with setup run on the scene before the session starts: a plugin loaded, say.
func ShipSceneWith(t *testing.T, setup func(*Scene), more ...func(*Config)) (s *Scene, lw *rig.Host, hub *ghtest.Server, se core.SessionState, ups map[string]string) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	hub = ghtest.New(t)
	lw = rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	api, apiUp := rig.ShipRepo(t, "search-api", map[string]string{
		"internal/api/usage/handler.go":    "package usage\n\nfunc Handle() {}\n",
		"internal/api/usage/routes.go":     "package usage\n\nvar Routes = 1\n",
		".github/CODEOWNERS":               "/internal/api/  @acme/platform-team @jkowalski\n",
		".github/pull_request_template.md": "## What\n\n## Why\n",
	})
	web, webUp := rig.ShipRepo(t, "web-dashboard", map[string]string{"src/usage.ts": "export const usage = 1\n"})
	s = New(t, Options{ScriptedFiles: true, Config: func(cfg *Config) {
		Connect(lw.W)(cfg)
		for _, m := range more {
			m(cfg)
		}
	}})
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if setup != nil {
		setup(s)
	}
	s.Run("session.start", registry.Args{"kind": "work", "prompt": "Search API rate-limit headers", "ticket": "ENG-2231", "provider": "fake",
		"repos": []string{api, web}})
	st := rig.WaitState(t, s.Store(), "the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se = sess.Snapshot()
	s.Reopen()
	return s, lw, hub, se, map[string]string{"search-api": apiUp, "web-dashboard": webUp}
}

// ShipSettle frames until the session's projects have been read again after every step started, and ok.
func (s *Scene) ShipSettle(what string, ok func() bool) {
	s.T.Helper()
	s.Until(what, func() bool {
		s.ShipIdle()
		s.RunQueued()
		s.Frame()
		return ok()
	})
}

// ShipIdle waits for the ship plugin's reads and steps, framing meanwhile: a Lua code host answers on the loop.
func (s *Scene) ShipIdle() {
	done := make(chan struct{})
	go func() { PluginOf[*ship.Ship](s, "ship").Wait(); close(done) }()
	for {
		select {
		case <-done:
			return
		default:
			s.RunQueued()
			s.Frame()
		}
	}
}

// ProjectDir is the directory of se's project name; "" if it has none.
func ProjectDir(se core.SessionState, name string) string {
	for _, p := range se.Projects {
		if p.Name == name {
			return p.Dir
		}
	}
	return ""
}

// ShipSession is rig.ShipSession's session as read, in front, on 09:00's clock.
func ShipSession(t *testing.T) (*Scene, core.SessionState) {
	t.Helper()
	s := New(t, Options{})
	s.Now = At(9, 0, 0)
	se, reads := rig.ShipSession(t, s.Now)
	s.Apply(core.SessionSynced{Session: se}, core.SessionFocused{ID: se.ID})
	x := PluginOf[*ship.Ship](s, "ship")
	x.Clock = func() time.Time { return s.Now }
	x.Fixture(se.ID, false, func([]ship.Read) []ship.Read { return reads(GitHubCode(s)) })
	return s, se
}
