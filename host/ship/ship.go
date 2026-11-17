package ship

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// The Ship page's side on the host: each project's way from uncommitted work to a pushed branch, read from
// its worktree, and the git steps along it the user takes from the app. They run here, where the checkout and the
// user's credentials are, with the sandbox's git guard in a sandboxed workspace. The pull request is the app's, through
// its code host (registry.CodeHost).

type Ship = hostapi.Ship

type ShipFile = hostapi.ShipFile

type ShipCommit = hostapi.ShipCommit

func shipCommit(c gitx.Commit) ShipCommit {
	return ShipCommit{Hash: c.Hash, Subject: c.Subject, Body: c.Body, Author: c.Author}
}

type ShipReq = hostapi.ShipReq

type ShipRef = hostapi.ShipRef

type ShipCommitReq = hostapi.ShipCommitReq

type DraftReq = hostapi.DraftReq

// shipState is what the host keeps between reads: which branches' PRs a code host last found open, and when each
// project was last synced.
type shipState struct {
	mu     sync.Mutex
	open   map[string]bool      // by session/project/branch
	synced map[string]time.Time // by session/project
}

func shipKey(id core.SessionID, project string) string { return string(id) + "/" + project }

// ShipRead is every project of a session on its way to a PR, or those r names.
func (x *Ships) ShipRead(ctx context.Context, r ShipReq) (_ []Ship, err error) {
	defer x.env.Recover("ShipRead", &err)
	se, err := x.st.Session(r.Session)
	if err != nil {
		return nil, err
	}
	projects := se.Projects
	if len(r.Projects) > 0 {
		projects = slices.DeleteFunc(slices.Clone(projects), func(p core.Project) bool { return !slices.Contains(r.Projects, p.Name) })
	}
	out := make([]Ship, len(projects))
	var wg sync.WaitGroup
	for i, p := range projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer x.env.Recover("ShipRead", nil)
			out[i] = x.shipProject(ctx, se, p, r.Fresh)
		}()
	}
	wg.Wait()
	return out, nil
}

func (x *Ships) shipProject(ctx context.Context, se core.SessionState, p core.Project, fresh bool) Ship {
	s := Ship{Project: p.Name, Dir: p.Dir, Branch: p.Branch, Base: strings.TrimPrefix(p.Base, "origin/"), ReadOnly: p.ReadOnly || p.Detached}
	s.CommitDraft = x.env.ReadDraft(se.ID, p.Name, "commit")
	s.PRDraft = x.env.ReadDraft(se.ID, p.Name, "pr")
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		s.GitError = err.Error()
		return s
	}
	repo := gitx.Repo{Dir: p.Dir, Env: env}
	if !x.deps.Kinds.Spec(se).Can(core.OwnsBranch) {
		s.Branch = hostcore.DirectBranch(ctx, p, repo).Branch
	}
	s.Origin = x.remoteURL(ctx, repo, fresh)
	s.HasOrigin = s.Origin != ""
	if !s.HasOrigin {
		s.Remotes, _ = repo.Remotes(ctx)
	}
	if s.ReadOnly {
		return s
	}
	s.Default = localDefault(ctx, repo)
	if fresh && s.HasOrigin {
		_ = repo.Fetch(ctx) // offline, it's behind by what it last saw
	}
	f, err := repo.Flight(ctx, s.Base)
	if err != nil {
		s.GitError = err.Error()
		return s
	}
	if f.Branch != "" && f.Branch != "HEAD" {
		s.Branch = f.Branch
	}
	if x.deps.Kinds.Spec(se).Can(core.OwnsBranch) {
		s.Off = hostcore.OffBranchOf(ctx, x.st, se, p, repo)
	}
	if s.Base == "" {
		s.Base = strings.TrimPrefix(f.Base, "origin/")
	}
	for _, fs := range f.Uncommitted {
		s.Files = append(s.Files, ShipFile{Path: fs.Path, Code: string(fs.Code), Add: fs.Add, Del: fs.Del, Binary: fs.Binary})
	}
	s.Behind, s.Conflicts, s.Commits, s.Pushed, s.Replaced = f.Behind, f.Conflicts, len(f.Commits), f.Pushed, f.Replaced
	s.Head, _ = repo.Head(ctx)
	if s.Base != "" && repo.HasBranch(ctx, s.Base) {
		if mb, err := repo.MergeBaseOf(ctx, "HEAD", "refs/heads/"+s.Base); err == nil {
			if n, err := repo.CountCommits(ctx, mb, "refs/remotes/origin/"+s.Base); err == nil {
				s.BaseAhead = n
			}
		}
	}
	for _, c := range f.Unpushed {
		s.Unpushed = append(s.Unpushed, shipCommit(c))
	}
	if n := len(f.Commits); n > 0 {
		s.First = shipCommit(f.Commits[n-1])
	}
	if f.Base != "" {
		if files, err := repo.ChangedFiles(ctx, f.Base); err == nil {
			s.Changed = files
		}
	}
	for kind, d := range map[string]*hostcore.Draft{"commit": &s.CommitDraft, "pr": &s.PRDraft} {
		if d.Code != "" {
			d.Stale = d.Code != hostcore.DraftCode(ctx, repo, f.Base, kind)
		}
	}
	st := &x.ship
	st.mu.Lock()
	s.Synced = st.synced[shipKey(se.ID, p.Name)]
	st.mu.Unlock()
	return s
}

