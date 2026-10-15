package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/wire"
	"github.com/liamg/oriel/worker"
)

// client is a second connection to the worker, for what a test sets up there.
func (lw *localWorker) client(t *testing.T) worker.Client {
	a, b := wire.Pipe()
	lw.w.Serve(b)
	t.Cleanup(func() { a.Close(); b.Close() })
	return worker.Client{Conn: a}
}

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

// AI-4 done when: "each mode starts the right prompt pack". SE-1: "each kind starts from the launcher"; "direct never
// creates a worktree; new repo inits and registers". ui.md 04: "Mode: Plan · Work · Review PR · Tour · Job · New repo".
func TestLauncherModesStartTheirPacks(t *testing.T) {
	lw := newLocalWorker(t)
	ctx := context.Background()
	c := lw.client(t)
	if _, err := c.AddRepoPath(ctx, lw.repo); err != nil {
		t.Fatal(err)
	}
	// The repo's origin is itself, with a pull request's head for the review.
	git(t, lw.repo, "remote", "add", "origin", lw.repo)
	git(t, lw.repo, "update-ref", "refs/pull/7/head", "HEAD")
	parent, plain := t.TempDir(), t.TempDir()
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	// The test worker has no sandbox factory.
	r.do(func() error {
		return r.host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})

	prompt := func(text string) {
		r.clickTop(promptPlaceholder)
		r.typeText(text)
	}
	addRepo := func() {
		r.clickTop("+ add ▾")
		r.clickTop("pricing")
		r.latest("the repo's chip", func(f headless.Frame) bool { return strings.Contains(f.Text(), "pricing\n✕") })
	}
	for _, tc := range []struct {
		mode, pack string
		fill       func()
	}{
		{"Plan", "plan", func() { prompt("Round invoice lines"); addRepo() }},
		{"Work", "work", func() { prompt("Fix stale usage prices"); addRepo() }},
		{"Review PR", "review", func() {
			r.clickTop(prPlaceholder)
			r.typeText("https://github.com/acme/pricing/pull/7")
			addRepo()
		}},
		{"Tour", "tour", func() {
			prompt("How does rounding work?")
			addRepo()
			r.clickTop("tour: walkthrough · how the code works ▾")
			r.clickTop("demo · built and run for real")
			r.waitText("tour: demo · built and run for real")
		}},
		{"Job", "job", func() { prompt("Sweep stale deps"); addRepo() }},
		{"New repo", "newrepo", func() {
			r.clickTop(namePlaceholder)
			r.typeText("invoicer")
			r.clickTop(parentPlaceholder)
			r.typeText(parent)
		}},
		{"Direct", "direct", func() {
			r.clickTop(dirPlaceholder)
			r.typeText(plain)
		}},
		{"Scratch", "scratch", func() { prompt("notes for the offsite") }},
	} {
		r.typeKeys("<D-n>")
		r.waitText("NEW SESSION")
		r.clickTop(tc.mode)
		r.latest(tc.mode+" picked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start "+tc.mode) })
		tc.fill()
		r.clickTop("Start " + tc.mode)
		a := lw.fake.WaitStarted(t)
		if a.Spec.Pack.Name != tc.pack {
			t.Errorf("%s started the %q pack, want %q", tc.mode, a.Spec.Pack.Name, tc.pack)
		}
		r.waitGone("NEW SESSION")
	}

	st := r.store.State()
	kinds := map[core.Kind]core.SessionState{}
	for _, se := range st.Sessions {
		kinds[se.Kind] = se
	}
	if m, _ := worker.LoadManifest(kinds[core.KindTour].Dir); m.Tour == nil || m.Tour.Kind != "demo" {
		t.Errorf("the tour is %+v, want the demo picked", m.Tour)
	}
	if d := kinds[core.KindDirect]; d.Dir != plain || len(d.Projects) != 1 || d.Projects[0].Dir != plain {
		t.Errorf("direct = %+v, want the directory itself", d)
	}
	if out := git(t, lw.repo, "worktree", "list"); strings.Contains(out, plain) {
		t.Errorf("a direct session made a worktree:\n%s", out)
	}
	repos, err := c.Repos(ctx)
	if err != nil {
		t.Fatal(err)
	}
	registered := false
	for _, x := range repos {
		registered = registered || x.Path == filepath.Join(parent, "invoicer")
	}
	if !registered {
		t.Errorf("the new repo isn't registered: %+v", repos)
	}
}

