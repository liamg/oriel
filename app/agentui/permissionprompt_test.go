package agentui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/ui/widget"
)

// promptScene is two sessions, alpha in front with a file open in the editor and the cursor in it, and beta behind;
// ask.answer closes the ask it answers, as the session host does, and says what it was given.
func promptScene(t *testing.T) (*apptest.Scene, *[]string) {
	t.Helper()
	dir := t.TempDir()
	rig.WriteFiles(t, dir, map[string]string{"notes.txt": "notes\n"})
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	s.Now = apptest.At(14, 30, 0)
	for _, id := range []core.SessionID{"alpha", "beta"} {
		s.Apply(core.SessionSynced{Session: core.SessionState{ID: id, Name: string(id), Kind: core.KindWork, Status: core.StatusQuiet,
			Agent: core.AgentState{Provider: "claude", Running: true}}})
		s.Apply(core.TabOpened{ID: id})
	}
	s.Apply(core.SessionFocused{ID: "alpha"})
	var got []string
	s.Registry().Command(registry.Command{ID: "ask.answer", Run: func(_ registry.Ctx, a registry.Args) error {
		got = append(got, a.String("ask")+"="+a.String("decision"))
		return s.Store().Apply(core.AskClosed{Session: core.SessionID(a.String("id")), ID: a.String("ask"), Decision: a.String("decision")})
	}})
	s.Run("editor.open", registry.Args{"path": filepath.Join(dir, "notes.txt")})
	s.Until("the editor", func() bool { return strings.Contains(s.Text(), "notes") })
	s.ClickText("notes")
	s.Quiet()
	s.Now = s.Now.Add(time.Minute)
	s.Frame()
	return s, &got
}

func permission(id, command string) core.Ask {
	return core.Ask{ID: id, Kind: core.AskPermission, Tool: "Bash", Summary: command, Rule: "Bash(" + command + ":*)"}
}

// prompt is the permission prompt in the middle of the window, or nil.
func prompt(s *apptest.Scene) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == render.RoleDialog && strings.HasPrefix(n.Name, "Permission: ") {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// wantPrompt fails unless the prompt for command is up, centred over the window and holding focus.
func wantPrompt(t *testing.T, s *apptest.Scene, command string) {
	t.Helper()
	p := prompt(s)
	if p == nil {
		t.Fatalf("no permission prompt for %q:\n%s", command, s.Text())
	}
	if want := "Permission: Bash(" + command + ")"; p.Name != want {
		t.Fatalf("the prompt is %q, want %q", p.Name, want)
	}
	if !p.Modal {
		t.Error("the prompt doesn't hold focus")
	}
	win, box := apptest.Centre(render.Rect{Max: apptest.MockupSize}), apptest.Centre(p.Bounds)
	if dx, dy := box.X-win.X, box.Y-win.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
		t.Errorf("the prompt's centre is %v, the window's %v", box, win)
	}
}

func wantNoPrompt(t *testing.T, s *apptest.Scene, why string) {
	t.Helper()
	if p := prompt(s); p != nil {
		t.Fatalf("%s, yet the prompt %q is up", why, p.Name)
	}
}

// A permission asked while you type waits until no key has gone down for three seconds, so it never takes a key
// meant for what you're typing in; everything typed meanwhile reaches the editor.
func TestAPermissionPromptWaitsForTypingToPause(t *testing.T) {
	s, _ := promptScene(t)
	s.Type("i", "a", "b")
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Frame()
	wantNoPrompt(t, s, "a key went down a moment ago")
	s.Now = s.Now.Add(2 * time.Second)
	s.Frame()
	wantNoPrompt(t, s, "two seconds after the last key")
	s.Type("c")
	s.Now = s.Now.Add(2900 * time.Millisecond)
	s.Frame()
	wantNoPrompt(t, s, "2.9 seconds after the last key")
	s.Now = s.Now.Add(100 * time.Millisecond)
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")
	if !strings.Contains(s.Text(), "abc") {
		t.Errorf("what was typed didn't all reach the editor:\n%s", s.Text())
	}
}

