package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/integ/gitlab/gitlabtest"
	"github.com/liamg/oriel/jsonrpc"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// host is the app's side of the process: its host methods, the plugin granted what its oriel.toml declares.
type host struct {
	client  *http.Client
	secrets plugin.Secrets
}

var _ plugin.Network = host{}

func (h host) Log() *slog.Logger       { return slog.New(slog.DiscardHandler) }
func (h host) Notify(core.Notice)      {}
func (h host) Net() *http.Client       { return h.client }
func (h host) Secrets() plugin.Secrets { return h.secrets }

// serving runs Serve as the plugin's process would, and is the Lua's end of it; more are grants Oriel makes at
// run time, for a self-hosted site.
func serving(t *testing.T, h host, more ...plugin.Grant) *jsonrpc.Conn {
	t.Helper()
	m, err := plugin.LoadManifest("plugin")
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, append(m.Permissions, more...))
	p := plugin.Caller{ID: m.ID, Guard: g}
	methods := plugin.HostMethods(h)
	toProc, procIn := io.Pipe()
	procOut, fromProc := io.Pipe()
	go Serve(toProc, fromProc)
	conn := jsonrpc.NewConn(procOut, procIn, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		go func() { r.Reply(methods.Call(context.Background(), p, r.Method, r.Params)) }()
	}}, jsonrpc.Options{Version: "2.0"})
	t.Cleanup(func() { conn.Close() })
	return conn
}

func keyed(t *testing.T, g *gitlabtest.GitLab) *jsonrpc.Conn {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret(Secret, gitlabtest.Key)
	return serving(t, host{client: g.Client(), secrets: secrets})
}