// SE-1: "Register session.new and session.resume; the start view's greyed entries light up on their own". SE-3 and
// sessions.md: "The app opens on mission control", whose stand-in until SH-7 is the start view's recent sessions.
func TestStartViewListsSessions(t *testing.T) {
	lw := newLocalWorker(t)
	c := lw.client(t)
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
	lw := newLocalWorker(t)
	outside := filepath.Join(t.TempDir(), "other.go")
	os.WriteFile(outside, []byte("package other\n"), 0o644)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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
// their plans' usage, the worker, recent prompts and the pre-flight.
func TestLauncherSnapshot(t *testing.T) {
	claude, codex := agenttest.NewFake(), agenttest.NewFake()
	claude.Name, codex.Name = "claude", "codex"
	lw := newLocalWorker(t, claude, codex)
	cloud := filepath.Join(t.TempDir(), "cloud")
	git(t, filepath.Dir(cloud), "init", "-q", "-b", "main", cloud)
	git(t, cloud, "commit", "-q", "--allow-empty", "-m", "first")
	c := lw.client(t)
	for _, r := range []string{cloud, lw.repo} {
		if _, err := c.AddRepoPath(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	s.now = at(14, 30, 0)
	st := waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	ago := func(h int) time.Time { return s.now.Add(-time.Duration(h) * time.Hour) }
	for _, ev := range []core.Event{
		core.WorkerChanged{Worker: core.WorkerState{ID: st.Workers[0].ID, Name: "devbox", Connected: true, Cores: 32, CPU: .61}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "max plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .38}, {Name: "wk", Used: .71}}}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "codex", Plan: "pro plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .12}}}},
		core.SessionSynced{Session: core.SessionState{ID: "rounding", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusQuiet, Created: ago(2),
			Prompt: "Round minor units half-even, per currency, and keep the totals stable", Projects: []core.Project{{Name: "pricing", Dir: lw.repo}}}},
		core.SessionSynced{Session: core.SessionState{ID: "deps", Name: "sweep-deps", Kind: core.KindJob, Status: core.StatusQuiet, Created: ago(26),
			Prompt: "Sweep stale deps, keep major versions", Agent: core.AgentState{Provider: "codex"}}},
		core.SessionSynced{Session: core.SessionState{ID: "auth", Name: "auth-tour", Kind: core.KindTour, Status: core.StatusQuiet, Created: ago(50),
			Prompt: "Show me how auth works end to end"}},
	} {
		if err := s.store.Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.run("session.new", registry.Args{"kind": "plan", "ticket": "INF-2231", "repos": []string{"cloud", "pricing"},
		"prompt": "I think the stale prices come from the usage cache never being invalidated when a plan changes: confirm that before fixing it, and keep the API response shape unchanged."})
	var l *launcher
	for _, e := range s.exts {
		if x, ok := e.(*launcher); ok {
			l = x
		}
	}
	for deadline := time.Now().Add(10 * time.Second); len(l.kinds) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the launcher never heard from the worker: %s", l.loadErr)
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
	lw := newLocalWorker(t)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
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
		r.latest("the launcher, asked the worker", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Plan") })
		close()
		r.waitGone("NEW SESSION")
	}
}

// sessions.md, "Repos on a worker": a repo the chosen worker hasn't got says so on its chip, with Clone, which
// takes the local checkout's remote (or a URL typed there) and clones it onto the worker before the session starts.
func TestLauncherClonesAMissingRepo(t *testing.T) {
	src := filepath.Join(t.TempDir(), "src")
	devbox := newWorkerWith(t, worker.Options{ID: "devbox", Name: "devbox", ReposDir: src})
	checkout := filepath.Join(t.TempDir(), "pricing")
	bare := filepath.Join(t.TempDir(), "pricing.git")
	git(t, filepath.Dir(bare), "clone", "-q", "--bare", devbox.repo, bare)
	git(t, filepath.Dir(checkout), "clone", "-q", bare, checkout)
	r := startAppWith(t, t.TempDir(), "", nil, devbox.connect(t))
	waitState(t, r.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.do(func() error {
		return r.host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})
	r.do(func() error {
		return r.host.Run(context.Background(), "session.new", registry.Args{"kind": "work", "worker": "devbox", "repos": []string{checkout},
			"prompt": "Round half even"})
	})
	r.waitText("not on devbox:")
	r.clickTop("Start Work")
	r.waitText("To start: clone pricing onto devbox, or remove it.")
	r.clickTop("Clone")
	r.waitText(bare)
	r.clickTop("Clone at start")
	r.waitText("clones onto devbox at start:")
	r.clickTop("Start Work")
	devbox.fake.WaitStarted(t)
	want := filepath.Join(src, "pricing")
	waitState(t, r.store, "the session on the clone", func(st core.State) bool {
		return len(st.Sessions) == 1 && len(st.Sessions[0].Projects) == 1 && st.Sessions[0].Projects[0].Repo == want
	})
	repos, _ := devbox.client(t).Repos(context.Background())
	if !slices.ContainsFunc(repos, func(x core.Repo) bool { return x.Path == want && x.Remote == bare }) {
		t.Errorf("the clone isn't registered: %+v", repos)
	}
}

// The chip of a repo the worker lacks, with its Clone popover open, against a golden.
func TestLauncherCloneSnapshot(t *testing.T) {
	devbox := newWorkerWith(t, worker.Options{ID: "devbox", Name: "devbox", ReposDir: filepath.Join(t.TempDir(), "src")})
	checkout := filepath.Join(t.TempDir(), "pricing")
	git(t, filepath.Dir(checkout), "clone", "-q", devbox.repo, checkout)
	git(t, checkout, "remote", "set-url", "origin", "git@github.com:acme/pricing.git")
	s := newSceneWith(t, "", "", mockupSize, devbox.connect(t))
	waitState(t, s.store, "the worker", func(st core.State) bool { return len(st.Workers) == 1 })
	if err := s.store.Apply(core.WorkerChanged{Worker: core.WorkerState{ID: "devbox", Name: "devbox", Connected: true, Cores: 32, CPU: .61}}); err != nil {
		t.Fatal(err)
	}
	s.run("session.new", registry.Args{"kind": "work", "worker": "devbox", "repos": []string{checkout}, "prompt": "Round half even"})
	var l *launcher
	for _, e := range s.exts {
		if x, ok := e.(*launcher); ok {
			l = x
		}
	}
	for deadline := time.Now().Add(10 * time.Second); len(l.kinds) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the launcher never heard from the worker: %s", l.loadErr)
		}
		s.frame()
		s.sh.runTodo()
	}
	s.frame()
	s.frame()
	s.click(centre(s.access("Clone").Bounds))
	s.golden("launcher-clone")
	if n := s.access("Clone from"); n.Value != "git@github.com:acme/pricing.git" {
		t.Errorf("the URL is %q, not the checkout's remote", n.Value)
	}
	s.lint("the launcher's Clone popover")
}
