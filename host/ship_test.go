package host

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gh/ghtest"
	"github.com/liamg/oriel/gitx/gittest"
)

// githubRepo is a fixture checkout whose origin is https://github.com/acme/<name>.git, answered by a bare repo on
// disk (git's url.insteadOf), with main pushed; upstream is another clone that moves main on.
func githubRepo(t *testing.T, name string, files map[string]string) (checkout, upstream *gittest.Repo) {
	t.Helper()
	root := t.TempDir()
	bare := filepath.Join(root, "origins", "acme", name+".git")
	checkout = &gittest.Repo{T: t, Dir: filepath.Join(root, name), When: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	os.MkdirAll(checkout.Dir, 0o755)
	checkout.Git("init", "-q", "--bare", "-b", "main", bare)
	checkout.Git("init", "-q", "-b", "main")
	checkout.Write(files)
	checkout.Commit("Ada", "first")
	checkout.Git("remote", "add", "origin", "https://github.com/acme/"+name+".git")
	checkout.Git("config", "url."+filepath.Join(root, "origins", "acme")+"/.insteadOf", "https://github.com/acme/")
	checkout.Git("push", "-q", "origin", "main")
	checkout.Git("fetch", "-q", "origin")
	checkout.Git("remote", "set-head", "origin", "main")
	upstream = &gittest.Repo{T: t, Dir: filepath.Join(root, "upstream-"+name), When: checkout.When}
	checkout.Git("clone", "-q", bare, upstream.Dir)
	return checkout, upstream
}

func (r *rig) ship(id core.SessionID, fresh bool) map[string]Ship {
	r.t.Helper()
	ss, err := r.w.ShipRead(context.Background(), ShipReq{Session: id, Fresh: fresh})
	if err != nil {
		r.t.Fatal(err)
	}
	out := map[string]Ship{}
	for _, s := range ss {
		out[s.Project] = s
	}
	return out
}

// Flows tested against fixture repos and a fake GitHub: a flight checklist per project (uncommitted → behind base →
// unpushed → PR → checks), and multiple projects side by side, with a merge order.
func TestShipFlowAcrossProjects(t *testing.T) {
	hub := ghtest.New(t)
	r := newRigWith(t, func(o *Options) { o.GitHub = func(string, []string) gh.Client { return hub.Client() } })
	api, apiUp := githubRepo(t, "api", map[string]string{
		"limiter.go":                       "package limiter\n\nfunc Allow() bool {\n\treturn true\n}\n",
		".github/CODEOWNERS":               "*  @acme/platform-team\n*.go  @jkowalski\n",
		".github/pull_request_template.md": "## What\n\n## Why\n",
	})
	web, _ := githubRepo(t, "web", map[string]string{"index.html": "<html></html>\n"})
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Back off on 429", Ticket: "ENG-2231", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(api.Dir), core.RepoID(web.Dir)}})
	se := r.session(id)
	dirs := map[string]string{}
	for _, p := range se.Projects {
		dirs[p.Name] = p.Dir
	}
	ctx := context.Background()

	s := r.ship(id, true)
	a := s["api"]
	if a.Origin != "https://github.com/acme/api.git" || !a.HasOrigin || a.Base != "main" || a.Branch != se.Branch || len(a.Files) != 0 || a.Commits != 0 {
		t.Fatalf("a fresh project: %+v", a)
	}
	if a.Template != "## What\n\n## Why\n" || a.TemplateFile != ".github/pull_request_template.md" {
		t.Errorf("template %q from %q", a.Template, a.TemplateFile)
	}

	// The agent drafts a commit message; it shows with who drafted it.
	ag := r.agent()
	ag.Call(t, "commit_draft", map[string]any{"message": "feat(api): back off on 429", "project": "api"})
	ag.Call(t, "pr_draft", map[string]any{"title": "feat(search): back off on 429", "body": "## What\nRetry-After.\n", "project": "api"})
	s = r.ship(id, false)
	if d := s["api"].CommitDraft; d.Body != "feat(api): back off on 429" || d.By != "fake" {
		t.Errorf("commit draft %+v", d)
	}
	if d := s["api"].PRDraft; d.Title != "feat(search): back off on 429" || d.Body != "## What\nRetry-After." || d.By != "fake" {
		t.Errorf("PR draft %+v", d)
	}

	// Uncommitted → committed: the draft it used goes.
	os.WriteFile(filepath.Join(dirs["api"], "limiter.go"), []byte("package limiter\n\nfunc Allow() bool {\n\treturn false\n}\n"), 0o644)
	os.WriteFile(filepath.Join(dirs["api"], "retry.go"), []byte("package limiter\n\nconst Retry = 1\n"), 0o644)
	s = r.ship(id, false)
	if fs := s["api"].Files; len(fs) != 2 || fs[0].Path != "limiter.go" || fs[0].Add != 1 || fs[0].Del != 1 || fs[1].Code != "?" {
		t.Fatalf("uncommitted %+v", fs)
	}
	if _, err := r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: "api", Message: "feat(api): back off on 429", Files: []string{"limiter.go"}}); err != nil {
		t.Fatal(err)
	}
	s = r.ship(id, false)
	a = s["api"]
	if len(a.Files) != 1 || a.Commits != 1 || len(a.Unpushed) != 1 || a.Unpushed[0].Subject != "feat(api): back off on 429" || !a.CommitDraft.Empty() {
		t.Fatalf("after committing one file: %+v", a)
	}
	if !slices.Equal(a.Owners, []string{"jkowalski"}) {
		t.Errorf("owners of the branch's change: %q", a.Owners)
	}
	r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: "api", Message: "test(api): retry"})

	// Behind its base: a fresh read fetches, and Sync rebases.
	apiUp.Write(map[string]string{"README": "docs\n"})
	apiUp.Commit("Bo", "docs")
	apiUp.Git("push", "-q", "origin", "main")
	if a = r.ship(id, true)["api"]; a.Behind != 1 || len(a.Conflicts) != 0 {
		t.Fatalf("behind main: %d, conflicts %q", a.Behind, a.Conflicts)
	}
	if err := r.w.ShipSync(ctx, ShipRef{Session: id, Project: "api"}); err != nil {
		t.Fatal(err)
	}
	if a = r.ship(id, false)["api"]; a.Behind != 0 || a.Commits != 2 || !a.Synced.Equal(r.now()) {
		t.Fatalf("after Sync: %+v", a)
	}

	// Push, then raise as a draft with the reviewers CODEOWNERS names.
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: "api"}); err != nil {
		t.Fatal(err)
	}
	if a = r.ship(id, false)["api"]; !a.Pushed || len(a.Unpushed) != 0 {
		t.Fatalf("after Push: %+v", a)
	}
	if out := apiUp.Git("ls-remote", "origin", se.Branch); !strings.Contains(out, se.Branch) {
		t.Fatalf("origin hasn't got %s: %q", se.Branch, out)
	}
	// A PR is raised from the session's branch, which the host names for every code host.
	if b, err := r.w.ShipBranch(ctx, ShipRef{Session: id, Project: "api"}); err != nil || b != se.Branch {
		t.Fatalf("api raises from %q, %v; want %s", b, err, se.Branch)
	}

	// Only the session's branch is pushed.
	(&gittest.Repo{T: t, Dir: dirs["web"]}).Git("checkout", "-q", "-b", "elsewhere")
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: "web"}); err == nil || !strings.Contains(err.Error(), "only pushes") {
		t.Errorf("pushing another branch: %v", err)
	}
	if _, err := r.w.ShipBranch(ctx, ShipRef{Session: id, Project: "web"}); err == nil || !strings.Contains(err.Error(), "raises PRs") {
		t.Errorf("raising from another branch: %v", err)
	}

	// A draft the user writes is theirs.
	if err := r.w.ShipDraft(ctx, DraftReq{Session: id, Project: "web", Kind: "commit", Body: "fix(web): mine"}); err != nil {
		t.Fatal(err)
	}
	if d := r.ship(id, false)["web"].CommitDraft; d.Body != "fix(web): mine" || d.By != "you" {
		t.Errorf("the user's draft: %+v", d)
	}
}

