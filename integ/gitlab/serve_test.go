package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	go func() {
		if err := Serve(toProc, fromProc); err != nil {
			t.Error(err)
		}
	}()
	conn := jsonrpc.NewConn(procOut, procIn, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		go func() {
			// A call the test stopped waiting for is answered on a connection its cleanup closed.
			if err := r.Reply(methods.Call(context.Background(), p, r.Method, r.Params)); err != nil && !errors.Is(err, jsonrpc.ErrClosed) {
				t.Error(err)
			}
		}()
	}}, jsonrpc.Options{Version: "2.0"})
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func keyed(t *testing.T, g *gitlabtest.GitLab) *jsonrpc.Conn {
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	if err := secrets.SetSecret(Secret, gitlabtest.Key); err != nil {
		t.Fatal(err)
	}
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
	if err := secrets.SetSecret(Secret, gitlabtest.Key); err != nil {
		t.Fatal(err)
	}

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
	if err := secrets.SetSecret(site.Secret, gitlabtest.Key); err != nil {
		t.Fatal(err)
	}
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

// Each note's award emoji come with it in threads: GitLab's names as emoji (one with no emoji, a custom one, as
// :name:), how many, whether you're one of them and who. react_note awards one to a note as you, or takes yours
// back.
func TestServeReactions(t *testing.T) {
	g := gitlabtest.New(t)
	conn := keyed(t, g)
	ctx := t.Context()
	iid := g.AddMR("acme/api", gitlabtest.MR{Title: "feat: x", Author: gitlabtest.Me, Source: "eng-1", Target: "main"})
	g.AddThread("acme/api", iid, gitlabtest.Thread{Author: "priya-k", Body: "Can this return an error?", Path: "handler.go", Line: 3})
	g.AddNote("acme/api", iid, "mkhan", "Looks good overall.")
	repo := Repo{Remote: "https://gitlab.com/acme/api.git", Repo: "acme/api", Site: Public}
	pr := registry.PR{Number: iid}
	threads := func() (diff, general registry.ReviewNote) {
		t.Helper()
		var got struct{ Threads []registry.ReviewThread }
		if err := conn.Call(ctx, "threads", Params{Repo: repo, PR: pr}, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Threads) != 2 {
			t.Fatalf("threads %+v", got.Threads)
		}
		return got.Threads[0].Comments[0], got.Threads[1].Comments[0]
	}
	diff, general := threads()
	if diff.Reactions != nil || general.Reactions != nil {
		t.Errorf("reactions on notes with none: %+v, %+v", diff.Reactions, general.Reactions)
	}
	line, _ := strconv.Atoi(diff.ID)
	g.Award("acme/api", iid, line, "priya-k", "party_parrot")
	g.Award("acme/api", iid, line, "mkhan", "fire")
	g.Award("acme/api", iid, line, "mkhan", "thumbsup")
	g.Award("acme/api", iid, line, gitlabtest.Me, "thumbsup")

	diff, _ = threads()
	want := []registry.PRReaction{{Emoji: "👍", Count: 2, Mine: true, Who: []string{"mkhan", gitlabtest.Me}},
		{Emoji: ":party_parrot:", Count: 1, Who: []string{"priya-k"}}, {Emoji: "🔥", Count: 1, Who: []string{"mkhan"}}}
	if !reflect.DeepEqual(diff.Reactions, want) {
		t.Errorf("the diff note's reactions %+v", diff.Reactions)
	}

	react := func(note, emoji string, on bool) error {
		var ok struct{ OK bool }
		return conn.Call(ctx, "react_note", Params{Repo: repo, PR: pr, Note: note, Emoji: emoji, On: on}, &ok)
	}
	for _, r := range []struct {
		note, emoji string
		on          bool
	}{{general.ID, "🎉", true}, {general.ID, "🎉", true}, {diff.ID, "👍", false}, {diff.ID, "🔥", true}, {diff.ID, ":party_parrot:", true}} {
		if err := react(r.note, r.emoji, r.on); err != nil {
			t.Fatalf("react_note %+v: %v", r, err)
		}
	}
	conv, _ := strconv.Atoi(general.ID)
	if got := g.Awards("acme/api", iid, conv); !reflect.DeepEqual(got, map[string][]string{"tada": {gitlabtest.Me}}) {
		t.Errorf("the conversation note's awards after 🎉 twice: %v", got)
	}
	if got := g.Awards("acme/api", iid, line); !reflect.DeepEqual(got, map[string][]string{"thumbsup": {"mkhan"},
		"fire": {"mkhan", gitlabtest.Me}, "party_parrot": {"priya-k", gitlabtest.Me}}) {
		t.Errorf("the diff note's awards: %v", got)
	}
	if err := react(diff.ID, "🦄", true); err == nil {
		t.Error("an emoji Oriel has no GitLab name for was taken")
	}
}

// An MR's awards are read in one GraphQL query per 100 notes, who you are with them, rather than a request for each
// note: a note past the first 100 still has its own.
func TestServeReactionsInPages(t *testing.T) {
	g := gitlabtest.New(t)
	conn := keyed(t, g)
	iid := g.AddMR("acme/api", gitlabtest.MR{Title: "feat: x", Author: gitlabtest.Me, Source: "eng-1", Target: "main"})
	g.AddThread("acme/api", iid, gitlabtest.Thread{Author: "priya-k", Body: "Can this return an error?", Path: "handler.go", Line: 3})
	for range 150 {
		g.AddNote("acme/api", iid, "mkhan", "Any news?")
	}
	var got struct{ Threads []registry.ReviewThread }
	read := func() {
		t.Helper()
		g.ClearCalls()
		if err := conn.Call(t.Context(), "threads", Params{Repo: Repo{Remote: "https://gitlab.com/acme/api.git", Repo: "acme/api", Site: Public},
			PR: registry.PR{Number: iid}}, &got); err != nil {
			t.Fatal(err)
		}
		if len(got.Threads) != 2 || len(got.Threads[1].Comments) != 150 {
			t.Fatalf("threads %+v", got.Threads)
		}
	}
	read()
	last := got.Threads[1].Comments[149]
	first := got.Threads[0].Comments[0]
	n, _ := strconv.Atoi(last.ID)
	g.Award("acme/api", iid, n, gitlabtest.Me, "rocket")
	d, _ := strconv.Atoi(first.ID)
	g.Award("acme/api", iid, d, "priya-k", "eyes")

	read()
	if r := got.Threads[1].Comments[149].Reactions; !reflect.DeepEqual(r, []registry.PRReaction{{Emoji: "🚀", Count: 1, Mine: true, Who: []string{gitlabtest.Me}}}) {
		t.Errorf("the 150th note's reactions: %+v", r)
	}
	if r := got.Threads[0].Comments[0].Reactions; !reflect.DeepEqual(r, []registry.PRReaction{{Emoji: "👀", Count: 1, Who: []string{"priya-k"}}}) {
		t.Errorf("the diff note's reactions: %+v", r)
	}
	var awards, graphql, user int
	for _, c := range g.Calls() {
		switch {
		case strings.Contains(c, "/award_emoji"):
			awards++
		case c == "POST /api/graphql":
			graphql++
		case c == "GET /api/v4/user":
			user++
		}
	}
	if awards != 0 || user != 0 || graphql != 2 {
		t.Errorf("reading 151 notes' awards took %d award requests, %d of /user and %d GraphQL queries, want 0, 0 and 2:\n%s",
			awards, user, graphql, strings.Join(g.Calls(), "\n"))
	}
}

// Awards that can't be read leave the notes without reactions; the threads still come.
func TestServeThreadsWithoutAwards(t *testing.T) {
	g := gitlabtest.New(t)
	conn := keyed(t, g)
	iid := g.AddMR("acme/api", gitlabtest.MR{Title: "feat: x", Author: gitlabtest.Me, Source: "eng-1", Target: "main"})
	g.AddNote("acme/api", iid, "mkhan", "Looks good overall.")
	g.RefuseGraphQL()
	var got struct{ Threads []registry.ReviewThread }
	if err := conn.Call(t.Context(), "threads", Params{Repo: Repo{Remote: "https://gitlab.com/acme/api.git", Repo: "acme/api", Site: Public},
		PR: registry.PR{Number: iid}}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Threads) != 1 || len(got.Threads[0].Comments) != 1 || got.Threads[0].Comments[0].Reactions != nil {
		t.Errorf("threads %+v", got.Threads)
	}
}
