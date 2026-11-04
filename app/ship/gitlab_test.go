package ship_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/integ/gitlab"
	"github.com/liamg/oriel/integ/gitlab/gitlabtest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// loadGitlab runs the GitLab plugin (integ/gitlab/plugin) as an install lays it out, its process built into bin/,
// granted what its manifest declares.
func loadGitlab(t *testing.T, s *apptest.Scene) {
	t.Helper()
	dir := hosttest.Install(t, "../../integ/gitlab/plugin", "../../integ/gitlab/cmd/oriel-gitlab")
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	p := plugin.Caller{ID: m.ID, Dir: dir, Guard: g}
	d, err := plugin.Contribute(s.Registry(), p.Dir, m.ID, m.Contributes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Dispose)
	vm := s.PluginVM(p)
	src, err := os.ReadFile(filepath.Join(p.Dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
}

// gitlabConfig is the app reaching the fake gitlab.com for the internet, with the token set.
func gitlabConfig(t *testing.T, g *gitlabtest.GitLab) func(*apptest.Config) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret(gitlab.Secret, gitlabtest.Key)
	return func(cfg *apptest.Config) { cfg.Net, cfg.Secrets = g.Client(), secrets }
}

// Against a recorded GitLab API: merge requests asking you, their state and checkout. The bundled plugin's merge
// requests list on the Reviews page beside GitHub's.
func TestGitlabReviewsOnTheReviewsPage(t *testing.T) {
	g := gitlabtest.New(t)
	g.AddMR("acme/platform/gateway", gitlabtest.MR{Title: "Rate-limit the gateway", Author: "mkhan", Source: "mk/limits", Target: "main",
		Reviewers: []string{gitlabtest.Me}})
	s := apptest.New(t, apptest.Options{Config: gitlabConfig(t, g)})
	loadGitlab(t, s)
	s.Run("reviews.open", nil)
	s.Until("the merge request", func() bool { return strings.Contains(s.Text(), "Rate-limit the gateway") })
	s.WantFrame("acme/platform/gateway", "Open in GitLab")
}

// Ship's Raise PR, its checks and its PR step work on any code host (Raise MR on GitLab), and Merge… warns
// of an unresolved discussion first, then merges at the head it showed.
func TestGitlabRaiseAndMergeFromShip(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	g := gitlabtest.New(t)
	g.AddProject("acme/billing", "merge", "default_off")
	root := t.TempDir()
	origins := filepath.Join(root, "origins", "acme")
	repo := filepath.Join(root, "billing")
	git(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(origins, "billing.git"))
	git(t, root, "init", "-q", "-b", "main", repo)
	writeFiles(t, repo, map[string]string{"invoice.go": "package billing\n"})
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "first")
	git(t, repo, "remote", "add", "origin", "https://gitlab.com/acme/billing.git")
	git(t, repo, "config", "url."+origins+"/.insteadOf", "https://gitlab.com/acme/")
	git(t, repo, "push", "-q", "origin", "main")
	git(t, repo, "fetch", "-q", "origin")
	git(t, repo, "remote", "set-head", "origin", "main")

	lw := rig.NewHostWith(t, host.Options{})
	net := gitlabConfig(t, g)
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) { apptest.Connect(lw.W)(cfg); net(cfg) }})
	loadGitlab(t, s)
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.Run("session.start", registry.Args{"kind": "work", "prompt": "Invoices", "provider": "fake", "repos": []string{repo}})
	st := s.UntilState("the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se := sess.Snapshot()
	s.Reopen()
	x := shipOf(s)
	p := se.Projects[0].Name
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})

	writeFiles(t, shipDir(se, p), map[string]string{"pdf.go": "package billing\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, p); return len(sh.Files) == 1 })
	s.Run("ship.commit", registry.Args{"project": p, "message": "feat: invoice PDFs"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, p); return sh.Commits == 1 && sh.Code != nil })
	s.Run("ship.open", nil)
	s.Until("Raise MR", func() bool { return strings.Contains(s.Text(), "Raise MR…") })

	s.Run("ship.raise", registry.Args{"project": p, "title": "feat: invoice PDFs"})
	shipSettle(s, t, "the MR", func() bool { sh, _ := x.ShipOf(se.ID, p); return sh.PR != nil })
	mrs := g.MRs("acme/billing")
	if len(mrs) != 1 || mrs[0]["source_branch"] != se.Branch || mrs[0]["target_branch"] != "main" || mrs[0]["title"] != "feat: invoice PDFs" {
		t.Fatalf("the MR on GitLab: %v", mrs)
	}
	s.WantFrame("!1 · feat: invoice PDFs", "Open on GitLab ↗")

	// Review mirroring: the reviewer's discussion is a thread of the session's; the agent's reply is a draft until
	// it's forwarded, then a note in that discussion, marked sent.
	g.SetMember("acme/billing", "priya-k", 30)
	disc := g.AddThread("acme/billing", 1, gitlabtest.Thread{Author: "priya-k", Body: "Should the PDF be cached?", Path: "pdf.go", Line: 1})
	s.Run("ship.refresh", nil)
	var tid core.ThreadID
	s.Until("the mirrored discussion", func() bool { // Lua answers on frames: no waiting on the read
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Hosted != nil && th.Hosted.Host == "gitlab" && th.Hosted.Key == disc {
				if c, ok := th.Last(); ok && c.Body == "Should the PDF be cached?" {
					tid = th.ID
				}
			}
		}
		return tid != ""
	})
	if batch := ag.WaitSent(t).Text; !strings.Contains(batch, "Should the PDF be cached?") {
		t.Fatalf("the agent was given %q", batch)
	}
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Yes: it's cached per invoice now."}); !strings.Contains(out, "drafted") {
		t.Fatalf("reply: %q", out)
	}
	if n := g.Notes(); len(n) != 0 {
		t.Fatalf("GitLab was written to before the forward: %q", n)
	}
	d := draftOf(t, s, se.ID, tid)
	s.Run("github.forward", registry.Args{"thread": string(tid)})
	s.Until("the forward", func() bool {
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		th, _ := sess.Snapshot().Thread(tid)
		for _, c := range th.Comments {
			if c.ID == d.ID && !c.Sent.IsZero() {
				return true
			}
		}
		return false
	})
	shipSettle(s, t, "the forward's step done", func() bool { return x.Doing(se.ID, p) == "" })
	if got := g.Discussion("acme/billing", 1, disc); len(got) != 2 || got[1] != "you: Yes: it's cached per invoice now." {
		t.Errorf("the discussion on GitLab: %q", got)
	}

	g.AddDiscussion("acme/billing", 1, false)
	own := 0 // the session's own open threads; the mirrored ones are GitLab's, counted as reviewer comments
	if sess, ok := s.Store().State().Session(se.ID); ok {
		for _, th := range sess.Snapshot().Threads {
			if th.Open() && th.Hosted == nil {
				own++
			}
		}
	}
	lead := "2 reviewer comments unresolved"
	if own > 0 {
		lead = fmt.Sprintf("2 reviewer comments, %s unresolved", kit.Counted(own, "Oriel thread"))
	}
	s.Run("ship.merge", registry.Args{"project": p})
	s.Until("the warning", func() bool { // the code host is Lua, which answers on frames: no waiting on the read
		f := s.Text()
		return strings.Contains(f, "MERGE !1") && strings.Contains(f, lead)
	})
	s.ClickText("Continue")
	shipSettle(s, t, "merged, and the next PR started", func() bool { p := pastPRs(s, se.ID); return len(p) == 1 && p[0].State == "merged" })
	if m := g.Merged(); len(m) != 1 || m[0].SHA != mrs[0]["sha"] {
		t.Errorf("merged %+v, want at %v", m, mrs[0]["sha"])
	}
}
