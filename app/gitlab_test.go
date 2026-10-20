package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
func loadGitlab(t *testing.T, s *errScene) {
	t.Helper()
	dir := hosttest.Install(t, "../integ/gitlab/plugin", "../integ/gitlab/cmd/oriel-gitlab")
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	p := plugin.Caller{ID: m.ID, Dir: dir, Guard: g}
	d, err := plugin.Contribute(s.reg, p.Dir, m.ID, m.Contributes)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Dispose)
	vm := s.pluginVM(t, p)
	src, err := os.ReadFile(filepath.Join(p.Dir, "init.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: src}); err != nil {
		t.Fatal(err)
	}
	s.frame()
}

// gitlabConfig is the app reaching the fake gitlab.com for the internet, with the token set.
func gitlabConfig(t *testing.T, g *gitlabtest.GitLab) func(*Config) {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret(gitlab.Secret, gitlabtest.Key)
	return func(cfg *Config) { cfg.Net, cfg.Secrets = g.Client(), secrets }
}

// RV-11 done when: "against a recorded GitLab API: merge requests asking you, their state and checkout". The bundled
// plugin's merge requests list on the Reviews page beside GitHub's.
func TestGitlabReviewsOnTheReviewsPage(t *testing.T) {
	g := gitlabtest.New(t)
	g.AddMR("acme/platform/gateway", gitlabtest.MR{Title: "Rate-limit the gateway", Author: "mkhan", Source: "mk/limits", Target: "main",
		Reviewers: []string{gitlabtest.Me}})
	s := newSceneWith(t, "", "", mockupSize, gitlabConfig(t, g))
	loadGitlab(t, s)
	s.run("reviews.open", nil)
	s.until(t, "the merge request", func() bool { return strings.Contains(s.frameText(), "Rate-limit the gateway") })
	s.wantFrame(t, "acme/platform/gateway", "Open in GitLab")
}

// hosts.md: "Ship's Raise PR, its checks and its PR step work on any of them: Raise MR on GitLab", and Merge… warns
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

	lw := newHostWith(t, host.Options{})
	net := gitlabConfig(t, g)
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) { lw.connect(t)(cfg); net(cfg) })
	loadGitlab(t, s)
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	s.run("session.start", registry.Args{"kind": "work", "prompt": "Invoices", "provider": "fake", "repos": []string{repo}})
	st := waitState(t, s.store, "the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se := sess.Snapshot()
	s.reopen()
	x := s.ship()
	p := se.Projects[0].Name

	writeFiles(t, shipDir(se, p), map[string]string{"pdf.go": "package billing\n"})
	s.run("ship.refresh", nil)
	s.shipSettle(t, "the file", func() bool { sh, _ := x.ship(se.ID, p); return len(sh.Files) == 1 })
	s.run("ship.commit", registry.Args{"project": p, "message": "feat: invoice PDFs"})
	s.shipSettle(t, "the commit", func() bool { sh, _ := x.ship(se.ID, p); return sh.Commits == 1 && sh.Code != nil })
	s.run("ship.open", nil)
	s.until(t, "Raise MR", func() bool { return strings.Contains(s.frameText(), "Raise MR…") })

	s.run("ship.raise", registry.Args{"project": p, "title": "feat: invoice PDFs"})
	s.shipSettle(t, "the MR", func() bool { sh, _ := x.ship(se.ID, p); return sh.PR != nil })
	mrs := g.MRs("acme/billing")
	if len(mrs) != 1 || mrs[0]["source_branch"] != se.Branch || mrs[0]["target_branch"] != "main" || mrs[0]["title"] != "feat: invoice PDFs" {
		t.Fatalf("the MR on GitLab: %v", mrs)
	}
	s.wantFrame(t, "!1 · feat: invoice PDFs", "Open on GitLab ↗")

	// Review mirroring: the reviewer's discussion is a thread of the session's; the agent's reply is a draft until
	// it's forwarded, then a note in that discussion, marked sent.
	ag := lw.fake.WaitStarted(t)
	g.SetMember("acme/billing", "priya-k", 30)
	disc := g.AddThread("acme/billing", 1, gitlabtest.Thread{Author: "priya-k", Body: "Should the PDF be cached?", Path: "pdf.go", Line: 1})
	s.run("ship.refresh", nil)
	var tid core.ThreadID
	s.until(t, "the mirrored discussion", func() bool { // Lua answers on frames: no waiting on the read
		st := s.store.State()
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
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Yes: it's cached per invoice now."}); !strings.Contains(out, "drafted") {
		t.Fatalf("reply: %q", out)
	}
	if n := g.Notes(); len(n) != 0 {
		t.Fatalf("GitLab was written to before the forward: %q", n)
	}
	d := draftOf(t, s, se.ID, tid)
	s.run("github.forward", registry.Args{"thread": string(tid)})
	s.until(t, "the forward", func() bool {
		st := s.store.State()
		sess, _ := st.Session(se.ID)
		th, _ := sess.Snapshot().Thread(tid)
		for _, c := range th.Comments {
			if c.ID == d.ID && !c.Sent.IsZero() {
				return true
			}
		}
		return false
	})
	if got := g.Discussion("acme/billing", 1, disc); len(got) != 2 || got[1] != "you: Yes: it's cached per invoice now." {
		t.Errorf("the discussion on GitLab: %q", got)
	}

	g.AddDiscussion("acme/billing", 1, false)
	own := 0 // the session's own open threads; the mirrored ones are GitLab's, counted as reviewer comments
	if sess, ok := s.store.State().Session(se.ID); ok {
		for _, th := range sess.Snapshot().Threads {
			if th.Open() && th.Hosted == nil {
				own++
			}
		}
	}
	lead := "2 reviewer comments unresolved"
	if own > 0 {
		lead = fmt.Sprintf("2 reviewer comments, %s unresolved", counted(own, "Oriel thread"))
	}
	s.run("ship.merge", registry.Args{"project": p})
	s.until(t, "the warning", func() bool { // the code host is Lua, which answers on frames: no waiting on the read
		f := s.frameText()
		return strings.Contains(f, "MERGE !1") && strings.Contains(f, lead)
	})
	s.clickText(t, "Continue")
	s.shipSettle(t, "merged, and the next PR started", func() bool { p := pastPRs(s, se.ID); return len(p) == 1 && p[0].State == "merged" })
	if m := g.Merged(); len(m) != 1 || m[0].SHA != mrs[0]["sha"] {
		t.Errorf("merged %+v, want at %v", m, mrs[0]["sha"])
	}
}