// A rebase that would conflict is predicted, and Sync undoes it and says where.
func TestShipSyncConflicts(t *testing.T) {
	hub := ghtest.New(t)
	r := newRigWith(t, func(o *Options) { o.GitHub = func(string, []string) gh.Client { return hub.Client() } })
	api, up := githubRepo(t, "api", map[string]string{"limiter.go": "package limiter\n\nvar n = 1\n"})
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "n", Repos: []core.RepoID{core.RepoID(api.Dir)}})
	p, _ := r.session(id).Primary()
	os.WriteFile(filepath.Join(p.Dir, "limiter.go"), []byte("package limiter\n\nvar n = 2\n"), 0o644)
	ctx := context.Background()
	r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: p.Name, Message: "two"})
	up.Write(map[string]string{"limiter.go": "package limiter\n\nvar n = 3\n"})
	up.Commit("Bo", "three")
	up.Git("push", "-q", "origin", "main")
	if a := r.ship(id, true)[p.Name]; a.Behind != 1 || !slices.Equal(a.Conflicts, []string{"limiter.go"}) {
		t.Fatalf("predicted %+v", a)
	}
	err := r.w.ShipSync(ctx, ShipRef{Session: id, Project: p.Name})
	if err == nil || !strings.Contains(err.Error(), "conflicts in limiter.go") {
		t.Fatalf("sync = %v", err)
	}
	if a := r.ship(id, false)[p.Name]; a.Behind != 1 || a.Commits != 1 || len(a.Files) != 0 {
		t.Fatalf("a conflicting sync wasn't undone: %+v", a)
	}
}

