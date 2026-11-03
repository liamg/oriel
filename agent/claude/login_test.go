package claude

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/agent/eventq"
)

// The recordings in testdata are Claude Code 2.1.296's own:
//   - oauth-revoked.jsonl: the record it wrote into a session's log when its login was revoked (in a crit sandbox).
//   - not-logged-in.jsonl, signed-out-start.pty, signed-out-send.pty: claude run in a PTY of 200×50 with an empty
//     CLAUDE_CONFIG_DIR: the record it wrote for a prompt sent with no login, and everything its screen showed from
//     the start, and from the start through that send.

// An API error in the log that says to run /login raises AuthExpired with what it said, and how to sign in.
func TestAuthExpiredFromTheLog(t *testing.T) {
	s := agenttest.Start(t, fakeHarness(t))
	s.Send("lose the login")
	ev := s.Wait("AuthExpired", func(ev agent.Event) bool { _, ok := ev.(agent.AuthExpired); return ok }).(agent.AuthExpired)
	want := agent.AuthExpired{Provider: "claude", Detail: "OAuth token revoked · Please run /login", Terminal: "/login",
		Shell: "claude auth login", HostCommand: []string{"claude", "auth", "login"}}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("AuthExpired = %+v, want %+v", ev, want)
	}
	s.Reply()
	for _, ev := range s.Events {
		if m, ok := ev.(agent.Message); ok && strings.Contains(m.Text, "/login") {
			t.Errorf("the error came as the agent's words too: %+v", m)
		}
	}
}

// A sandboxed claude signs in as the host does, so its lost login says to sign in on the host, not with /login in
// its own terminal (which would sign in that sandbox alone).
func TestSandboxedAuthExpiredSignsInOnTheHost(t *testing.T) {
	h := fakeHarness(t)
	spec := h.Spec
	h.Spec = func(t *testing.T, dir string) agent.StartSpec {
		s := spec(t, dir)
		s.Sandbox = &agent.Sandbox{Name: "box"}
		return s
	}
	s := agenttest.Start(t, h)
	s.Send("lose the login")
	ev := s.Wait("AuthExpired", func(ev agent.Event) bool { _, ok := ev.(agent.AuthExpired); return ok }).(agent.AuthExpired)
	if ev.Terminal != "" || !slices.Equal(ev.HostCommand, []string{"claude", "auth", "login"}) {
		t.Errorf("AuthExpired = %+v", ev)
	}
	s.Reply()
}

// The signed-out line on claude's screen raises AuthExpired, once while it stays; /login typed in the
// terminal and a login's success raise AuthRestored.
func TestAuthExpiredFromTheScreen(t *testing.T) {
	s := agenttest.Start(t, fakeHarness(t))
	s.Send("show the login prompt")
	ev := s.Wait("AuthExpired", func(ev agent.Event) bool { _, ok := ev.(agent.AuthExpired); return ok }).(agent.AuthExpired)
	if ev.Detail != "Not logged in · Run /login" || ev.Terminal != "/login" {
		t.Errorf("AuthExpired = %+v", ev)
	}
	s.Reply()
	if _, err := s.S.Terminal().Write([]byte("/login\r")); err != nil {
		t.Fatal(err)
	}
	s.Wait("AuthRestored", func(ev agent.Event) bool { r, ok := ev.(agent.AuthRestored); return ok && r.Provider == "claude" })
	n := 0
	for _, ev := range s.Events {
		if _, ok := ev.(agent.AuthExpired); ok {
			n++
		}
	}
	if n != 1 {
		t.Errorf("AuthExpired raised %d times for one screen", n)
	}
}

// The screen reader finds the signed-out line in the recorded screens however the output is split, and
// takes neither the error under a prompt ("Please run /login", which the log says) nor the agent's prose for it.
func TestScreenSignedOut(t *testing.T) {
	for _, file := range []string{"signed-out-start.pty", "signed-out-send.pty"} {
		b, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		for _, size := range []int{len(b), 7, 1} {
			var sc screen
			var got []string
			for i := 0; i < len(b); i += size {
				if out, ok := sc.read(b[i:min(i+size, len(b))]); ok && out.signedOut != "" {
					got = append(got, out.signedOut)
				}
			}
			if len(got) == 0 || got[0] != "Not logged in · Run /login" {
				t.Errorf("%s in chunks of %d: %q", file, size, got)
			}
		}
	}
	var sc screen
	for _, prose := range []string{"⏺ Claude shows Please run /login when it's signed out.\r\n", "  ⎿  Not logged in · Please run /login\r\n"} {
		if out, ok := sc.read([]byte(prose)); ok {
			t.Errorf("%q read as %+v", prose, out)
		}
	}
	out, ok := sc.read([]byte("\x1b[38;2;78;186;101mLogin successful. Press \x1b[1mEnter\x1b[22m to continue…\x1b[39m"))
	if !ok || !out.signedIn {
		t.Errorf("a login's success read as %+v", out)
	}
}

// The mod's api-error that says to run /login raises AuthExpired beside the failure.
func TestAuthExpiredFromTheHook(t *testing.T) {
	s := &session{events: eventq.New()}
	s.event(eventIn{Kind: "api-error", Error: "authentication_failed", Text: "OAuth token revoked · Please run /login"})
	if f, ok := (<-s.events.Out()).(agent.Failure); !ok || f.Retryable {
		t.Errorf("first = %+v, want a failure", f)
	}
	if a, ok := (<-s.events.Out()).(agent.AuthExpired); !ok || a.Detail != "OAuth token revoked · Please run /login" {
		t.Errorf("then %+v, want AuthExpired", a)
	}
}

// A login is back once a request after it went succeeds: the agent's next answer in the log raises
// AuthRestored, once; answers while signed in don't.
func TestAnAnswerAfterALostLoginRestoresIt(t *testing.T) {
	s := agenttest.Start(t, fakeHarness(t))
	s.Send("say hi")
	s.Reply()
	s.Send("lose the login")
	s.Wait("AuthExpired", func(ev agent.Event) bool { _, ok := ev.(agent.AuthExpired); return ok })
	s.Reply()
	s.Send("say hello")
	s.Wait("AuthRestored", func(ev agent.Event) bool { r, ok := ev.(agent.AuthRestored); return ok && r.Provider == "claude" })
	s.Reply()
	s.Send("say bye")
	s.Reply()
	n, lost := 0, false
	for _, ev := range s.Events {
		switch ev.(type) {
		case agent.AuthExpired:
			lost = true
		case agent.AuthRestored:
			if !lost {
				t.Error("AuthRestored before the login was lost")
			}
			n++
		}
	}
	if n != 1 {
		t.Errorf("AuthRestored raised %d times, want once", n)
	}
}
