package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui/widget"
)

// clickTop clicks the middle of the last run of text drawn in the frame on screen: the one on top.
func (r *running) clickTop(text string) {
	r.t.Helper()
	var at render.Point
	r.latest(`"`+text+`" to click`, func(f headless.Frame) bool {
		found := false
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == text {
				at, found = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}, true
			}
		}
		return found
	})
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: at, Buttons: render.ButtonPrimary})
	r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: at, Buttons: render.ButtonPrimary})
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x"}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

// AI-4 done when: "each mode starts the right prompt pack". SE-1: "each kind starts from the launcher" or
// session.start; "direct never creates a worktree; new repo inits and registers". ui.md 04: "Mode: Work · Plan ·
// Review PR": the kinds marked Launcher, Work first and picked; Job, New repo and Direct start through session.start.
func TestLauncherModesStartTheirPacks(t *testing.T) {
	lw := newLocalHost(t)
	ctx := context.Background()
	c := lw.w
	if _, err := c.AddRepoPath(ctx, lw.repo); err != nil {
		t.Fatal(err)
	}
	// The repo's origin is itself, with a pull request's head for the review.
	git(t, lw.repo, "remote", "add", "origin", lw.repo)
	git(t, lw.repo, "update-ref", "refs/pull/7/head", "HEAD")
	parent, plain := t.TempDir(), t.TempDir()
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	// The test host has no sandbox factory.
	r.do(func() error {
		return r.host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})

	prompt := func(text string) {
		r.clickTop(promptPlaceholder)
		r.typeText(text)
	}
	addRepo := func() {
		r.clickTop("pricing")
		r.latest("the repo ticked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "1 selected") })
	}
	started := func(mode, pack string) {
		t.Helper()
		a := lw.fake.WaitStarted(t)
		if a.Spec.Pack.Name != pack {
			t.Errorf("%s started the %q pack, want %q", mode, a.Spec.Pack.Name, pack)
		}
		waitState(t, r.store, "the "+mode+" session in front", func(st core.State) bool {
			se, ok := st.Session(st.Focus)
			return ok && se.Snapshot().Kind == core.Kind(pack)
		})
	}
	for i, tc := range []struct {
		mode, pack string
		fill       func()
	}{
		{"Work", "work", func() { prompt("Fix stale search results"); addRepo() }},
		{"Work", "work", func() { prompt("Sketch an invoicing service") }},
		{"Plan", "plan", func() { prompt("Round invoice lines"); addRepo() }},
		{"Review PR", "review", func() {
			r.clickTop(prPlaceholder)
			r.typeText("https://github.com/acme/pricing/pull/7")
			addRepo()
		}},
	} {
		// Start lands on the session's first request, where ⌘N is a new request: the launcher opens by its command.
		if err := r.do(func() error { return r.host.Run(context.Background(), "session.new", nil) }); err != nil {
			t.Fatal(err)
		}
		f := r.latest("the launcher", func(f headless.Frame) bool { return strings.Contains(f.Text(), "NEW SESSION") })
		if i == 0 {
			if !strings.Contains(f.Text(), "Start Work") {
				t.Errorf("the launcher doesn't open on Work:\n%s", f.Text())
			}
			modes := launcherModes(r.win.Access())
			if want := []string{"Work", "Plan", "Review PR"}; strings.Join(modes, " · ") != strings.Join(want, " · ") {
				t.Errorf("the launcher's modes are %v, want %v", modes, want)
			}
		}
		r.clickTop(tc.mode)
		r.latest(tc.mode+" picked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start "+tc.mode) })
		tc.fill()
		r.clickTop("Start " + tc.mode)
		r.waitGone("NEW SESSION")
		started(tc.mode, tc.pack)
	}

	// The kinds the launcher doesn't offer still start through session.start.
	for _, tc := range []struct {
		pack string
		args registry.Args
	}{
		{"job", registry.Args{"kind": "job", "prompt": "Sweep stale deps", "repos": []string{lw.repo}}},
		{"newrepo", registry.Args{"kind": "newrepo", "name": "invoicer", "dir": parent}},
		{"direct", registry.Args{"kind": "direct", "dir": plain}},
	} {
		tc.args["provider"] = "fake"
		if err := r.do(func() error { return r.host.Run(context.Background(), "session.start", tc.args) }); err != nil {
			t.Fatalf("session.start %v: %v", tc.args, err)
		}
		started(tc.pack, tc.pack)
	}

	st := r.store.State()
	kinds := map[core.Kind]core.SessionState{}
	for _, se := range st.Sessions {
		if se.Kind != core.KindWork || len(se.Projects) == 0 {
			kinds[se.Kind] = se
		}
	}
	if w := kinds[core.KindWork]; w.Dir == "" || len(w.Projects) != 0 {
		t.Errorf("the work session with no repo = %+v", w)
	}
	if d := kinds[core.KindDirect]; d.Dir != plain || len(d.Projects) != 1 || d.Projects[0].Dir != plain {
		t.Errorf("direct = %+v, want the directory itself", d)
	}
	if out := git(t, lw.repo, "worktree", "list"); strings.Contains(out, plain) {
		t.Errorf("a direct session made a worktree:\n%s", out)
	}
	repos := c.Repos()
	registered := false
	for _, x := range repos {
		registered = registered || x.Path == filepath.Join(parent, "invoicer")
	}
	if !registered {
		t.Errorf("the new repo isn't registered: %+v", repos)
	}
}

