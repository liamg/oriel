package sessions_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/headless"
)

// The launcher's + Add repo opens the dialog over it; the repo it adds comes back ticked.
func TestAddRepoFromTheLauncher(t *testing.T) {
	lw := rig.NewHost(t)
	if _, err := lw.W.AddRepoPath(context.Background(), lw.Repo); err != nil {
		t.Fatal(err)
	}
	other := rig.NewRepo(t, filepath.Join(t.TempDir(), "billing"))
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	r.WaitFrame("New Session available", func(s string) bool { return strings.Contains(s, "New Session") && !strings.Contains(s, "SOON") })

	r.TypeKeys("<D-n>")
	r.WaitText("NEW SESSION")
	r.ClickTop("Work")
	r.WaitText("REPOSITORIES")
	r.ClickTop("+ Add repo")
	r.WaitText("Existing directory")
	r.ClickTop("~/src/my-project")
	r.TypeText(other)
	r.WaitText("✓ git repo · main")
	r.ClickTop("Add")
	r.Latest("the new repo ticked", func(f headless.Frame) bool {
		s := f.Text()
		return !strings.Contains(s, "Existing directory") && strings.Contains(s, "JUST ADDED") && strings.Contains(s, "1 selected")
	})
	if _, ok := r.Store.State().Repo("billing"); !ok {
		t.Fatalf("billing isn't registered: %v", r.Store.State().Repos)
	}
}

// Clone from URL: the parent folder and name are shown, and where they make it, before it clones.
func TestCloneFromTheDialog(t *testing.T) {
	lw := rig.NewHost(t)
	ctx := context.Background()
	code := filepath.Join(t.TempDir(), "code")
	for _, n := range []string{"a", "b"} {
		if _, err := lw.W.AddRepoPath(ctx, rig.NewRepo(t, filepath.Join(code, n))); err != nil {
			t.Fatal(err)
		}
	}
	bare := filepath.Join(t.TempDir(), "my-project.git")
	if out, err := exec.Command("git", "clone", "-q", "--bare", lw.Repo, bare).CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the repos", func(st core.State) bool { return len(st.Repos) >= 2 })
	if err := r.Do(func() error { return r.Host.Run(ctx, "repos.new", registry.Args{"mode": "clone"}) }); err != nil {
		t.Fatal(err)
	}
	r.WaitText("Clone from URL")
	r.ClickTop("https://github.com/owner/repo")
	r.TypeText(bare)
	into := kit.TildePath(filepath.Join(code, "my-project"))
	r.WaitText(into)
	r.WaitText("new folder")
	r.ClickTop("Clone")
	r.UntilState("the clone registered", func(st core.State) bool {
		rr, ok := st.Repo("my-project")
		return ok && rr.Path == filepath.Join(code, "my-project") && rr.Source == core.RepoCloned
	})
	r.WaitGone("Clone from URL")
}

// Housekeeping and New tag, driven from the page.
func TestReposPageCleansAndTags(t *testing.T) {
	lw := rig.NewHost(t)
	t.Setenv("GIT_COMMITTER_NAME", "x")
	t.Setenv("GIT_COMMITTER_EMAIL", "x@x")
	ctx := context.Background()
	rig.Git(t, lw.Repo, "branch", "done")
	repo, err := lw.W.AddRepoPath(ctx, lw.Repo)
	if err != nil {
		t.Fatal(err)
	}
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the repo", func(st core.State) bool { return len(st.Repos) == 1 })
	if err := r.Do(func() error { return r.Host.Run(ctx, "repos.open", registry.Args{"repo": string(repo.ID)}) }); err != nil {
		t.Fatal(err)
	}
	r.WaitText("REPOS")
	r.WaitText("Clean up 1…")
	r.ClickTop("Clean up 1…")
	r.ClickTop("Sure? Delete 1")
	r.WaitText("✓ deleted done")
	if out := rig.Git(t, lw.Repo, "branch", "--format=%(refname:short)"); strings.Contains(out, "done") {
		t.Errorf("done is still there: %s", out)
	}

	r.ClickTop("New tag…")
	r.WaitText("Create tag v0.1.0")
	r.WaitText("WILL TAG") // the form's preview, read off the loop: its coming in moves the button down
	r.ScrollAt("HOUSEKEEPING", 2000)
	r.Idle() // the scroll drawn, so the click is aimed at where the button is now
	r.ClickTop("Create tag v0.1.0")
	r.WaitText("✓ tagged v0.1.0")
	if out := rig.Git(t, lw.Repo, "cat-file", "-t", "v0.1.0"); strings.TrimSpace(out) != "tag" {
		t.Errorf("v0.1.0 is %q, not an annotated tag", out)
	}
}

// Remove unregisters a repo from the page, and its files stay.
func TestReposPageRemoves(t *testing.T) {
	lw := rig.NewHost(t)
	ctx := context.Background()
	repo, _ := lw.W.AddRepoPath(ctx, lw.Repo)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W)})
	r.UntilState("the repo", func(st core.State) bool { return len(st.Repos) == 1 })
	if err := r.Do(func() error { return r.Host.Run(ctx, "repos.open", registry.Args{"repo": string(repo.ID)}) }); err != nil {
		t.Fatal(err)
	}
	r.WaitText("New session")
	r.ClickTop("Remove")
	r.ClickTop("Sure? Remove")
	r.UntilState("no repos", func(st core.State) bool { return len(st.Repos) == 0 })
	r.WaitText("No repos yet.")
	rig.Git(t, lw.Repo, "status")
}

func reposScene(t *testing.T) *apptest.Scene { return apptest.ReposScene(t) }

// The Repos page: the list with a found repo, the detail's stats, housekeeping and tags.
func TestReposPageLayout(t *testing.T) {
	s := reposScene(t)
	s.Lint("the repos page")
	s.WantFrame("REPOS", "2 repos", "+ Add repo", "Scan for repos", "pricing", "billing", "FOUND", "Reveal",
		"Remove", "New session", "STATS", "HOUSEKEEPING", "liam/eng-2190-usage-cache", "MERGED", "TAGS", "latest v0.4.2", "New tag…")
	s.ClickTop("New tag…")
	s.Until("the tag form", func() bool { return strings.Contains(s.Text(), "Create tag v0.4.3") })
	s.Lint("the new tag form")
	s.Run("repos.new", registry.Args{"mode": "clone", "url": "https://github.com/example/my-project"})
	s.Until("the clone target", func() bool {
		s.Now = s.Now.Add(100 * time.Millisecond) // past the fields' rest
		return strings.Contains(s.Text(), "new folder")
	})
	s.Lint("the add repo dialog")
	s.WantFrame("Add repo", "Existing directory", "Clone from URL", "Parent folder", "Folder name", "my-project", "Clones into")
}
