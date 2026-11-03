package sessions_test

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry/uireg"
)

// Add a project picks a repo, says it's adding it while the host makes the worktree off the loop, and opens the
// project once it's there.
func TestAddProjectWorksOffTheLoop(t *testing.T) {
	r, _, _, lw := apptest.ReviewWindow(t)
	other := filepath.Join(t.TempDir(), "billing-ui")
	for _, args := range [][]string{{"init", "-q", "-b", "main", other}, {"-C", other, "-c", "user.name=x", "-c", "user.email=x@x", "commit", "-q", "--allow-empty", "-m", "first"}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if _, err := lw.W.AddRepoPath(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	r.Do(func() error {
		for _, p := range r.Host.Pickers() {
			if p.ID == "projects.add" {
				return p.Run(context.Background(), uireg.PickerItem{ID: "billing-ui"})
			}
		}
		return errors.New("no projects.add picker")
	})
	r.WaitText("Adding billing-ui to the session")
	r.UntilState("billing-ui in the session", func(st core.State) bool {
		se, _ := st.Session("w")
		return slices.ContainsFunc(se.Snapshot().Projects, func(p core.Project) bool {
			return p.Name == "billing-ui" || filepath.Base(p.Repo) == "billing-ui" || filepath.Base(p.Dir) == "billing-ui"
		})
	})
	r.WaitGone("Adding billing-ui")
}