// A pull request's link pasted into the prompt picks the kind that starts from a pull request, Review PR, with the
// link in its field.
func TestLauncherPastedPRPicksReview(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.typeKeys("<D-n>")
	r.latest("the launcher on Work", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
	r.clickTop(promptPlaceholder)
	const url = "https://github.com/acme/pricing/pull/7"
	r.win.SetClipboard(url)
	paste := render.ModCtrl
	if runtime.GOOS == "darwin" {
		paste = render.ModSuper
	}
	r.win.Send(render.KeyEvent{Key: "v", Mods: paste})
	r.latest("Review PR picked, with the link", func(f headless.Frame) bool {
		return strings.Contains(f.Text(), "Start Review PR") && strings.Contains(f.Text(), url)
	})
}

// launcherModes are the titles of the launcher's Mode tabs, in the accessibility tree n.
func launcherModes(n *render.AccessNode) []string {
	var out []string
	var walk func(n *render.AccessNode, in bool)
	walk = func(n *render.AccessNode, in bool) {
		if n == nil {
			return
		}
		in = in || n.Role == render.RoleTabList && n.Name == "Mode"
		if in && n.Role == render.RoleTab {
			out = append(out, n.Name)
		}
		for _, k := range n.Children {
			walk(k, in)
		}
	}
	walk(n, false)
	return out
}

// SE-1: "Register session.new and session.resume; the start view's greyed entries light up on their own". SE-3 and
// sessions.md: "The app opens on mission control", whose stand-in until SH-7 is the start view's recent sessions.
func TestStartViewListsSessions(t *testing.T) {
	lw := newLocalHost(t)
	c := lw.w
	if _, err := c.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "pricing-rounding", Repos: []core.RepoID{core.RepoID(lw.repo)}}); err != nil {
		t.Fatal(err)
	}
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	r.waitFrame("the session under Recent", func(s string) bool {
		return containsAll(s, "SESSIONS", "pricing-rounding", "work · pricing") && !strings.Contains(s, "SOON")
	})
	r.clickTop("pricing-rounding")
	st := waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "pricing-rounding" })
	se, _ := st.Session("pricing-rounding")
	p, _ := se.Snapshot().Primary()
	r.waitGone("SESSIONS")
	var dir string
	r.do(func() error { dir = r.host.Here().Project; return nil })
	if dir != p.Dir {
		t.Errorf("the window is on %q, want the session's worktree %q", dir, p.Dir)
	}
}

