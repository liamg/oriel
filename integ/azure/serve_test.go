package azure_test

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/integ/azure"
	"github.com/liamg/oriel/integ/azure/azuretest"
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

// serving runs Serve as the plugin's process would, and is the Lua's end of it.
func serving(t *testing.T, h host) *jsonrpc.Conn {
	t.Helper()
	m, err := plugin.LoadManifest(filepath.Join("plugin"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	// The server site, as Oriel grants it once the user sets it up.
	g.Widen(m.ID, "the server site, set up", plugin.Grant{Cap: plugin.CapNet, Scope: []string{azuretest.ServerHost}},
		plugin.Grant{Cap: plugin.CapSecrets, Scope: []string{azuretest.OnPrem.Secret + "@" + azuretest.ServerHost}})
	p := plugin.Caller{ID: m.ID, Guard: g}
	methods := plugin.HostMethods(h)
	toProc, procIn := io.Pipe()
	procOut, fromProc := io.Pipe()
	go azure.Serve(toProc, fromProc)
	conn := jsonrpc.NewConn(procOut, procIn, jsonrpc.Handlers{Request: func(r *jsonrpc.Request) {
		go func() { r.Reply(methods.Call(context.Background(), p, r.Method, r.Params)) }()
	}}, jsonrpc.Options{Version: "2.0"})
	t.Cleanup(func() { conn.Close() })
	return conn
}

func fixture(t *testing.T) (*azuretest.Server, *jsonrpc.Conn, *plugin.SecretFile) {
	t.Helper()
	az := azuretest.New(t)
	secrets := &plugin.SecretFile{Path: filepath.Join(t.TempDir(), "secrets.json")}
	return az, serving(t, host{client: az.Client(), secrets: secrets}), secrets
}

// The review side: Requested, Lookup, Checkout. The requests asking you come from every organisation the plugin has
// learned of; your vote is the state, a team's ask is via the team. The token goes only to Azure DevOps, as HTTP
// Basic's password.
func TestReviewRequests(t *testing.T) {
	az, conn, secrets := fixture(t)
	ctx := t.Context()
	var rpc *jsonrpc.Error
	if err := conn.Call(ctx, "requested", azure.Params{Site: azuretest.Cloud, Orgs: []string{"acme"}}, nil); !errors.As(err, &rpc) || rpc.Code != azure.CodeNoKey {
		t.Fatalf("with no token: %v", err)
	}
	secrets.SetSecret(azure.Secret, azuretest.Token)

	priya := azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000002", DisplayName: "Priya K", UniqueName: "priya@acme.com"}
	asked := az.AddPR("acme", "web", "dashboard", azure.PullRequest{Title: "Usage chart", CreatedBy: priya, Source: "refs/heads/usage",
		Target: "refs/heads/main", Reviewers: []azure.Reviewer{{Identity: azuretest.Me}}})
	voted := az.AddPR("acme", "web", "api", azure.PullRequest{Title: "Retry uploads", CreatedBy: priya, Source: "refs/heads/retry",
		Target: "refs/heads/main", Reviewers: []azure.Reviewer{{Identity: azuretest.Me, Vote: -5}}})
	team := az.AddPR("acme", "web", "api", azure.PullRequest{Title: "Bump deps", CreatedBy: priya, Source: "refs/heads/deps", Target: "refs/heads/main",
		IsDraft: true, Reviewers: []azure.Reviewer{{Identity: azure.Identity{ID: "team-1", DisplayName: "[web]\\Platform"}, IsContainer: true}}})

	var got azure.Requested
	if err := conn.Call(ctx, "requested", azure.Params{Site: azuretest.Cloud, Orgs: []string{"acme"}}, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Orgs, []string{"acme"}) || len(got.Requests) != 3 {
		t.Fatalf("requested %+v", got)
	}
	byNumber := map[int]registry.ReviewRequest{}
	for _, r := range got.Requests {
		byNumber[r.Number] = r
	}
	if r := byNumber[asked.ID]; r.State != registry.ReviewNew || r.Via != "" || r.Repo != "web/dashboard" || r.Author != "Priya K" ||
		r.URL != "https://dev.azure.com/acme/web/_git/dashboard/pullrequest/57" || r.Asked.IsZero() {
		t.Errorf("asked of you %+v", r)
	}
	if r := byNumber[voted.ID]; r.State != registry.ReviewChanges {
		t.Errorf("waiting for the author %+v", r)
	}
	if r := byNumber[team.ID]; r.Via != "@[web]\\Platform" || !r.Draft {
		t.Errorf("asked of a team %+v", r)
	}

	az.SetChanges(asked.ID, "src/chart.ts", "src/chart.test.ts")
	az.AddStatus(asked.ID, "ci", "build", "pending", "")
	az.AddStatus(asked.ID, "ci", "build", "failed", "2 tests failed")
	var d registry.ReviewDetail
	if err := conn.Call(ctx, "lookup", azure.Params{Site: azuretest.Cloud, URL: asked.Repository.RemoteURL + "/pullrequest/57"}, &d); err != nil {
		t.Fatal(err)
	}
	if d.Head != "usage" || d.Base != "main" || len(d.Files) != 2 || d.Files[0].Path != "src/chart.ts" || d.Mergeable != "yes" ||
		len(d.Checks) != 1 || d.Checks[0].State != registry.CheckFailed || d.Checks[0].Detail != "2 tests failed" ||
		len(d.Reviewers) != 1 || d.Reviewers[0].Login != "you@acme.com" || d.Reviewers[0].State != "requested" {
		t.Errorf("lookup %+v", d)
	}
	var co registry.PRCheckout
	if err := conn.Call(ctx, "checkout", azure.Params{Site: azuretest.Cloud, URL: d.Request.URL}, &co); err != nil {
		t.Fatal(err)
	}
	if co.Remote != "dev.azure.com/acme/web/_git/dashboard" || co.Ref != "refs/heads/usage" {
		t.Errorf("checkout %+v", co)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(":"+azuretest.Token))
	for _, a := range az.Auths() {
		if a != "dev.azure.com "+want {
			t.Errorf("a request went as %q", a)
		}
	}
}

// The ship and merge side: a branch's PR raised, edited, made ready, its reviewers asked by name, its checks; the
// methods a policy allows; blockers ("active comment threads… draft (hard)… conflicts (hard)"); and the merge at the
// head the user saw, refused once the source has moved on.
func TestShipAndMerge(t *testing.T) {
	az, conn, secrets := fixture(t)
	secrets.SetSecret(azure.Secret, azuretest.Token)
	ctx := t.Context()
	repo := azure.CodeRepo{Remote: "https://acme@dev.azure.com/acme/web/_git/dashboard", Repo: "dashboard", Site: azuretest.Cloud}

	var none *registry.PR
	if err := conn.Call(ctx, "for_branch", azure.Params{Repo: repo, Branch: "usage"}, &none); err != nil || none != nil {
		t.Fatalf("no PR yet: %+v, %v", none, err)
	}
	var pr registry.PR
	if err := conn.Call(ctx, "create", azure.Params{Repo: repo, New: registry.NewPR{Title: "Usage chart", Body: "Adds it.", Branch: "usage",
		Base: "main", Draft: true}}, &pr); err != nil {
		t.Fatal(err)
	}
	if pr.Number != 57 || !pr.Draft || pr.State != "open" || pr.Branch != "usage" || pr.Base != "main" || pr.Head == "" ||
		pr.URL != "https://dev.azure.com/acme/web/_git/dashboard/pullrequest/57" {
		t.Fatalf("created %+v", pr)
	}
	var found registry.PR
	if err := conn.Call(ctx, "for_branch", azure.Params{Repo: repo, Branch: "usage"}, &found); err != nil || found.Number != pr.Number {
		t.Fatalf("the branch's PR: %+v, %v", found, err)
	}
	if err := conn.Call(ctx, "edit", azure.Params{Repo: repo, PR: &pr, Title: "Usage chart, weekly", Body: "Adds it, weekly."}, &found); err != nil ||
		found.Title != "Usage chart, weekly" {
		t.Fatalf("edited %+v, %v", found, err)
	}

	az.AddThread(pr.Number, "active", false)
	az.AddThread(pr.Number, "active", true)
	az.AddThread(pr.Number, "fixed", false)
	az.Update(pr.Number, func(p *azure.PullRequest) { p.MergeStatus = "conflicts" })
	var bs []registry.MergeBlocker
	if err := conn.Call(ctx, "blockers", azure.Params{Repo: repo, PR: &pr}, &bs); err != nil {
		t.Fatal(err)
	}
	kinds := map[string]registry.MergeBlocker{}
	for _, b := range bs {
		kinds[b.Kind] = b
	}
	if b := kinds[registry.BlockThreads]; b.Count != 1 || b.Text != "1 reviewer comment unresolved" || b.Hard {
		t.Errorf("threads %+v", b)
	}
	if !kinds[registry.BlockDraft].Hard || !kinds[registry.BlockConflicts].Hard || len(bs) != 3 {
		t.Errorf("blockers %+v", bs)
	}

	if err := conn.Call(ctx, "ready", azure.Params{Repo: repo, PR: &pr}, nil); err != nil {
		t.Fatal(err)
	}
	priya := azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000002", DisplayName: "Priya K", UniqueName: "priya@acme.com"}
	az.AddIdentity(priya)
	if err := conn.Call(ctx, "request_reviewers", azure.Params{Repo: repo, PR: &pr, Who: []string{"priya@acme.com"}}, nil); err != nil {
		t.Fatal(err)
	}
	az.Vote(pr.Number, priya, -10)
	az.Update(pr.Number, func(p *azure.PullRequest) { p.MergeStatus = "succeeded" })
	got := az.PRs("acme", "web", "dashboard")[0]
	if got.IsDraft || len(got.Reviewers) != 1 || got.Reviewers[0].ID != priya.ID {
		t.Fatalf("after ready and reviewers: %+v", got)
	}
	if err := conn.Call(ctx, "blockers", azure.Params{Repo: repo, PR: &pr}, &bs); err != nil || len(bs) != 2 || bs[1].Kind != registry.BlockChanges ||
		bs[1].Text != "Priya K asked for changes" {
		t.Errorf("blockers once ready: %+v, %v", bs, err)
	}

	az.AddStatus(pr.Number, "ci", "build", "succeeded", "")
	az.AddStatus(pr.Number, "", "lint", "pending", "")
	var cs []registry.Check
	if err := conn.Call(ctx, "checks", azure.Params{Repo: repo, PR: &pr}, &cs); err != nil || len(cs) != 2 || cs[0].Name != "ci/build" ||
		cs[0].State != registry.CheckOK || cs[1].State != registry.CheckRunning {
		t.Errorf("checks %+v, %v", cs, err)
	}

	var mm registry.MergeMethods
	if err := conn.Call(ctx, "merge_methods", azure.Params{Repo: repo, PR: &pr}, &mm); err != nil || len(mm.Allowed) != 3 || mm.Default != "" {
		t.Errorf("no policy: %+v, %v", mm, err)
	}
	az.AllowMerges("acme", "web", "squash")
	if err := conn.Call(ctx, "merge_methods", azure.Params{Repo: repo, PR: &pr}, &mm); err != nil || !slices.Equal(mm.Allowed, []string{"squash"}) ||
		mm.Default != "squash" {
		t.Errorf("a squash-only policy: %+v, %v", mm, err)
	}

	moved := pr
	az.Update(pr.Number, func(p *azure.PullRequest) { p.LastSource.CommitID = "beef" })
	if err := conn.Call(ctx, "merge", azure.Params{Repo: repo, PR: &moved, Merge: registry.MergeOpts{Method: "squash", Head: pr.Head}}, nil); err == nil {
		t.Fatal("completed at a source that had moved on")
	}
	if err := conn.Call(ctx, "merge", azure.Params{Repo: repo, PR: &moved, Merge: registry.MergeOpts{Method: "squash", Head: "beef",
		DeleteBranch: true}}, nil); err != nil {
		t.Fatal(err)
	}
	if how, deleted := az.Completed(pr.Number); how != "squash" || !deleted {
		t.Errorf("completed %q, deleted %v", how, deleted)
	}
}

// The repo a remote names on its site, in each form Azure DevOps hands out: on dev.azure.com with its organization,
// on a server within the collection its site's API names.
func TestParseRemote(t *testing.T) {
	ssh := azure.Site{Host: "ssh.dev.azure.com", API: "https://dev.azure.com", Secret: azure.Secret}
	for _, c := range []struct {
		site   azure.Site
		remote string
		want   azure.Repo
	}{
		{azuretest.Cloud, "https://dev.azure.com/acme/web/_git/dashboard", azure.Repo{Site: azuretest.Cloud, Org: "acme", Project: "web", Name: "dashboard"}},
		{azuretest.Cloud, "https://acme@dev.azure.com/acme/web/_git/dashboard", azure.Repo{Site: azuretest.Cloud, Org: "acme", Project: "web", Name: "dashboard"}},
		{ssh, "git@ssh.dev.azure.com:v3/acme/web/dashboard", azure.Repo{Site: ssh, Org: "acme", Project: "web", Name: "dashboard"}},
		{azuretest.OnPrem, "https://tfs.acme.corp/DefaultCollection/web/_git/dashboard", azure.Repo{Site: azuretest.OnPrem, Project: "web", Name: "dashboard"}},
		{azuretest.OnPrem, "ssh://tfs.acme.corp:22/DefaultCollection/web/_git/dashboard.git", azure.Repo{Site: azuretest.OnPrem, Project: "web", Name: "dashboard"}},
	} {
		if got, ok := azure.ParseRemote(c.site, c.remote); !ok || got != c.want {
			t.Errorf("%s: %+v, %v", c.remote, got, ok)
		}
	}
	if _, ok := azure.ParseRemote(azuretest.Cloud, "https://github.com/acme/web.git"); ok {
		t.Error("GitHub's remote as Azure DevOps'")
	}
	if got := (azure.Repo{Site: azuretest.OnPrem, Project: "web", Name: "dashboard"}).Web(); got != "https://tfs.acme.corp/DefaultCollection/web/_git/dashboard" {
		t.Errorf("a server repo's page: %s", got)
	}
}

// The coordinator's setup: "The probe answers done(true) when host is an Azure DevOps Server, from one
// unauthenticated request (GET https://{host}/_apis/connectionData: an X-TFS-ProcessId or X-VSS-* header)." A
// server site's requests are its collection's, and a project there ships and merges as on dev.azure.com.
func TestServerSite(t *testing.T) {
	az, conn, secrets := fixture(t)
	ctx := t.Context()
	var probed struct {
		Server bool `json:"server"`
	}
	if err := conn.Call(ctx, "probe", azure.Params{Host: azuretest.ServerHost}, &probed); err != nil || !probed.Server {
		t.Errorf("the server: %+v, %v", probed, err)
	}
	if err := conn.Call(ctx, "probe", azure.Params{Host: "dev.azure.com"}, &probed); err != nil || probed.Server {
		t.Errorf("not a server: %+v, %v", probed, err)
	}
	for _, a := range az.Auths() {
		if !strings.HasSuffix(a, " ") {
			t.Errorf("a probe sent a token: %q", a)
		}
	}

	secrets.SetSecret(azuretest.OnPrem.Secret, azuretest.Token)
	asked := az.AddPR("DefaultCollection", "web", "billing", azure.PullRequest{Title: "Invoice PDFs", CreatedBy: azuretest.Me,
		Source: "refs/heads/pdf", Target: "refs/heads/main", Reviewers: []azure.Reviewer{{Identity: azuretest.Me}}})
	var got azure.Requested
	if err := conn.Call(ctx, "requested", azure.Params{Site: azuretest.OnPrem}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Requests) != 1 || got.Requests[0].URL != "https://tfs.acme.corp/DefaultCollection/web/_git/billing/pullrequest/"+strconv.Itoa(asked.ID) {
		t.Fatalf("requested on the server %+v", got)
	}
	repo := azure.CodeRepo{Remote: "https://tfs.acme.corp/DefaultCollection/web/_git/billing", Site: azuretest.OnPrem}
	var pr registry.PR
	if err := conn.Call(ctx, "for_branch", azure.Params{Repo: repo, Branch: "pdf"}, &pr); err != nil || pr.Number != asked.ID {
		t.Fatalf("the branch's PR: %+v, %v", pr, err)
	}
	if err := conn.Call(ctx, "merge", azure.Params{Repo: repo, PR: &pr, Merge: registry.MergeOpts{Method: "merge", Head: pr.Head}}, nil); err != nil {
		t.Fatal(err)
	}
	if how, _ := az.Completed(asked.ID); how != "noFastForward" {
		t.Errorf("completed %q", how)
	}
	for _, a := range az.Auths() {
		if strings.HasPrefix(a, azuretest.ServerHost+" Basic") && a != azuretest.ServerHost+" Basic "+base64.StdEncoding.EncodeToString([]byte(":"+azuretest.Token)) {
			t.Errorf("a request went as %q", a)
		}
	}
}

// The coordinator's spec for review mirroring: "One thread per PR thread with a threadContext (id = the thread id,
// path from filePath, line from rightFileStart.line, or leftFileStart with old = true), resolved when its status is
// fixed/closed/wontFix/byDesign, plus one with no id holding the general threads' comments", the reviewer's vote as
// its state; reply "answering the new comment's id", in a thread or as a new one in the conversation.
func TestThreadsAndReply(t *testing.T) {
	az, conn, secrets := fixture(t)
	secrets.SetSecret(azure.Secret, azuretest.Token)
	ctx := t.Context()
	priya := azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000002", DisplayName: "Priya K", UniqueName: "priya@acme.com"}
	stranger := azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000009", DisplayName: "Drive-by", UniqueName: "x@else.com"}
	pr := az.AddPR("acme", "web", "dashboard", azure.PullRequest{Title: "Usage chart", CreatedBy: azuretest.Me, Source: "refs/heads/usage",
		Target: "refs/heads/main"})
	az.Vote(pr.ID, priya, -5)
	onLine := az.AddFileThread(pr.ID, "src/chart.ts", 12, priya, "Can this return an error instead?")
	done := az.AddFileThread(pr.ID, "src/old.ts", 3, priya, "nit")
	az.Resolve(pr.ID, done, "fixed")
	az.AddThread(pr.ID, "active", true)
	az.AddConversation(pr.ID, stranger, "Looks neat")
	repo := azure.CodeRepo{Remote: "https://dev.azure.com/acme/web/_git/dashboard", Site: azuretest.Cloud}
	var got registry.PR
	if err := conn.Call(ctx, "for_branch", azure.Params{Repo: repo, Branch: "usage"}, &got); err != nil {
		t.Fatal(err)
	}

	var ts []registry.ReviewThread
	if err := conn.Call(ctx, "threads", azure.Params{Repo: repo, PR: &got}, &ts); err != nil {
		t.Fatal(err)
	}
	if len(ts) != 3 {
		t.Fatalf("threads %+v", ts)
	}
	a := ts[0]
	if a.ID != strconv.Itoa(onLine) || a.Path != "src/chart.ts" || a.Line != 12 || a.Old || a.Resolved == nil || *a.Resolved ||
		a.Reviewer != "priya@acme.com" || a.State != registry.ReviewChanges || len(a.Comments) != 1 || !a.Comments[0].Member ||
		a.Comments[0].ID != strconv.Itoa(onLine)+"/1" || a.Comments[0].At.IsZero() || !strings.Contains(a.URL, "discussionId=") {
		t.Errorf("the thread on a line %+v", a)
	}
	if b := ts[1]; b.Resolved == nil || !*b.Resolved {
		t.Errorf("the fixed thread %+v", b)
	}
	if g := ts[2]; g.ID != "" || g.Path != "" || len(g.Comments) != 1 || g.Comments[0].Body != "Looks neat" || g.Comments[0].Member {
		t.Errorf("the conversation %+v", g)
	}

	var id string
	if err := conn.Call(ctx, "reply", azure.Params{Repo: repo, PR: &got, Thread: a.ID, Body: "Agreed: it returns an error now."}, &id); err != nil {
		t.Fatal(err)
	}
	if id != a.ID+"/2" {
		t.Errorf("the reply's key %q", id)
	}
	if cs := az.Threads(pr.ID)[onLine-1].Comments; len(cs) != 2 || cs[1].Content != "Agreed: it returns an error now." || cs[1].Author.ID != azuretest.Me.ID {
		t.Errorf("the thread on Azure DevOps: %+v", cs)
	}
	if err := conn.Call(ctx, "reply", azure.Params{Repo: repo, PR: &got, Body: "Thanks all."}, &id); err != nil || id != "5/1" {
		t.Errorf("a new thread: %q, %v", id, err)
	}
	if err := conn.Call(ctx, "reply", azure.Params{Repo: repo, PR: &got, Thread: a.ID}, &id); err == nil {
		t.Error("an empty reply went")
	}
}
