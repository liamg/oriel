package sessions_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/sessions"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// Each mode starts the right prompt pack, and each kind starts from the launcher or session.start; direct never
// creates a worktree; new repo inits and registers. The launcher's modes (Work · Plan · Review PR) are the kinds marked
// Launcher, Work first and picked; Job, New repo and Direct start through session.start.
func TestLauncherModesStartTheirPacks(t *testing.T) {
	lw := rig.NewHost(t)
	ctx := context.Background()
	c := lw.W
	if _, err := c.AddRepoPath(ctx, lw.Repo); err != nil {
		t.Fatal(err)
	}
	// The repo's origin is itself, with a pull request's head for the review.
	rig.Git(t, lw.Repo, "remote", "add", "origin", lw.Repo)
	rig.Git(t, lw.Repo, "update-ref", "refs/pull/7/head", "HEAD")
	parent, plain := t.TempDir(), t.TempDir()
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	// The test host has no sandbox factory.
	r.Do(func() error {
		return r.Host.Options().Set("sandbox.default", registry.LayerUser, registry.At{}, "host")
	})

	prompt := func(text string) {
		r.ClickTop(sessions.PromptPlaceholder)
		r.TypeText(text)
	}
	addRepo := func() {
		r.ClickTop("pricing")
		r.Latest("the repo ticked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "1 selected") })
	}
	started := func(mode, pack string) {
		t.Helper()
		a := lw.Fake.WaitStarted(t)
		if a.Spec.Pack.Name != pack {
			t.Errorf("%s started the %q pack, want %q", mode, a.Spec.Pack.Name, pack)
		}
		r.UntilState("the "+mode+" session in front", func(st core.State) bool {
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
			r.ClickTop(sessions.PRPlaceholder)
			r.TypeText("https://github.com/acme/pricing/pull/7")
			addRepo()
		}},
	} {
		// Start lands on the session's first request, where ⌘N is a new request: the launcher opens by its command.
		if err := r.Do(func() error { return r.Host.Run(context.Background(), "session.new", nil) }); err != nil {
			t.Fatal(err)
		}
		f := r.Latest("the launcher", func(f headless.Frame) bool { return strings.Contains(f.Text(), "NEW SESSION") })
		if i == 0 {
			if !strings.Contains(f.Text(), "Start Work") {
				t.Errorf("the launcher doesn't open on Work:\n%s", f.Text())
			}
			modes := launcherModes(r.Win.Access())
			if want := []string{"Work", "Plan", "Review PR"}; strings.Join(modes, " · ") != strings.Join(want, " · ") {
				t.Errorf("the launcher's modes are %v, want %v", modes, want)
			}
		}
		r.ClickTop(tc.mode)
		r.Latest(tc.mode+" picked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start "+tc.mode) })
		tc.fill()
		r.ClickTop("Start " + tc.mode)
		r.WaitGone("NEW SESSION")
		started(tc.mode, tc.pack)
	}

	// The kinds the launcher doesn't offer still start through session.start.
	for _, tc := range []struct {
		pack string
		args registry.Args
	}{
		{"job", registry.Args{"kind": "job", "prompt": "Sweep stale deps", "repos": []string{lw.Repo}}},
		{"newrepo", registry.Args{"kind": "newrepo", "name": "invoicer", "dir": parent}},
		{"direct", registry.Args{"kind": "direct", "dir": plain}},
	} {
		tc.args["provider"] = "fake"
		if err := r.Do(func() error { return r.Host.Run(context.Background(), "session.start", tc.args) }); err != nil {
			t.Fatalf("session.start %v: %v", tc.args, err)
		}
		started(tc.pack, tc.pack)
	}

	st := r.Store.State()
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
	if out := rig.Git(t, lw.Repo, "worktree", "list"); strings.Contains(out, plain) {
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
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.TypeKeys("<D-n>")
	r.Latest("the launcher on Work", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
	r.ClickTop(sessions.PromptPlaceholder)
	const url = "https://github.com/acme/pricing/pull/7"
	r.Win.SetClipboard(url)
	paste := render.ModCtrl
	if runtime.GOOS == "darwin" {
		paste = render.ModSuper
	}
	r.Win.Send(render.KeyEvent{Key: "v", Mods: paste})
	r.Latest("Review PR picked, with the link", func(f headless.Frame) bool {
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

// With a session host, the start view's session entries light up on their own, and its recents list the sessions.
func TestStartViewListsSessions(t *testing.T) {
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.WaitText("Open Directory") // one there at launch is landed on, past the start view
	if _, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "pricing-rounding", Repos: []core.RepoID{core.RepoID(lw.Repo)}}); err != nil {
		t.Fatal(err)
	}
	r.WaitFrame("the session under Recent", func(s string) bool {
		return apptest.HasAll(s, "SESSIONS", "pricing-rounding", "work · pricing") && !strings.Contains(s, "SOON")
	})
	r.ClickTop("pricing-rounding")
	st := r.UntilState("the session in front", func(st core.State) bool { return st.Focus == "pricing-rounding" })
	se, _ := st.Session("pricing-rounding")
	p, _ := se.Snapshot().Primary()
	r.WaitGone("SESSIONS")
	var dir string
	r.Do(func() error { dir = r.Host.Here().Project; return nil })
	if dir != p.Dir {
		t.Errorf("the window is on %q, want the session's worktree %q", dir, p.Dir)
	}
}

// Files outside the session's worktrees (the stdlib, dependencies, another repo) open read-only.
func TestFilesOutsideTheSessionOpenReadOnly(t *testing.T) {
	lw := rig.NewHost(t)
	outside := filepath.Join(t.TempDir(), "other.go")
	os.WriteFile(outside, []byte("package other\n"), 0o644)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if err := r.Do(func() error {
		return r.Host.Run(context.Background(), "session.start", registry.Args{"kind": "work", "name": "w", "repos": []string{lw.Repo}})
	}); err != nil {
		t.Fatal(err)
	}
	st := r.UntilState("the session in front", func(st core.State) bool { return st.Focus == "w" })
	se, _ := st.Session("w")
	p, _ := se.Snapshot().Primary()
	inside := filepath.Join(p.Dir, "round.go")
	os.WriteFile(inside, []byte("package pricing\n"), 0o644)
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		var dir string
		r.Do(func() error { dir = r.Host.Here().Project; return nil })
		if dir == p.Dir {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window is on %q, not the worktree", dir)
		}
	}

	open := func(path string) {
		t.Helper()
		if err := r.Do(func() error { return r.Host.Run(context.Background(), "editor.open", registry.Args{"path": path}) }); err != nil {
			t.Fatal(err)
		}
	}
	open(outside)
	r.WaitText("read-only · outside the session")
	open(inside)
	r.WaitFrame("round.go", func(s string) bool { return strings.Contains(s, "package\n \npricing") })
	time.Sleep(200 * time.Millisecond)
	if fs := r.Win.Frames(); strings.Contains(fs[len(fs)-1].Text(), "read-only ·") {
		t.Errorf("a file in the session's worktree opened read-only:\n%s", fs[len(fs)-1].Text())
	}
}

// The launcher over the start view, a plan from a ticket on two repos, Claude and Codex with
// their plans' usage, recent prompts and the pre-flight.
func TestLauncherSnapshot(t *testing.T) {
	claude, codex := agenttest.NewFake(), agenttest.NewFake()
	claude.Name, codex.Name = "claude", "codex"
	lw := rig.NewHost(t, claude, codex)
	cloud := filepath.Join(t.TempDir(), "cloud")
	rig.Git(t, filepath.Dir(cloud), "init", "-q", "-b", "main", cloud)
	rig.Git(t, cloud, "commit", "-q", "--allow-empty", "-m", "first")
	c := lw.W
	for _, r := range []string{cloud, lw.Repo} {
		if _, err := c.AddRepoPath(context.Background(), r); err != nil {
			t.Fatal(err)
		}
	}
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = apptest.At(14, 30, 0)
	rig.WaitState(t, s.Store(), "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	ago := func(h int) time.Time { return s.Now.Add(-time.Duration(h) * time.Hour) }
	for _, ev := range []core.Event{
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "claude", Plan: "max plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .38}, {Name: "wk", Used: .71}}}},
		core.UsageSampled{Usage: core.ProviderUsage{Provider: "codex", Plan: "pro plan", Windows: []core.UsageWindow{{Name: "5h window", Used: .12}}}},
		core.SessionSynced{Session: core.SessionState{ID: "rounding", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusQuiet, Created: ago(2),
			Prompt: "Round minor units half-even, per currency, and keep the totals stable", Projects: []core.Project{{Name: "pricing", Dir: lw.Repo}}}},
		core.SessionSynced{Session: withData(core.SessionState{ID: "deps", Name: "sweep-deps", Kind: core.KindJob, Status: core.StatusQuiet, Created: ago(26),
			Prompt: "Sweep stale deps, keep major versions", Agent: core.AgentState{Provider: "codex"}}, core.JobData{Job: "sweep-deps"})},
		core.SessionSynced{Session: core.SessionState{ID: "auth", Name: "auth-review", Kind: core.KindReview, Status: core.StatusQuiet, Created: ago(50),
			Prompt: "Show me how auth works end to end"}},
	} {
		if err := s.Store().Apply(ev); err != nil {
			t.Fatal(err)
		}
	}
	s.Run("session.new", registry.Args{"kind": "plan", "ticket": "ENG-2231", "repos": []string{"cloud", "pricing"},
		"prompt": "I think the stale results come from the search cache never being invalidated when an index is rebuilt: confirm that before fixing it, and keep the API response shape unchanged."})
	reading := func() bool {
		return s.Find("Checking which agents are installed") != nil || strings.Contains(s.Text(), "Checking agents")
	}
	for deadline := time.Now().Add(10 * time.Second); reading(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("the launcher never read the providers:\n%s", s.Text())
		}
		s.Frame()
		s.RunQueued()
	}
	s.SettleStats()
	s.Frame()
	s.Lint("the launcher")
	s.Golden("launcher")
}

// AGENTS.md: a closable surface closes with Escape (from a focused field too) and a visible close; a dialog over a
// scrim closes on a click outside it.
func TestLauncherCloses(t *testing.T) {
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	for _, close := range []func(){
		func() { r.TypeKeys("<Esc>") },
		func() { r.ClickTop("esc") },
		func() {
			r.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: 4, Y: 790}})
			r.Win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: render.Point{X: 4, Y: 790}, Buttons: render.ButtonPrimary})
			r.Win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: render.Point{X: 4, Y: 790}, Buttons: render.ButtonPrimary})
		},
	} {
		r.TypeKeys("<D-n>")
		r.Latest("the launcher, asked the host", func(f headless.Frame) bool { return strings.Contains(f.Text(), "Start Work") })
		close()
		r.WaitGone("NEW SESSION")
	}
}