// A direct session is the user's own checkout: it ships from the branch it's on now, whichever they switched to.
func TestShipDirectFollowsTheCheckout(t *testing.T) {
	hub := ghtest.New(t)
	r := newRigWith(t, func(o *Options) { o.GitHub = func(string, []string) gh.Client { return hub.Client() } })
	blog, up := githubRepo(t, "blog", map[string]string{"post.md": "# One\n"})
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: blog.Dir})
	p, _ := r.session(id).Primary()
	if p.Branch != "main" {
		t.Fatalf("started on %q", p.Branch)
	}
	blog.Git("switch", "-q", "-c", "two")
	os.WriteFile(filepath.Join(blog.Dir, "post.md"), []byte("# Two\n"), 0o644)
	ctx := context.Background()
	if _, err := r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: p.Name, Message: "docs: two"}); err != nil {
		t.Fatal(err)
	}
	if s := r.ship(id, false)[p.Name]; s.Branch != "two" || s.Base != "main" || s.Commits != 1 {
		t.Fatalf("after switching: %+v", s)
	}
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: p.Name}); err != nil {
		t.Fatal(err)
	}
	if out := up.Git("ls-remote", "origin", "two"); !strings.Contains(out, "refs/heads/two") {
		t.Fatalf("origin hasn't got two: %q", out)
	}
	if b, err := r.w.ShipBranch(ctx, ShipRef{Session: id, Project: p.Name}); err != nil || b != "two" {
		t.Errorf("raises its PR from %q, %v; want two", b, err)
	}
	blog.Git("switch", "-q", "--detach")
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: p.Name}); err == nil {
		t.Error("pushed a detached HEAD")
	}
}

// GitHub's API is asked through the host's HTTP client (Options.HTTP), as its other requests are.
func TestGitHubIsAskedThroughTheHostsHTTP(t *testing.T) {
	var asked []string
	r := newRigWith(t, func(o *Options) {
		o.HTTP = &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
			asked = append(asked, req.URL.Host)
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`[]`)),
				Header: http.Header{"Content-Type": {"application/json"}}, Request: req}, nil
		})}
	})
	t.Setenv("GH_TOKEN", "test-token")
	blog, _ := githubRepo(t, "blog", map[string]string{"post.md": "# One\n"})
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: blog.Dir})
	if _, err := r.w.SyncGitHub(context.Background(), MirrorReq{Session: id, Fresh: true}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(asked, []string{"api.github.com"}) {
		t.Fatalf("GitHub wasn't asked through the host's client; it was asked of %q", asked)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// A directory opened as it is, just git init'ed with files in it and no commit yet, reads as its branch with those
// files to commit, not a git error.
func TestShipReadsARepoWithNoCommitsYet(t *testing.T) {
	r := newRig(t)
	g := gittest.New(t)
	g.Write(map[string]string{"main.go": "package main\n"})
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: g.Dir})
	for name, s := range r.ship(id, false) {
		if s.GitError != "" {
			t.Errorf("%s: git error %q", name, s.GitError)
		}
		if s.Branch != "main" || len(s.Files) != 1 || s.Files[0].Path != "main.go" {
			t.Errorf("%s: on %q with files %+v, want main.go to commit on main", name, s.Branch, s.Files)
		}
	}
}

// A repo whose remote isn't named origin reads as having none to push to, with the remotes it has, so the reason
// can say so rather than that it has no remote at all.
func TestShipSaysWhichRemotesARepoHasWithoutOrigin(t *testing.T) {
	r := newRig(t)
	g := gittest.New(t)
	g.Write(map[string]string{"main.go": "package main\n"})
	g.Commit("Ada", "first")
	g.Git("remote", "add", "upstream", "https://github.com/acme/search-api.git")
	id := r.start(core.SessionSpec{Kind: core.KindDirect, Dir: g.Dir})
	for name, s := range r.ship(id, false) {
		if s.HasOrigin || !slices.Equal(s.Remotes, []string{"upstream"}) {
			t.Errorf("%s: remote %v with remotes %v, want none to push to and [upstream]", name, s.HasOrigin, s.Remotes)
		}
	}
}
