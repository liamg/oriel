package ship_test

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/integ/azure"
	"github.com/liamg/oriel/integ/azure/azuretest"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/plugin/hosttest"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/script"
)

// loadAzure runs the Azure DevOps plugin's init.lua (integ/azure/plugin) as an install lays it out: its files, its
// process built into bin/, granted what its manifest declares.
func loadAzure(t *testing.T, s *apptest.Scene) {
	t.Helper()
	dir := hosttest.Install(t, "../../integ/azure/plugin", "../../integ/azure/cmd/oriel-azure")
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

// azureRepo is a fixture checkout whose origin is https://dev.azure.com/acme/web/_git/<name>, answered by a bare repo
// on disk (git's url.insteadOf), with main pushed.
func azureRepo(t *testing.T, name string) string {
	t.Helper()
	root := t.TempDir()
	origins := filepath.Join(root, "origins")
	checkout := filepath.Join(root, name)
	git(t, root, "init", "-q", "--bare", "-b", "main", filepath.Join(origins, name))
	git(t, root, "init", "-q", "-b", "main", checkout)
	writeFiles(t, checkout, map[string]string{"src/usage.ts": "export const usage = 1\n"})
	git(t, checkout, "add", "-A")
	git(t, checkout, "commit", "-q", "-m", "first")
	git(t, checkout, "remote", "add", "origin", "https://dev.azure.com/acme/web/_git/"+name)
	git(t, checkout, "config", "url."+origins+"/.insteadOf", "https://dev.azure.com/acme/web/_git/")
	git(t, checkout, "push", "-q", "origin", "main")
	git(t, checkout, "fetch", "-q", "origin")
	git(t, checkout, "remote", "set-head", "origin", "main")
	return checkout
}

// Azure DevOps is a full provider: review, raise, merge. A work session on a repo on Azure DevOps raises its PR from
// Ship through the plugin, Merge… warns of an active thread (a dialog listing it, Continue (merge anyway) or Cancel)
// and Continue completes it at the head shown; the pull requests asking you list on the Reviews page beside GitHub's.
func TestAzureShipAndReviews(t *testing.T) {
	t.Setenv("GIT_AUTHOR_NAME", "You")
	t.Setenv("GIT_AUTHOR_EMAIL", "you@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "You")
	t.Setenv("GIT_COMMITTER_EMAIL", "you@example.com")
	az := azuretest.New(t)
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret(azure.Secret, azuretest.Token)
	hub := ghtest.New(t)
	lw := rig.NewHostWith(t, host.Options{GitHub: func(string, []string) gh.Client { return hub.Client() }})
	dash := azureRepo(t, "dashboard")
	s := apptest.New(t, apptest.Options{Config: func(cfg *apptest.Config) {
		apptest.Connect(lw.W)(cfg)
		cfg.Net, cfg.Secrets = az.Client(), secrets
	}})
	s.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	loadAzure(t, s)
	s.Run("session.start", registry.Args{"kind": "work", "prompt": "Weekly usage chart", "provider": "fake", "repos": []string{dash}})
	st := s.UntilState("the session in front", func(st core.State) bool { return st.Focus != "" })
	sess, _ := st.Session(st.Focus)
	se := sess.Snapshot()
	s.Follow()
	x := shipOf(s)
	ag := lw.Fake.WaitStarted(t)
	ag.WaitSent(t)
	ag.Emit(agent.TurnStart{})
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})

	writeFiles(t, shipDir(se, "dashboard"), map[string]string{"src/chart.ts": "export const chart = 1\n"})
	s.Run("ship.refresh", nil)
	shipSettle(s, t, "the file", func() bool { sh, _ := x.ShipOf(se.ID, "dashboard"); return len(sh.Files) == 1 && sh.Code != nil })
	if sh, _ := x.ShipOf(se.ID, "dashboard"); sh.Code.Name != "azure" || sh.Repo != "dashboard" {
		t.Fatalf("the project's code host: %+v, repo %q", sh.Code, sh.Repo)
	}
	s.Run("ship.commit", registry.Args{"project": "dashboard", "message": "feat: weekly chart"})
	shipSettle(s, t, "the commit", func() bool { sh, _ := x.ShipOf(se.ID, "dashboard"); return sh.Commits == 1 })
	s.Run("ship.open", nil)
	s.Until("Raise PR…", func() bool { return strings.Contains(s.Text(), "Raise PR…") })

	s.Run("ship.raise", registry.Args{"project": "dashboard", "title": "feat: weekly chart"})
	shipSettle(s, t, "the PR", func() bool { sh, _ := x.ShipOf(se.ID, "dashboard"); return sh.PR != nil })
	prs := az.PRs("acme", "web", "dashboard")
	if len(prs) != 1 || prs[0].Title != "feat: weekly chart" || prs[0].Source != "refs/heads/"+se.Branch || prs[0].Target != "refs/heads/main" {
		t.Fatalf("the PR on Azure DevOps: %+v", prs)
	}
	s.Until("the PR's number", func() bool { return strings.Contains(s.Text(), "!57 · feat: weekly chart") })

	// Review mirroring: the reviewer's thread on a line becomes the session's, the agent's draft reply is forwarded by
	// github.forward into that thread on Azure DevOps, and the draft is marked sent.
	priya := azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000002", DisplayName: "Priya K", UniqueName: "priya@acme.com"}
	az.Vote(prs[0].ID, priya, -5)
	onLine := az.AddFileThread(prs[0].ID, "src/chart.ts", 1, priya, "Can chart take the week as an argument?")
	s.Run("ship.refresh", nil)
	var tid core.ThreadID
	shipSettle(s, t, "the mirrored thread", func() bool {
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		for _, th := range sess.Snapshot().Threads {
			if th.Origin == core.OriginCodeHost && th.Hosted != nil && th.Hosted.Host == "azure" && th.Hosted.Key == strconv.Itoa(onLine) {
				tid = th.ID
			}
		}
		return tid != ""
	})
	if batch := ag.WaitSent(t).Text; !strings.Contains(batch, "Can chart take the week as an argument?") {
		t.Fatalf("the agent was given %q", batch)
	}
	if out := ag.Call(t, "reply", map[string]any{"thread": string(tid), "body": "Done: chart(week) now."}); !strings.Contains(out, "drafted") {
		t.Errorf("reply: %q", out)
	}
	rig.Answered(t, s.Store(), ag)
	ag.Emit(agent.TurnEnd{})
	if cs := az.Threads(prs[0].ID)[onLine-1].Comments; len(cs) != 1 {
		t.Fatalf("Azure DevOps was written to before the forward: %+v", cs)
	}
	if err := s.Host().Run(context.Background(), "github.forward", registry.Args{"thread": string(tid)}); err != nil {
		t.Fatal(err)
	}
	shipSettle(s, t, "the reply on Azure DevOps", func() bool {
		cs := az.Threads(prs[0].ID)[onLine-1].Comments
		return len(cs) == 2 && cs[1].Content == "Done: chart(week) now." && cs[1].Author.ID == azuretest.Me.ID
	})
	shipSettle(s, t, "the draft marked sent", func() bool {
		st := s.Store().State()
		sess, _ := st.Session(se.ID)
		th, _ := sess.Snapshot().Thread(tid)
		for _, c := range th.Comments {
			if c.Body == "Done: chart(week) now." && !c.Sent.IsZero() && c.HostID == strconv.Itoa(onLine)+"/2" {
				return true
			}
		}
		return false
	})
	az.Resolve(prs[0].ID, onLine, "fixed")

	az.AddThread(prs[0].ID, "active", false)
	s.Run("ship.merge", registry.Args{"project": "dashboard"})
	shipSettle(s, t, "the warning", func() bool { return strings.Contains(s.Text(), "1 reviewer comment") })
	s.WantFrame("MERGE !57", "Continue", "Cancel")
	s.ClickText("Continue")
	shipSettle(s, t, "merged, and the next PR started", func() bool { p := pastPRs(s, se.ID); return len(p) == 1 && p[0].State == "merged" })
	if how, _ := az.Completed(prs[0].ID); how != "squash" {
		t.Errorf("completed with %q, want squash", how)
	}

	az.AddPR("acme", "web", "api", azure.PullRequest{Title: "Retry uploads with jitter", CreatedBy: priya, Source: "refs/heads/retry",
		Target: "refs/heads/main", Reviewers: []azure.Reviewer{{Identity: azuretest.Me}}})
	s.Run("reviews.open", nil)
	s.Until("the request", func() bool { return strings.Contains(s.Text(), "Retry uploads with jitter") })
	s.WantFrame("web/api#58", "Priya K")

	// An organization none of your repos is in is named in the plugin's own setting.
	if err := s.Registry().Options().Set("plugin.azure-devops.organizations", registry.LayerUser, registry.At{}, []string{"contoso"}); err != nil {
		t.Fatal(err)
	}
	az.AddPR("contoso", "apps", "mobile", azure.PullRequest{Title: "Offline mode for the field app", CreatedBy: priya, Source: "refs/heads/offline",
		Target: "refs/heads/main", Reviewers: []azure.Reviewer{{Identity: azuretest.Me}}})
	s.Run("reviews.refresh", nil)
	s.Until("contoso's request", func() bool { return strings.Contains(s.Text(), "Offline mode for the field app") })
}
