package app

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
)

// scrollAt scrolls what's under the last run of text drawn, by dy.
func (r *running) scrollAt(text string, dy float32) {
	r.t.Helper()
	var at render.Point
	r.latest(`"`+text+`" to scroll`, func(f headless.Frame) bool {
		found := false
		for _, op := range f.Ops {
			if op.Kind == render.OpGlyphs && op.Run.Text == text {
				at, found = render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}, true
			}
		}
		return found
	})
	r.win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	r.win.Send(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: dy}})
}

// newRepo is a git repo with one commit at dir.
func newRepo(t *testing.T, dir string) string {
	t.Helper()
	for _, args := range [][]string{{"init", "-q", "-b", "main", dir}, {"-C", dir, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// add-repo.md: the launcher's + Add repo opens the dialog over it; the repo it adds comes back ticked.
func TestAddRepoFromTheLauncher(t *testing.T) {
	lw := newLocalHost(t)
	if _, err := lw.w.AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	other := newRepo(t, filepath.Join(t.TempDir(), "billing"))
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.waitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })

	r.typeKeys("<D-n>")
	r.waitText("NEW SESSION")
	r.clickTop("Work")
	r.waitText("REPOSITORIES")
	r.clickTop("+ Add repo")
	r.waitText("Existing directory")
	r.clickTop("~/src/my-project")
	r.typeText(other)
	r.waitText("✓ git repo · main")
	r.clickTop("Add")
	r.latest("the new repo ticked", func(f headless.Frame) bool {
		s := f.Text()
		return !strings.Contains(s, "Existing directory") && strings.Contains(s, "JUST ADDED") && strings.Contains(s, "1 selected")
	})
	if _, ok := r.store.State().Repo("billing"); !ok {
		t.Fatalf("billing isn't registered: %v", r.store.State().Repos)
	}
}

// add-repo.md, Clone from URL: the parent folder and name are shown, and where they make it, before it clones.
func TestCloneFromTheDialog(t *testing.T) {
	lw := newLocalHost(t)
	ctx := context.Background()
	code := filepath.Join(t.TempDir(), "code")
	for _, n := range []string{"a", "b"} {
		if _, err := lw.w.AddRepoPath(ctx, newRepo(t, filepath.Join(code, n))); err != nil {
			t.Fatal(err)
		}
	}
	bare := filepath.Join(t.TempDir(), "my-project.git")
	if out, err := exec.Command("git", "clone", "-q", "--bare", lw.repo, bare).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the repos", func(st core.State) bool { return len(st.Repos) >= 2 })
	if err := r.do(func() error { return r.host.Run(ctx, "repos.new", registry.Args{"mode": "clone"}) }); err != nil {
		t.Fatal(err)
	}
	r.waitText("Clone from URL")
	r.clickTop("https://github.com/owner/repo")
	r.typeText(bare)
	into := tildePath(filepath.Join(code, "my-project"))
	r.waitText(into)
	r.waitText("new folder")
	r.clickTop("Clone")
	waitState(t, r.store, "the clone registered", func(st core.State) bool {
		rr, ok := st.Repo("my-project")
		return ok && rr.Path == filepath.Join(code, "my-project") && rr.Source == core.RepoCloned
	})
	r.waitGone("Clone from URL")
}

// repos-page.md: housekeeping and New tag, driven from the page.
func TestReposPageCleansAndTags(t *testing.T) {
	lw := newLocalHost(t)
	t.Setenv("GIT_COMMITTER_NAME", "x")
	t.Setenv("GIT_COMMITTER_EMAIL", "x@x")
	ctx := context.Background()
	git(t, lw.repo, "branch", "done")
	repo, err := lw.w.AddRepoPath(ctx, lw.repo)
	if err != nil {
		t.Fatal(err)
	}
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the repo", func(st core.State) bool { return len(st.Repos) == 1 })
	if err := r.do(func() error { return r.host.Run(ctx, "repos.open", registry.Args{"repo": string(repo.ID)}) }); err != nil {
		t.Fatal(err)
	}
	r.waitText("REPOS")
	r.waitText("Clean up 1…")
	r.clickTop("Clean up 1…")
	r.clickTop("Sure? Delete 1")
	r.waitText("✓ deleted done")
	if out := git(t, lw.repo, "branch", "--format=%(refname:short)"); strings.Contains(out, "done") {
		t.Errorf("done is still there: %s", out)
	}

	r.clickTop("New tag…")
	r.waitText("Create tag v0.1.0")
	r.scrollAt("HOUSEKEEPING", 2000)
	r.clickTop("Create tag v0.1.0")
	r.waitText("✓ tagged v0.1.0")
	if out := git(t, lw.repo, "cat-file", "-t", "v0.1.0"); strings.TrimSpace(out) != "tag" {
		t.Errorf("v0.1.0 is %q, not an annotated tag", out)
	}
}

// repos.md: Remove unregisters a repo from the page, and its files stay.
func TestReposPageRemoves(t *testing.T) {
	lw := newLocalHost(t)
	ctx := context.Background()
	repo, _ := lw.w.AddRepoPath(ctx, lw.repo)
	r := startAppWith(t, t.TempDir(), "", nil, lw.connect(t))
	waitState(t, r.store, "the repo", func(st core.State) bool { return len(st.Repos) == 1 })
	if err := r.do(func() error { return r.host.Run(ctx, "repos.open", registry.Args{"repo": string(repo.ID)}) }); err != nil {
		t.Fatal(err)
	}
	r.waitText("New session")
	r.clickTop("Remove")
	r.clickTop("Sure? Remove")
	waitState(t, r.store, "no repos", func(st core.State) bool { return len(st.Repos) == 0 })
	r.waitText("No repos yet.")
	git(t, lw.repo, "status")
}

// reposScene is the Repos page on a repo with a merged branch and a tag, beside a sibling discovery found.
func reposScene(t *testing.T) *errScene {
	t.Helper()
	lw := newLocalHost(t)
	t.Setenv("GIT_COMMITTER_NAME", "x")
	t.Setenv("GIT_COMMITTER_EMAIL", "x@x")
	git(t, lw.repo, "branch", "liam/eng-2190-usage-cache")
	git(t, lw.repo, "tag", "-a", "-m", "v0.4.2", "v0.4.2")
	newRepo(t, filepath.Join(filepath.Dir(lw.repo), "billing"))
	s := newSceneWith(t, "", "", mockupSize, lw.connect(t))
	waitState(t, s.store, "the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	if _, err := lw.w.AddRepoPath(context.Background(), lw.repo); err != nil {
		t.Fatal(err)
	}
	s.until(t, "the sibling found", func() bool { return len(s.store.State().Repos) == 2 })
	s.run("repos.open", registry.Args{"repo": "pricing"})
	s.until(t, "the page read", func() bool {
		f := s.frameText()
		return strings.Contains(f, "Clean up 1…") && strings.Contains(f, "latest v0.4.2") && strings.Contains(f, "last commit")
	})
	return s
}

// ui.md 45: the Repos page: the list with a found repo, the detail's stats, housekeeping and tags.
func TestReposPageLayout(t *testing.T) {
	s := reposScene(t)
	s.lint("the repos page")
	s.wantFrame(t, "REPOS", "2 repos · 1 found", "+ Add repo", "Scan for repos", "pricing", "billing", "FOUND", "Reveal",
		"Remove", "New session", "STATS", "HOUSEKEEPING", "liam/eng-2190-usage-cache", "MERGED", "TAGS", "latest v0.4.2", "New tag…")
	s.clickText(t, "New tag…")
	s.until(t, "the tag form", func() bool { return strings.Contains(s.frameText(), "Create tag v0.4.3") })
	s.lint("the new tag form")
	s.run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/example/my-project"})
	s.until(t, "the clone target", func() bool {
		s.now = s.now.Add(100 * time.Millisecond) // past the fields' rest
		return strings.Contains(s.frameText(), "new folder")
	})
	s.lint("the add repo dialog")
	s.wantFrame(t, "Add repo", "Existing directory", "Clone from URL", "Parent folder", "Folder name", "my-project", "Clones into")
}