// A code host's requested lists the merge requests asking you, with where you are with each (docs/api.md,
// oriel.code.host): their state and checkout (merge-requests/N/head), its token a secret.
func TestServeReviewsThroughTheHost(t *testing.T) {
	g := gitlabtest.New(t)
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	conn := serving(t, host{client: g.Client(), secrets: secrets})
	ctx := t.Context()
	asked := g.AddMR("acme/platform/gateway", gitlabtest.MR{Title: "Rate-limit the gateway", Author: "mkhan", Source: "mk/limits", Target: "main",
		Reviewers: []string{gitlabtest.Me}})
	changes := g.AddMR("acme/platform/gateway", gitlabtest.MR{Title: "Retry uploads", Author: "priya-k", Source: "pk/retry", Target: "main",
		Reviewers: []string{gitlabtest.Me}})
	g.SetReviewer("acme/platform/gateway", changes, gitlabtest.Me, "requested_changes")
	g.AddMR("acme/web", gitlabtest.MR{Title: "not mine", Author: "mkhan", Source: "x", Target: "main", Reviewers: []string{"priya-k"}})
	g.SetDiff("acme/platform/gateway", asked, "limits.go", "--- a/limits.go\n+++ b/limits.go\n@@ -1 +1,2 @@\n-a\n+b\n+c\n")
	g.SetJobs("acme/platform/gateway", asked, gitlabtest.Job{Name: "lint", Status: "success"},
		gitlabtest.Job{Name: "test", Status: "failed", Trace: "$ go test ./...\n--- FAIL: TestLimit (0.01s)\n"})

	err := conn.Call(ctx, "requested", Params{Site: Public}, nil)
	var rpc *jsonrpc.Error
	if !errors.As(err, &rpc) || rpc.Code != CodeNoKey {
		t.Fatalf("with no token: %v", err)
	}
	secrets.SetSecret(Secret, gitlabtest.Key)

	var got struct {
		Requests []registry.ReviewRequest `json:"requests"`
	}
	if err := conn.Call(ctx, "requested", Params{Site: Public}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Requests) != 2 {
		t.Fatalf("requested %+v", got.Requests)
	}
	a, c := got.Requests[0], got.Requests[1]
	if a.Repo != "acme/platform/gateway" || a.Number != asked || a.State != registry.ReviewNew || a.Author != "mkhan" || a.Asked.IsZero() ||
		a.URL != "https://gitlab.com/acme/platform/gateway/-/merge_requests/1" {
		t.Errorf("the ask %+v", a)
	}
	if c.State != registry.ReviewChanges {
		t.Errorf("changes asked for: %+v", c)
	}

	var d registry.ReviewDetail
	if err := conn.Call(ctx, "lookup", Params{Site: Public, URL: a.URL}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Base != "main" || d.Head != "mk/limits" || d.Request.Adds != 2 || d.Request.Dels != 1 || len(d.Files) != 1 || len(d.Checks) != 2 ||
		d.Checks[1].State != registry.CheckFailed || d.Checks[1].Detail != "--- FAIL: TestLimit (0.01s)" || d.Clone != "https://gitlab.com/acme/platform/gateway.git" {
		t.Errorf("detail %+v", d)
	}
	var co registry.PRCheckout
	if err := conn.Call(ctx, "checkout", Params{URL: a.URL}, &co); err != nil || co.Remote != "acme/platform/gateway" || co.Ref != "merge-requests/1/head" {
		t.Errorf("checkout %+v, %v", co, err)
	}
	for _, au := range g.Auths() {
		if au != "gitlab.com Bearer "+gitlabtest.Key && au != "gitlab.com " {
			t.Errorf("a request went as %q", au)
		}
	}
	raw, _ := json.Marshal(d)
	if strings.Contains(string(raw), gitlabtest.Key) {
		t.Error("the answer has the token in it")
	}
}

// A code host raises, edits, readies and merges, with sha set to the head we showed, so a push after you looked
// fails. GitLab's blockers: unresolved resolvable discussions, a draft, a conflict, missing approvals.
func TestServeShipThroughTheHost(t *testing.T) {
	g := gitlabtest.New(t)
	g.AddProject("acme/api", "merge", "default_on")
	conn := keyed(t, g)
	ctx := t.Context()
	repo := Repo{Remote: "https://gitlab.com/acme/api.git", Repo: "acme/api", Site: Public}

	var none struct{ PR *registry.PR }
	if err := conn.Call(ctx, "for_branch", Params{Repo: repo, Branch: "eng-1"}, &none); err != nil || none.PR != nil {
		t.Fatalf("no MR yet: %+v, %v", none.PR, err)
	}
	var pr registry.PR
	if err := conn.Call(ctx, "create", Params{Repo: repo, New: registry.NewPR{Title: "feat: x", Body: "## What", Branch: "eng-1", Base: "main", Draft: true}}, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Number != 1 || !pr.Draft || pr.Title != "feat: x" || pr.Branch != "eng-1" || pr.State != "open" {
		t.Fatalf("raised %+v", pr)
	}
	if err := conn.Call(ctx, "edit", Params{Repo: repo, PR: pr, Title: "feat: y", Body: "## What\nmore"}, &pr); err != nil {
		t.Fatal(err)
	}
	if m := g.MR("acme/api", 1); m["title"] != "Draft: feat: y" || m["description"] != "## What\nmore" {
		t.Errorf("edited a draft %v", m)
	}
	if err := conn.Call(ctx, "request_reviewers", Params{Repo: repo, PR: pr, Who: []string{"priya-k", "acme/team"}}, nil); err != nil {
		t.Fatal(err)
	}

	var bs struct{ Blockers []registry.MergeBlocker }
	g.AddDiscussion("acme/api", 1, false)
	g.AddDiscussion("acme/api", 1, true)
	g.SetStatus("acme/api", 1, "not_approved", true)
	if err := conn.Call(ctx, "blockers", Params{Repo: repo, PR: pr}, &bs); err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, b := range bs.Blockers {
		kinds = append(kinds, b.Kind)
	}
	if !slices.Equal(kinds, []string{registry.BlockThreads, registry.BlockDraft, registry.BlockConflicts, registry.BlockReviews}) ||
		bs.Blockers[0].Text != "1 reviewer comment unresolved" || !bs.Blockers[1].Hard {
		t.Fatalf("blockers %+v", bs.Blockers)
	}

	if err := conn.Call(ctx, "ready", Params{Repo: repo, PR: pr}, nil); err != nil {
		t.Fatal(err)
	}
	var now struct{ PR *registry.PR }
	if err := conn.Call(ctx, "for_branch", Params{Repo: repo, Branch: "eng-1"}, &now); err != nil || now.PR == nil || now.PR.Draft ||
		now.PR.Title != "feat: y" || !slices.Equal(now.PR.Reviewers, []string{"priya-k"}) {
		t.Fatalf("after ready %+v, %v", now.PR, err)
	}
	var ms registry.MergeMethods
	if err := conn.Call(ctx, "merge_methods", Params{Repo: repo, PR: *now.PR}, &ms); err != nil ||
		!slices.Equal(ms.Allowed, []string{"squash", "merge"}) || ms.Default != "squash" {
		t.Errorf("methods %+v, %v", ms, err)
	}

	g.SetStatus("acme/api", 1, "mergeable", false)
	seen := *now.PR
	g.Push("acme/api", 1, "moved")
	if err := conn.Call(ctx, "merge", Params{Repo: repo, PR: seen, Opts: registry.MergeOpts{Method: "squash", Head: seen.Head}}, nil); err == nil {
		t.Fatal("merged a head that had moved on")
	}
	seen.Head = "moved"
	if err := conn.Call(ctx, "merge", Params{Repo: repo, PR: seen, Opts: registry.MergeOpts{Method: "squash", Head: "moved", DeleteBranch: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if m := g.Merged(); len(m) != 1 || !m[0].Squash || !m[0].RemoveSource || m[0].SHA != "moved" {
		t.Errorf("merged %+v", m)
	}
}

func TestFailLine(t *testing.T) {
	log := "\x1b[0Ksection_start:1700000000:step_script\r\x1b[0K$ go test ./...\nok  a\n\x1b[31;1m--- FAIL: TestX (0.00s)\x1b[0;m\n"
	if got := FailLine(log); got != "--- FAIL: TestX (0.00s)" {
		t.Errorf("FailLine = %q", got)
	}
}

// Self-hosted sites: a site is { host, api, secret }; its calls go to its API with its own token, granted
// to its host at run time. A probe tells a GitLab from another server, without a token.
func TestServeASelfHostedSite(t *testing.T) {
	g := gitlabtest.New(t)
	g.AddHost("git.acme.corp")
	g.AddMR("platform/gateway", gitlabtest.MR{Title: "Rate-limit the gateway", Author: "mkhan", Source: "mk/limits", Target: "main",
		Reviewers: []string{gitlabtest.Me}})
	site := Site{Host: "git.acme.corp", API: "https://git.acme.corp/api/v4", Secret: "GITLAB_TOKEN:git.acme.corp"}
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	secrets.SetSecret(site.Secret, gitlabtest.Key)
	conn := serving(t, host{client: g.Client(), secrets: secrets},
		plugin.Grant{Cap: plugin.CapNet, Scope: []string{"git.acme.corp", "example.org"}},
		plugin.Grant{Cap: plugin.CapSecrets, Scope: []string{site.Secret + "@git.acme.corp"}})
	ctx := t.Context()

	var got struct {
		Requests []registry.ReviewRequest `json:"requests"`
	}
	if err := conn.Call(ctx, "requested", Params{Site: site}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Requests) != 1 || got.Requests[0].Repo != "platform/gateway" {
		t.Fatalf("requested %+v", got.Requests)
	}
	for _, au := range g.Auths() {
		if au != "git.acme.corp Bearer "+gitlabtest.Key {
			t.Errorf("a request went as %q", au)
		}
	}
	if err := conn.Call(ctx, "requested", Params{Site: Public}, nil); err == nil {
		t.Error("gitlab.com answered with no token of its own")
	}

	var probe struct{ GitLab bool }
	if err := conn.Call(ctx, "probe", Params{Host: "git.acme.corp"}, &probe); err != nil || !probe.GitLab {
		t.Errorf("probing a GitLab: %+v, %v", probe, err)
	}
	if err := conn.Call(ctx, "probe", Params{Host: "example.org"}, &probe); err != nil || probe.GitLab {
		t.Errorf("probing another server: %+v, %v", probe, err)
	}
}

// Review mirroring: threads answers one thread per diff discussion (its id, position, whether it's resolved, the
// reviewer's state) and one with no id for the general notes, each comment's author a member when they have
// Developer access; reply posts in the discussion, or with no thread in the conversation, and answers the note's id.
func TestServeThreadsAndReply(t *testing.T) {
	g := gitlabtest.New(t)
	conn := keyed(t, g)
	ctx := t.Context()
	iid := g.AddMR("acme/api", gitlabtest.MR{Title: "feat: x", Author: gitlabtest.Me, Source: "eng-1", Target: "main", Reviewers: []string{"priya-k"}})
	g.SetMember("acme/api", "priya-k", 30)
	g.SetReviewer("acme/api", iid, "priya-k", "approved")
	d := g.AddThread("acme/api", iid, gitlabtest.Thread{Author: "priya-k", Body: "Can this return an error?", Path: "handler.go", Line: 3})
	g.AddNote("acme/api", iid, "mkhan", "Looks good overall.")
	repo := Repo{Remote: "https://gitlab.com/acme/api.git", Repo: "acme/api", Site: Public}
	pr := registry.PR{Number: iid}

	var got struct{ Threads []registry.ReviewThread }
	if err := conn.Call(ctx, "threads", Params{Repo: repo, PR: pr}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 2 {
		t.Fatalf("threads %+v", got.Threads)
	}
	diff, general := got.Threads[0], got.Threads[1]
	if diff.ID != d || diff.Path != "handler.go" || diff.Line != 3 || diff.Resolved == nil || *diff.Resolved || diff.Reviewer != "priya-k" ||
		diff.State != registry.ReviewApproved || len(diff.Comments) != 1 || !diff.Comments[0].Member || diff.Comments[0].Body != "Can this return an error?" {
		t.Errorf("the diff thread %+v", diff)
	}
	if general.ID != "" || len(general.Comments) != 1 || general.Comments[0].Author != "mkhan" || general.Comments[0].Member {
		t.Errorf("the conversation %+v", general)
	}

	var posted struct{ ID string }
	if err := conn.Call(ctx, "reply", Params{Repo: repo, PR: pr, Thread: d, Body: "It does now."}, &posted); err != nil || posted.ID == "" {
		t.Fatalf("reply: %+v, %v", posted, err)
	}
	if got := g.Discussion("acme/api", iid, d); !slices.Equal(got, []string{"priya-k: Can this return an error?", "you: It does now."}) {
		t.Errorf("the discussion %q", got)
	}
	if err := conn.Call(ctx, "reply", Params{Repo: repo, PR: pr, Body: "Thanks!"}, &posted); err != nil {
		t.Fatal(err)
	}
	if n := g.Notes(); len(n) != 2 || n[1] != "acme/api!1 : Thanks!" {
		t.Errorf("notes %q", n)
	}
}
