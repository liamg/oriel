package host

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx/gittest"
)

// repoAt is a repo with one commit, at dir.
func repoAt(t *testing.T, dir string) *gittest.Repo {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	g := &gittest.Repo{T: t, Dir: dir}
	g.Git("init", "-q", "-b", "main")
	g.Write(map[string]string{"README": "hi\n"})
	g.Commit("Ada", "first")
	return g
}

func storeRepos(r *rig) []core.RepoID {
	var ids []core.RepoID
	for _, x := range r.w.store.State().Repos {
		ids = append(ids, x.ID)
	}
	return ids
}

// repos.md: repos.json becomes {repos, ignored}, and still reads today's bare array.
func TestReposFileReadsTheOldArray(t *testing.T) {
	r := newRig(t)
	old, _ := json.Marshal([]core.Repo{{ID: "pricing", Path: r.repo.Dir}})
	if err := os.WriteFile(filepath.Join(r.home, "repos.json"), old, 0o600); err != nil {
		t.Fatal(err)
	}
	r.restart()
	if got := storeRepos(r); !slices.Equal(got, []core.RepoID{"pricing"}) {
		t.Fatalf("store repos after an old repos.json = %v", got)
	}
	if err := r.w.RemoveRepo("pricing"); err != nil {
		t.Fatal(err)
	}
	var f reposFile
	b, _ := os.ReadFile(filepath.Join(r.home, "repos.json"))
	if err := json.Unmarshal(b, &f); err != nil || len(f.Repos) != 0 || !slices.Equal(f.Ignored, []string{r.repo.Dir}) {
		t.Fatalf("repos.json = %s (%v)", b, err)
	}
}

// repos.md: ReposChanged is fired on every add, with what it added; a session's start marks its repos used.
func TestReposChangedOnAdd(t *testing.T) {
	r := newRig(t)
	var got []core.ReposChanged
	r.w.store.Subscribe(core.Filter{}, func(ev core.Event) {
		if ev, ok := ev.(core.ReposChanged); ok {
			got = append(got, ev)
		}
	})
	repo, err := r.w.AddRepoPath(context.Background(), r.repo.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !slices.Equal(got[0].Added, []core.RepoID{repo.ID}) || len(got[0].Repos) != 1 {
		t.Fatalf("events = %+v", got)
	}
	if repo.Source != core.RepoAdded || !repo.Added.Equal(r.now()) {
		t.Errorf("added %+v", repo)
	}
	if _, err := r.w.AddRepoPath(context.Background(), r.repo.Dir); err != nil || len(got) != 1 {
		t.Errorf("adding it again: %v, %d events", err, len(got))
	}
	r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "x", Repos: []core.RepoID{repo.ID}})
	if x, _ := r.w.store.State().Repo(repo.ID); !x.Used.Equal(r.now()) {
		t.Errorf("used = %v after a session", x.Used)
	}
}