// SE-3, sessions.md: "Files outside the session's worktrees (the stdlib, dependencies, another repo) open read-only".
func TestFilesOutsideTheSessionOpenReadOnly(t *testing.T) {
	lw := newLocalHost(t)
	outside := filepath.Join(t.TempDir(), "other.go")
	os.WriteFile(outside, []byte("package other\n"), 0o644)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "repos": []string{lw.repo}})
	}); err != nil {
		t.Fatal(err)
	}
	st := waitState(t, r.store, "the session in front", func(st core.State) bool { return st.Focus == "w" })
	se, _ := st.Session("w")
	p, _ := se.Snapshot().Primary()
	inside := filepath.Join(p.Dir, "round.go")
	os.WriteFile(inside, []byte("package pricing\n"), 0o644)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var dir string
		r.do(func() error { dir = r.host.Here().Project; return nil })
		if dir == p.Dir {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window is on %q, not the worktree", dir)
		}
	}

	open := func(path string) {
		t.Helper()
		if err := r.do(func() error { return r.host.Run(context.Background(), "editor.open", registry.Args{"path": path}) }); err != nil {
			t.Fatal(err)
		}
	}
	open(outside)
	r.waitText("read-only · outside the session")
	open(inside)
	r.waitFrame("round.go", func(s string) bool { return strings.Contains(s, "package\n \npricing") })
	time.Sleep(200 * time.Millisecond)
	if fs := r.win.Frames(); strings.Contains(fs[len(fs)-1].Text(), "read-only ·") {
		t.Errorf("a file in the session's worktree opened read-only:\n%s", fs[len(fs)-1].Text())
	}
}

// ui.md 04 (04-launch): the launcher over the start view, a plan from a ticket on two repos, Claude and Codex with
// their plans' usage, recent prompts and the pre-flight.
func TestLauncherSnapshot(t *testing.T) {
	claude, codex := agenttest.NewFake(), agenttest.NewFake()
	claude.Name, codex.Name = "claude", "codex"
	lw := newLocalHost(t, claude, codex)
	cloud := filepath.Join(t.TempDir(), "cloud")
	git(t, filepath.Dir(cloud), "init", "-q", "-b", "main", cloud)
	git(t, cloud, "commit", "-q", "--allow-empty", "-m", "first")
	c := lw.w
	for _, r := range []string{cloud, lw.repo} {
		if _, err := c.AddRepoPath(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.now = at(14, 30, 0)
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	ago := func(h int) time.Time { return s.now.Add(-time.Duration(h) * time.Hour) }
	for _, ev := range []core.Event{
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "max plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .38}, {Name: "wk", Used: .71}}}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "codex", Plan: "pro plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .12}}}},
		core.SessionSynced{Session: core.SessionState{ID: "rounding", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusQuiet, Created: ago(2),
			Prompt: "Round minor units half-even, per currency, and keep the totals stable", Projects: []core.Project{{Name: "pricing", Dir: lw.repo}}}},
		core.SessionSynced{Session: core.SessionState{ID: "deps", Name: "sweep-deps", Kind: core.KindJob, Status: core.StatusQuiet, Created: ago(26),
			Prompt: "Sweep stale deps, keep major versions", Agent: core.AgentState{Provider: "codex"}}},
		core.SessionSynced{Session: core.SessionState{ID: "auth", Name: "auth-review", Kind: core.KindReview, Status: core.StatusQuiet, Created: ago(50),
			Prompt: "Show me how auth works end to end"}},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.run("session.new", registry.Args{"kind": "plan", "ticket": "ENG-2231", "repos": []string{"cloud", "pricing"},
		"prompt": "I think the stale results come from the search cache never being invalidated when an index is rebuilt: confirm that before fixing it, and keep the API response shape unchanged."})
	var l *launcher
	for _, e := range s.plugins {
		if x, ok := e.(*launcher); ok {
			l = x
		}
	}
	for deadline := time.Now().Add(10 * time.Second); l.providersState().Phase != widget.Ready; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the launcher never read the providers: %+v", l.providersState())
		}
		s.frame()
		s.sh.runTodo()
	}
	s.frame()
	s.lint("the launcher")
	s.golden("launcher")
}