// With no key down for three seconds already, a permission shows at once; inbox.prompt_pause sets how long.
func TestAPermissionPromptShowsAtOnceWhenIdle(t *testing.T) {
	s, _ := promptScene(t)
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")

	s, _ = promptScene(t)
	s.Type("i")
	s.Now = s.Now.Add(3 * time.Second)
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")

	s, _ = promptScene(t)
	if err := s.Registry().Options().Set("inbox.prompt_pause", registry.LayerUser, registry.At{}, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	s.Type("i")
	s.Now = s.Now.Add(9 * time.Second)
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Frame()
	wantNoPrompt(t, s, "inbox.prompt_pause is 10s, and it's 9s since the last key")
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")
}

// A permission for a session behind another doesn't come up over the one in front: its tab calls for you instead,
// pulsing, then steady until you go to it. Going to it, even straight from typing, brings the prompt up at once.
func TestAPermissionBehindCallsFromItsTab(t *testing.T) {
	s, _ := promptScene(t)
	s.Now = s.Now.Add(10 * time.Second)
	s.Apply(core.AskOpened{Session: "beta", Ask: permission("p1", "make e2e")})
	s.Frame()
	s.Frame()
	wantNoPrompt(t, s, "the ask is beta's, behind alpha")
	s.Within("beta", "Calls for you")
	if n := s.Count("Calls for you"); n != 1 {
		t.Errorf("%d tabs call for you, want beta's alone", n)
	}
	s.Now = s.Now.Add(time.Minute)
	s.Frame()
	s.Within("beta", "Calls for you") // settled, still calling
	wantNoPrompt(t, s, "a minute on, beta still behind")

	s.Type("i", "x")
	s.Press("<D-2>")
	s.Frame()
	s.Frame()
	if f := s.Store().State().Focus; f != "beta" {
		t.Fatalf("⌘2 brought %q to the front", f)
	}
	wantPrompt(t, s, "make e2e")
	if n := s.Find("Calls for you"); n != nil {
		t.Error("beta's tab still calls with it in front")
	}
}

// Asks come up one at a time in the order they came: once one is answered the next follows at once, the keys
// answering it having been the prompt's, not typing; one answered elsewhere or withdrawn goes, and Escape puts one
// off to the inbox until its session next comes to the front.
func TestPermissionPromptsComeOneByOne(t *testing.T) {
	s, got := promptScene(t)
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p2", "go test ./...")})
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p3", "rm -rf build")})
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p4", "git push")})
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")
	if n := strings.Count(s.Text(), "Allow once"); n != 1 {
		t.Errorf("%d prompts' buttons show, want one prompt's", n)
	}

	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Type("y")
	s.Frame()
	s.Frame()
	if strings.Join(*got, " ") != "p1=allow" {
		t.Fatalf("y answered %v, want p1=allow", *got)
	}
	wantPrompt(t, s, "go test ./...")

	s.Apply(core.AskClosed{Session: "alpha", ID: "p2", Decision: "allow"}) // answered in the console
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "rm -rf build")

	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Type("Esc")
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "git push")
	s.Now = s.Now.Add(widget.CardKeyGuard)
	s.Frame()
	s.Type("n")
	s.Frame()
	s.Frame()
	if strings.Join(*got, " ") != "p1=allow p4=deny" {
		t.Fatalf("the answers are %v, want p1=allow p4=deny", *got)
	}
	wantNoPrompt(t, s, "the one left was put off")
	if _, ok := s.InboxCard("asks", "alpha/p3"); !ok {
		t.Error("the ask put off isn't waiting in the inbox")
	}

	s.Press("<D-2>")
	s.Frame()
	s.Press("<D-1>")
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "rm -rf build")
	s.Apply(core.AskClosed{Session: "alpha", ID: "p3", Decision: "gone"}) // withdrawn by the agent
	s.Frame()
	s.Frame()
	wantNoPrompt(t, s, "the last ask was withdrawn")
}