// repos.md, Remove: unregisters and ignores; refused while a live session has a worktree of it.
func TestRemoveRepo(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	repo, _ := r.w.AddRepoPath(ctx, r.repo.Dir)
	id := r.start(core.SessionSpec{Kind: core.KindWork, Name: "eng-2231", Prompt: "x", Repos: []core.RepoID{repo.ID}})
	if err := r.w.RemoveRepo(repo.ID); err == nil || !strings.Contains(err.Error(), "eng-2231") {
		t.Fatalf("removing a repo a session works in: %v", err)
	}
	if err := r.w.Archive(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := r.w.RemoveRepo(repo.ID); err != nil {
		t.Fatal(err)
	}
	if len(r.w.Repos()) != 0 || len(storeRepos(r)) != 0 {
		t.Fatalf("still registered: %v", r.w.Repos())
	}
	if _, err := os.Stat(filepath.Join(r.repo.Dir, "round.go")); err != nil {
		t.Errorf("removing touched the files: %v", err)
	}
	if again, err := r.w.AddRepoPath(ctx, r.repo.Dir); err != nil || again.ID != repo.ID {
		t.Errorf("adding it back by hand: %+v, %v", again, err)
	}
}

// repos.md, Discovery: the siblings of what we know, one level, main clones only, never ignored ones.
func TestScanFindsSiblings(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "src")
	known := repoAt(t, filepath.Join(src, "known"))
	repoAt(t, filepath.Join(src, "sibling"))
	repoAt(t, filepath.Join(src, "removed"))
	repoAt(t, filepath.Join(src, ".hidden"))
	repoAt(t, filepath.Join(src, "group", "deeper"))
	os.MkdirAll(filepath.Join(src, "plain"), 0o755)
	known.Git("worktree", "add", "-q", "-b", "wt", filepath.Join(src, "a-worktree"))

	if _, err := r.w.AddRepoPath(ctx, filepath.Join(src, "removed")); err != nil {
		t.Fatal(err)
	}
	removed, _ := r.w.repos.find(filepath.Join(src, "removed"))
	if err := r.w.RemoveRepo(removed.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.w.AddRepoPath(ctx, known.Dir); err != nil {
		t.Fatal(err)
	}
	added, err := r.w.ScanRepos(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, a := range added {
		names = append(names, filepath.Base(a.Path))
		if a.Source != core.RepoDiscovered {
			t.Errorf("%s source = %q", a.ID, a.Source)
		}
	}
	if !slices.Equal(names, []string{"sibling"}) {
		t.Fatalf("discovered %v, want [sibling]", names)
	}
	repoAt(t, filepath.Join(src, "later"))
	if again, _ := r.w.ScanRepos(ctx, false); len(again) != 0 {
		t.Errorf("a folder scanned already was read again: %v", again)
	}
	if again, _ := r.w.ScanRepos(ctx, true); len(again) != 1 || filepath.Base(again[0].Path) != "later" {
		t.Errorf("scan all = %v", again)
	}
}

// add-repo.md: clones go where you choose; by default, the folder most known repos share.
func TestCloneParent(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	r.w.opts.ReposDir = filepath.Join(t.TempDir(), "fallback")
	if got := r.w.CloneParent(); got != r.w.opts.ReposDir {
		t.Errorf("parent with no repos = %q", got)
	}
	code := filepath.Join(t.TempDir(), "code")
	for _, n := range []string{"a", "b"} {
		if _, err := r.w.AddRepoPath(ctx, repoAt(t, filepath.Join(code, n)).Dir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.w.AddRepoPath(ctx, r.repo.Dir); err != nil {
		t.Fatal(err)
	}
	if got := r.w.CloneParent(); got != code {
		t.Errorf("parent = %q, want %q", got, code)
	}
	bare := filepath.Join(t.TempDir(), "My-Project.git")
	r.repo.Git("clone", "-q", "--bare", r.repo.Dir, bare)
	tg, err := r.w.CloneTarget(ctx, CloneReq{URL: bare})
	if err != nil || tg.Dir != filepath.Join(code, "my-project") || tg.Exists {
		t.Fatalf("target = %+v, %v", tg, err)
	}
	elsewhere := filepath.Join(t.TempDir(), "elsewhere")
	var lines []string
	repo, err := r.w.CloneRepo(ctx, CloneReq{URL: bare, Parent: elsewhere, Name: "mine", Progress: func(l string) { lines = append(lines, l) }})
	if err != nil {
		t.Fatal(err)
	}
	if repo.Path != filepath.Join(elsewhere, "mine") || repo.Source != core.RepoCloned {
		t.Errorf("cloned %+v", repo)
	}
	if len(lines) == 0 {
		t.Error("no progress lines")
	}
	if tg, _ := r.w.CloneTarget(ctx, CloneReq{URL: bare, Parent: elsewhere, Name: "mine"}); !tg.Exists || !tg.Same {
		t.Errorf("target over its own clone = %+v", tg)
	}
}

// add-repo.md: the live check under the path field.
func TestCheckRepoPath(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	if c := r.w.CheckRepoPath(ctx, t.TempDir()); c.Error == "" {
		t.Errorf("a plain folder: %+v", c)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	r.repo.Git("worktree", "add", "-q", "-b", "wt", wt)
	c := r.w.CheckRepoPath(ctx, wt)
	if c.Path != r.repo.Dir || !c.Worktree || c.Known != "" || c.Branch != "main" {
		t.Errorf("a worktree: %+v", c)
	}
	repo, _ := r.w.AddRepoPath(ctx, r.repo.Dir)
	if c := r.w.CheckRepoPath(ctx, r.repo.Dir); c.Known != repo.ID || c.Worktree {
		t.Errorf("a known repo: %+v", c)
	}
}
