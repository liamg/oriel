package host

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
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

// FG-6 done when: "flows tested against fixture repos and a fake GitHub". ui.md 18: "A flight checklist per project:
// uncommitted → behind base → unpushed → PR → checks", "Multiple projects side by side, with cross-linked PRs and a
// merge order".
func TestShipFlowAcrossProjects(t *testing.T) {
	r := newRig(t)
	hub := ghtest.New(t)
	r.w.opts.GitHub = func(string, []string) gh.Client { return hub.Client() }
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
	if a.GitHub != "acme/api" || !a.Remote || a.Base != "main" || a.Branch != se.Branch || len(a.Files) != 0 || a.Commits != 0 || a.PR != nil {
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
	if a = r.ship(id, false)["api"]; a.Behind != 0 || a.Commits != 2 || !a.Synced.Equal(r.now) {
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
	pr, err := r.w.ShipPR(ctx, PRReq{Session: id, Project: "api", Title: "feat(search): back off on 429", Body: "## What\nRetry-After.\n",
		Draft: true, Reviewers: []string{"acme/platform-team", "jkowalski"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := hub.PRs("acme/api"); len(got) != 1 || !got[0].Draft || got[0].Head != se.Branch || got[0].Base != "main" ||
		!slices.Equal(got[0].Reviewers, []string{"jkowalski", "acme/platform-team"}) || got[0].Number != pr.Number {
		t.Fatalf("the PR on GitHub: %+v", got)
	}

	// web's PR cross-links both, in the session's order.
	os.WriteFile(filepath.Join(dirs["web"], "index.html"), []byte("<html>429</html>\n"), 0o644)
	r.w.ShipCommit(ctx, ShipCommitReq{Session: id, Project: "web", Message: "feat(web): show the wait"})
	r.w.ShipPush(ctx, ShipRef{Session: id, Project: "web"})
	webPR, err := r.w.ShipPR(ctx, PRReq{Session: id, Project: "web", Title: "feat(web): show the wait", Body: "Shows it."})
	if err != nil {
		t.Fatal(err)
	}
	apiBody, webBody := hub.PRs("acme/api")[0].Body, hub.PRs("acme/web")[0].Body
	wantLinks := func(body, self string) {
		t.Helper()
		block := linkStart + "\n**Ships with** (merge in this order):\n1. acme/api#" + strconv.Itoa(pr.Number) + map[bool]string{true: " (this PR)"}[self == "api"] +
			"\n2. acme/web#" + strconv.Itoa(webPR.Number) + map[bool]string{true: " (this PR)"}[self == "web"] + "\n" + linkEnd
		if !strings.HasSuffix(body, block) {
			t.Errorf("%s's body doesn't end with the links:\n%s", self, body)
		}
	}
	wantLinks(apiBody, "api")
	wantLinks(webBody, "web")
	if !strings.HasPrefix(apiBody, "## What\nRetry-After.\n\n"+linkStart) {
		t.Errorf("api's body lost its text:\n%s", apiBody)
	}

	// Editing the PR keeps one block.
	if _, err := r.w.ShipPR(ctx, PRReq{Session: id, Project: "api", Title: "feat(search): back off on 429s", Body: apiBody}); err != nil {
		t.Fatal(err)
	}
	if got := hub.PRs("acme/api"); len(got) != 1 || got[0].Title != "feat(search): back off on 429s" || strings.Count(got[0].Body, linkStart) != 1 {
		t.Fatalf("after editing: %+v", got)
	}

	// Checks: a failing Actions job's log line comes with them.
	r.ship(id, false)
	t0 := r.now.Add(-3 * time.Minute)
	hub.SetChecks("acme/api", se.Branch,
		gh.Check{ID: 11, Name: "lint", Status: "completed", Conclusion: "success", Actions: true, Started: t0, Completed: t0.Add(38 * time.Second)},
		gh.Check{ID: 12, Name: "unit", Status: "completed", Conclusion: "failure", Actions: true, Started: t0, Completed: t0.Add(time.Minute)},
		gh.Check{ID: 13, Name: "e2e · chromium", Status: "in_progress", Actions: true, Started: t0})
	hub.SetLog(12, "2026-10-14T09:00:00.0000000Z go test ./limiter/...\n2026-10-14T09:00:01.0000000Z --- FAIL: TestBurst (0.02s)\n")
	if a = r.ship(id, false)["api"]; len(a.Checks) != 0 {
		t.Errorf("a read within the minute answered from GitHub again: %+v", a.Checks)
	}
	a = r.ship(id, true)["api"]
	if len(a.Checks) != 3 || a.FailLine != "--- FAIL: TestBurst (0.02s)" || a.PR == nil || a.PR.Number != pr.Number {
		t.Fatalf("checks %+v, fail line %q, PR %+v", a.Checks, a.FailLine, a.PR)
	}

	// Ready for review.
	if err := r.w.ShipReady(ctx, ShipRef{Session: id, Project: "api"}); err != nil {
		t.Fatal(err)
	}
	if hub.PRs("acme/api")[0].Draft {
		t.Error("Ready for review left it a draft")
	}

	// Only the session's branch is pushed.
	(&gittest.Repo{T: t, Dir: dirs["web"]}).Git("checkout", "-q", "-b", "elsewhere")
	if err := r.w.ShipPush(ctx, ShipRef{Session: id, Project: "web"}); err == nil || !strings.Contains(err.Error(), "only pushes") {
		t.Errorf("pushing another branch: %v", err)
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
	r := newRig(t)
	r.w.opts.GitHub = func(string, []string) gh.Client { return ghtest.New(t).Client() }
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