// A card for over the window that comes while you type in a text field (a security key's touch, at the top) waits
// for you to stop for a moment: it doesn't cover what you're writing or catch a key meant for it. (The sandbox's asks
// are prompts, waiting as permissions do: TestTheSandboxsAsksArePromptsToo.)
func TestAStackedCardWaitsForTypingToStop(t *testing.T) {
	s, _ := promptScene(t)
	s.Type("i", "r", "o")
	s.Apply(core.AskOpened{Session: "alpha", Ask: core.Ask{ID: "k1", Kind: core.AskKey, Summary: "git push origin round"}})
	s.Frame()
	for _, k := range []string{"u", "n", "d"} {
		s.Now = s.Now.Add(time.Second)
		s.Type(k)
		if strings.Contains(s.Text(), "Touch your security key") {
			t.Fatalf("the card went over the window while typing:\n%s", s.Text())
		}
	}
	s.Now = s.Now.Add(2 * time.Second)
	s.Frame()
	s.Frame()
	if !s.Has("Touch your security key", "round") {
		t.Errorf("once typing stopped, no card, or what was typed went elsewhere:\n%s", s.Text())
	}
}

// promptNamed is the prompt dialog named name, or nil.
func promptNamed(s *apptest.Scene, name string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == render.RoleDialog && n.Name == name {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// The sandbox's asks hold the agent on you just as a permission does: a gateway's call (a push, leaving the sandbox)
// and a connection the egress proxy holds come up as prompts the same way, centred and one at a time, after the
// permission that came first; behind, the session's tab calls for them.
func TestTheSandboxsAsksArePromptsToo(t *testing.T) {
	s, got := promptScene(t)
	s.Apply(core.AskOpened{Session: "alpha", Ask: permission("p1", "make e2e")})
	s.Apply(core.AskOpened{Session: "alpha", Ask: core.Ask{ID: "g1", Kind: core.AskGateway, Tool: "git push", Summary: "git push origin round"}})
	s.Apply(core.AskOpened{Session: "alpha", Ask: core.Ask{ID: "n1", Kind: core.AskNetwork, Host: "registry.npmjs.org"}})
	s.Frame()
	s.Frame()
	wantPrompt(t, s, "make e2e")
	s.Now = s.Now.Add(time.Second) // past the moment a prompt ignores keys as it comes up
	s.Press("y")
	s.Frame()
	s.Frame()
	for _, step := range []struct{ name, key, answer string }{
		{"git push origin round", "y", "g1=allow"},
		{"Network: registry.npmjs.org", "a", "n1=always"}, // a on a network ask allows the host for the session
	} {
		p := promptNamed(s, step.name)
		if p == nil {
			t.Fatalf("no prompt %q:\n%s", step.name, s.Text())
		}
		if !p.Modal {
			t.Errorf("%q doesn't hold focus", step.name)
		}
		win, box := apptest.Centre(render.Rect{Max: apptest.MockupSize}), apptest.Centre(p.Bounds)
		if dx, dy := box.X-win.X, box.Y-win.Y; dx < -2 || dx > 2 || dy < -2 || dy > 2 {
			t.Errorf("%q's centre is %v, the window's %v", step.name, box, win)
		}
		s.Now = s.Now.Add(time.Second)
		s.Press(step.key)
		s.Frame()
		s.Frame()
		if (*got)[len(*got)-1] != step.answer {
			t.Errorf("answered %v, want %s last", *got, step.answer)
		}
	}
	if p := promptNamed(s, "Network: registry.npmjs.org"); p != nil {
		t.Error("the network prompt stayed up once answered")
	}

	s.Apply(core.AskOpened{Session: "beta", Ask: core.Ask{ID: "g2", Kind: core.AskGateway, Tool: "git push", Summary: "git push origin beta"}})
	s.Frame()
	s.Frame()
	if promptNamed(s, "git push origin beta") != nil {
		t.Error("beta's gateway ask came up over alpha")
	}
	s.Within("beta", "Calls for you")
}