// The launcher's sandbox type: offered when this machine can run more than one backend, and the session runs in the
// one picked.
func TestLauncherPicksSandboxBackend(t *testing.T) {
	jr := rig.NewJobs(t, "docker", "seatbelt")
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: apptest.Connect(jr.W)})
	r.UntilState("the machine's sandboxes", func(st core.State) bool { return len(st.Machine.Sandboxes) == 2 })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })
	r.TypeKeys("<D-n>")
	r.WaitText("NEW SESSION")
	r.ClickTop("Work")
	r.WaitText("Sandbox type")
	r.WaitText("container · gateway for git & gh")
	r.ClickTop("SEATBELT")
	r.WaitText("seatbelt sandbox · gateway for git & gh")
	r.ClickTop(sessions.PromptPlaceholder)
	r.TypeText("Fix stale search results")
	r.ClickTop("pricing")
	r.Latest("the repo ticked", func(f headless.Frame) bool { return strings.Contains(f.Text(), "1 selected") })
	r.ClickTop("Start Work")
	r.UntilState("a session in seatbelt", func(st core.State) bool {
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
	lw := rig.NewHost(t, fake)
	s := apptest.New(t, apptest.Options{Config: apptest.Connect(lw.W)})
	s.Now = apptest.At(14, 30, 0)
	s.Run("session.new", registry.Args{"prompt": "Fix stale search results"})
	s.Frame()
	s.WantFrame("NEW SESSION", "Plan") // the kinds are there at once
	s.Now = s.Now.Add(4 * time.Second)
	s.Frame()
	s.WantFrame("Checking which agents are installed…", "· 4s", "Checking agents…")
	if n := s.Node("No agent"); n.Selected {
		t.Error("No agent is picked while the probe runs")
	}
	if err := s.Registry().Run(context.Background(), "session.launch", nil); err == nil || err.Error() != "To start: a moment: checking which agents are installed." {
		t.Errorf("⌘↵: %v", err)
	}
	s.Frame()
	s.WantFrame("To start: a moment: checking which agents are installed.")

	close(fake.Hold)
	s.Until("the probe", func() bool { return strings.Contains(s.Text(), "Fake") })
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	if n := s.Node("Fake"); !n.Selected {
		t.Error("the logged-in provider isn't picked once the probe answers")
	}
	if text := s.Text(); strings.Contains(text, "Checking agents…") || strings.Contains(text, "To start:") {
		t.Errorf("Start still waits after the probe:\n%s", text)
	}
}

// A probe that finds no agents says so, with Retry; No agent is there to pick, but not picked.
func TestLauncherProbeFails(t *testing.T) {
	w, err := host.New(host.Options{Home: t.TempDir(), Providers: &agent.Providers{}, Exec: rig.NopExec{}, Load: rig.SteadyLoad})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { cfg.Host, cfg.Store = w, w.Store() }})
	s.Run("session.new", registry.Args{"prompt": "notes"})
	s.Until("the failure", func() bool { return strings.Contains(s.Text(), "no agents are set up on this machine") })
	s.WantFrame("Retry")
	if n := s.Node("No agent"); n.Selected {
		t.Error("No agent is picked by default")
	}
	if err := s.Registry().Run(context.Background(), "session.launch", nil); err == nil || err.Error() != "To start: pick an agent, or No agent." {
		t.Errorf("⌘↵: %v", err)
	}
	s.Frame()
	s.WantFrame("To start: pick an agent, or No agent.")
}