// AGENTS.md: a closable surface closes with Escape (from a focused field too) and a visible close; a dialog over a
// scrim closes on a click outside it.
func TestLauncherCloses(t *testing.T) {
	lw := newLocalHost(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	for _, close := range []func(){
		func() { r.typeKeys("<Esc>") },
		func() { r.clickTop("esc") },
		func() {
			r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 4, Y: 790}})
			r.win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 4, Y: 790}, Buttons: render.ButtonPrimary})
			r.win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: 4, Y: 790}, Buttons: render.ButtonPrimary})
		},
	} {
		r.typeKeys("<D-n>")
		r.latest("the launcher, asked the host", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
		close()
		r.waitGone("NEW SESSION")
	}
}

// The launcher's sandbox type: offered when this machine can run more than one backend, and the session runs in the
// one picked.
func TestLauncherPicksSandboxBackend(t *testing.T) {
	jr := newJobsRig(t, "docker", "seatbelt")
	r := startAppWith(t, t.TempDir(), "", nil, jr.connect(t))
	waitState(t, r.store, "the machine's sandboxes", func(st core.State) bool { return len(st.Machine.Sandboxes) == 2 })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.typeKeys("<D-n>")
	r.waitText("NEW SESSION")
	r.clickTop("Work")
	r.waitText("Sandbox type")
	r.waitText("container · gateway for git & gh")
	r.clickTop("SEATBELT")
	r.waitText("seatbelt sandbox · gateway for git & gh")
	r.clickTop(promptPlaceholder)
	r.typeText("Fix stale search results")
	r.clickTop("pricing")
	r.latest("the repo ticked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "1 selected") })
	r.clickTop("Start Work")
	waitState(t, r.store, "a session in seatbelt", func(st core.State) bool {
		for _, se := range st.Sessions {
			if se.Agent.Sandboxed && se.Agent.Backend == "seatbelt" {
				return true
			}
		}
		return false
	})
}

// While the providers' probe runs, the launcher shows skeleton cards and how long it's taken, picks no agent, and
// Start waits with the reason; ⌘↵ says the same. Once the probe answers, the default agent is picked and Start goes.
func TestLauncherWaitsForTheProbe(t *testing.T) {
	fake := agenttest.NewFake()
	fake.Hold = make(chan struct{})
	lw := newLocalHost(t, fake)
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.now = at(14, 30, 0)
	s.run("session.new", registry.Args{"prompt": "Fix stale search results"})
	s.frame()
	s.wantFrame(t, "NEW SESSION", "Plan") // the kinds are there at once
	s.now = s.now.Add(4 * time.Second)
	s.frame()
	s.wantFrame(t, "Checking which agents are installed…", "· 4s", "Checking agents…")
	if n := s.access("No agent"); n.Selected {
		t.Error("No agent is picked while the probe runs")
	}
	if err := s.reg.Run(context.Background(), "session.launch", nil); err == nil || err.Error() != "To start: a moment: checking which agents are installed." {
		t.Errorf("⌘↵: %v", err)
	}
	s.frame()
	s.wantFrame(t, "To start: a moment: checking which agents are installed.")

	close(fake.Hold)
	s.until(t, "the probe", func() bool { return strings.Contains(s.frameText(), "Fake") })
	s.now = s.now.Add(time.Second)
	s.frame()
	if n := s.access("Fake"); !n.Selected {
		t.Error("the logged-in provider isn't picked once the probe answers")
	}
	if text := s.frameText(); strings.Contains(text, "Checking agents…") || strings.Contains(text, "To start:") {
		t.Errorf("Start still waits after the probe:\n%s", text)
	}
}

// A probe that finds no agents says so, with Retry; No agent is there to pick, but not picked.
func TestLauncherProbeFails(t *testing.T) {
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: nopExec{}, Load: steadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { cfg.Host, cfg.Store = w, w.Store() })
	s.run("session.new", registry.Args{"prompt": "notes"})
	s.until(t, "the failure", func() bool { return strings.Contains(s.frameText(), "no agents are set up on this machine") })
	s.wantFrame(t, "Retry")
	if n := s.access("No agent"); n.Selected {
		t.Error("No agent is picked by default")
	}
	if err := s.reg.Run(context.Background(), "session.launch", nil); err == nil || err.Error() != "To start: pick an agent, or No agent." {
		t.Errorf("⌘↵: %v", err)
	}
	s.frame()
	s.wantFrame(t, "To start: pick an agent, or No agent.")
}