// ShipCommit commits a project's files with the user's git identity, drops the commit draft it used, and returns the
// commit's hash.
func (x *Ships) ShipCommit(ctx context.Context, r ShipCommitReq) (_ string, err error) {
	defer x.env.Recover("ShipCommit", &err)
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	if p.ReadOnly || p.Detached {
		return "", fmt.Errorf("%s is read-only here", p.Name)
	}
	var hash string
	err = x.deps.Keyed(ctx, r.Session, "git commit", p.Name, func(ctx context.Context) (err error) {
		hash, err = repo.Commit(ctx, r.Message, gitx.CommitOpts{Paths: r.Files, All: len(r.Files) == 0, Trailers: r.Trailers})
		return err
	})
	if err != nil {
		return "", err
	}
	x.env.DropDraft(r.Session, p.Name, "commit")
	return hash, nil
}

// ShipPush pushes a project's branch to origin: the session's own branch only.
func (x *Ships) ShipPush(ctx context.Context, r ShipRef) (err error) {
	defer x.env.Recover("ShipPush", &err)
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	branch, err := hostcore.OwnBranch(p)
	if err != nil {
		return err
	}
	return x.deps.Keyed(ctx, r.Session, "git push", p.Name, func(ctx context.Context) error { return repo.Push(ctx, branch) })
}

// ShipSync fetches origin and rebases a project's branch onto its base, carrying uncommitted work over. One that
// conflicts is undone and says where.
func (x *Ships) ShipSync(ctx context.Context, r ShipRef) (err error) {
	defer x.env.Recover("ShipSync", &err)
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	if _, err := hostcore.OwnBranch(p); err != nil {
		return err
	}
	if err := x.deps.Keyed(ctx, r.Session, "git fetch origin", p.Name, repo.Fetch); err != nil {
		return err
	}
	base := repo.FlightBase(ctx, p.Base)
	if base == "" {
		return fmt.Errorf("%s has no base branch to rebase onto", p.Name)
	}
	if err := x.deps.Keyed(ctx, r.Session, "git rebase "+base+" (signing commits)", p.Name, func(ctx context.Context) error { return repo.Rebase(ctx, base) }); err != nil {
		return err
	}
	st := &x.ship
	st.mu.Lock()
	st.synced[shipKey(r.Session, p.Name)] = x.env.Now()
	st.mu.Unlock()
	return nil
}

// ShipMergeDefault merges a project's branch into the repo's default branch, here and not pushed (gitx.MergeInto):
// the session's own branch only, and its committed work only.
func (x *Ships) ShipMergeDefault(ctx context.Context, r ShipRef) (m gitx.Merge, err error) {
	defer x.env.Recover("ShipMergeDefault", &err)
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return m, err
	}
	branch, err := hostcore.OwnBranch(p)
	if err != nil {
		return m, err
	}
	into := localDefault(ctx, repo)
	switch into {
	case "":
		return m, fmt.Errorf("%s has no local default branch to merge into", p.Name)
	case branch:
		return m, fmt.Errorf("%s is on its default branch %s already", p.Name, into)
	}
	err = x.deps.Keyed(ctx, r.Session, "git merge "+branch+" into "+into, p.Name, func(ctx context.Context) (err error) {
		m, err = repo.MergeInto(ctx, into, branch, func(dir string) (gitx.Repo, error) {
			env, err := sandbox.GitEnv(dir)
			return gitx.Repo{Dir: dir, Env: env}, err
		})
		return err
	})
	return m, err
}

// localDefault is the repo's default branch when it has it as a local branch, else "".
func localDefault(ctx context.Context, repo gitx.Repo) string {
	if d := strings.TrimPrefix(repo.DefaultBranch(ctx), "origin/"); d != "HEAD" && repo.HasBranch(ctx, d) {
		return d
	}
	return ""
}

type RenameReq = hostapi.RenameReq

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (x *Ships) ShipRename(ctx context.Context, r RenameReq) (err error) {
	se, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(r.Name)
	old, err := hostcore.OwnBranch(p)
	if err != nil {
		return err
	}
	if name == old {
		return nil
	}
	if err := x.deps.Keyed(ctx, r.Session, "git branch -m "+old+" "+name, p.Name, func(ctx context.Context) error {
		return repo.RenameBranch(ctx, old, name)
	}); err != nil {
		return err
	}
	if !x.deps.Kinds.Spec(se).Can(core.OwnsBranch) {
		return nil
	}
	if err := x.deps.BranchRenamed(se.Dir, p.Name, name); err != nil {
		return err
	}
	return hostcore.SetProjectBranch(x.st, se.ID, p.Name, name, "")
}

// ShipDraft saves a draft the user wrote, or drops it.
func (x *Ships) ShipDraft(ctx context.Context, r DraftReq) (err error) {
	defer x.env.Recover("ShipDraft", &err)
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return err
	}
	if r.Kind != "commit" && r.Kind != "pr" {
		return fmt.Errorf("a draft is a commit or a pr, not %q", r.Kind)
	}
	if r.Drop {
		x.env.DropDraft(r.Session, p.Name, r.Kind)
		return nil
	}
	code := hostcore.DraftCode(ctx, repo, repo.FlightBase(ctx, p.Base), r.Kind)
	return x.env.WriteDraft(r.Session, p.Name, r.Kind, hostcore.Draft{Title: r.Title, Body: r.Body, By: hostcore.DraftByYou, Code: code})
}