// withData is se with its kind's data.
func withData(se core.SessionState, d core.KindData) core.SessionState {
	se.SetData(d)
	return se
}

// The default agent is the last session's, while its provider is there; else installed, then logged in, then by name.
func TestDefaultAgent(t *testing.T) {
	models := []agent.Model{{ID: "big", Default: true}, {ID: "small"}}
	claude := host.ProviderInfo{ID: "claude", Install: agent.Install{Bin: "claude"}, Models: models}
	codex := host.ProviderInfo{ID: "codex", Install: agent.Install{Bin: "codex", LoggedIn: true}, Models: models}
	gemini := host.ProviderInfo{ID: "gemini", Error: "not installed"}
	aider := host.ProviderInfo{ID: "aider", Error: "not installed"}
	session := func(provider, model string, ago time.Duration) core.SessionState {
		return core.SessionState{ID: core.SessionID(provider + model), Created: time.Now().Add(-ago), Agent: core.AgentState{Provider: provider, Model: model}}
	}
	for _, tt := range []struct {
		name            string
		sessions        []core.SessionState
		ps              []host.ProviderInfo
		provider, model string
	}{
		{"last session's agent and model", []core.SessionState{session("codex", "big", time.Hour), session("claude", "small", time.Minute)}, []host.ProviderInfo{codex, claude}, "claude", "small"},
		{"last session's model gone", []core.SessionState{session("claude", "old", time.Minute)}, []host.ProviderInfo{codex, claude}, "claude", "big"},
		{"not a job's", []core.SessionState{session("claude", "big", time.Hour), withData(core.SessionState{ID: "job", Kind: core.KindJob, Created: time.Now(),
			Agent: core.AgentState{Provider: "codex"}}, core.JobData{Job: "sweep"})}, []host.ProviderInfo{codex, claude}, "claude", "big"},
		{"last session's provider gone", []core.SessionState{session("gone", "x", time.Minute)}, []host.ProviderInfo{claude, codex}, "codex", "big"},
		{"logged in over installed", nil, []host.ProviderInfo{gemini, claude, codex}, "codex", "big"},
		{"installed over not", nil, []host.ProviderInfo{gemini, claude}, "claude", "big"},
		{"by name", nil, []host.ProviderInfo{gemini, aider}, "aider", ""},
		{"none", nil, nil, "", ""},
	} {
		st := core.State{Sessions: tt.sessions}
		if p, m := kit.DefaultAgent(st, tt.ps); p != tt.provider || m != tt.model {
			t.Errorf("%s: %s %s, want %s %s", tt.name, p, m, tt.provider, tt.model)
		}
	}
}
