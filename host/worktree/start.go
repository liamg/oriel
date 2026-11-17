package worktree

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/host/hostcore"
)

// sessionName is a new session's name until it's renamed: what spec names it, else its ticket and the prompt's first
// words.
func sessionName(spec core.SessionSpec) string {
	base := hostapi.Sanitise(spec.Name)
	if base == "" {
		base = hostcore.TrimWords(hostapi.Sanitise(core.Summary(spec.Prompt)), 5)
	}
	if t := hostapi.Sanitise(spec.Ticket); t != "" && !strings.HasPrefix(base, t) {
		base = strings.Trim(t+"-"+base, "-")
	}
	return cmp.Or(base, string(spec.Kind))
}

// freshID is a new session's id: short and random, apart from its name, and one no session or directory is using.
func (x *Worktrees) freshID() core.SessionID {
	st := x.st.Snapshot()
	for {
		id := x.env.NewSessionID()
		_, taken := st.Session(id)
		for _, p := range []string{filepath.Join(x.env.WorkspacesDir(), string(id)), x.env.SessionDir(id), x.env.RetiredID(id)} {
			if _, err := os.Stat(p); err == nil {
				taken = true
			}
		}
		if !taken {
			return id
		}
	}
}

// New is a fresh session of kind k as spec asks, its workspace and worktrees made as k calls for.
func (x *Worktrees) New(ctx context.Context, spec core.SessionSpec, k core.KindSpec) (core.SessionState, error) {
	name := sessionName(spec)
	se := core.SessionState{ID: x.freshID(), Name: cmp.Or(spec.Title, name), Kind: spec.Kind, Prompt: spec.Prompt, Ticket: spec.Ticket,
		Created: x.env.Now().UTC(), StartWork: k.Worktrees == core.WorktreesStartWork || k.Worktrees == core.WorktreesInit,
		Agent: core.AgentState{Provider: spec.Provider, Model: spec.Model, Effort: spec.Effort, Policy: hostcore.GateOf(spec, k),
			Sandboxed: spec.Sandbox.Container, Network: spec.Sandbox.Network, Backend: spec.Sandbox.Backend,
			CustomImage: spec.Sandbox.CustomImage}}
	se.SetData(spec.Data())
	branch := spec.Branch
	if branch == "" {
		branch = BranchName(hostcore.WhoAmI(ctx), spec.Ticket, se.ID)
	}
	var err error
	switch k.Worktrees {
	case core.WorktreesNone:
		err = inPlace(ctx, &se, spec)
	case core.WorktreesInit:
		err = x.newRepo(ctx, &se, spec, branch)
	default:
		err = x.workspace(ctx, &se, spec, k, branch)
	}
	if err != nil {
		return se, err
	}
	if p := strings.TrimSpace(spec.Prompt); k.PromptFile != "" && se.Dir != "" && p != "" {
		if err := os.WriteFile(filepath.Join(se.Dir, k.PromptFile), []byte(p+"\n"), 0o644); err != nil {
			return se, err
		}
	}
	return se, nil
}

// inPlace is a direct session: the directory itself, never a worktree.
func inPlace(ctx context.Context, se *core.SessionState, spec core.SessionSpec) error {
	if spec.Dir == "" {
		return errors.New("a direct session needs a directory")
	}
	dir, err := filepath.Abs(spec.Dir)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", spec.Dir)
	}
	se.Dir = dir
	p := core.Project{Name: filepath.Base(dir), Dir: dir, Primary: true}
	if top, err := gitx.Root(ctx, dir); err == nil && top == dir {
		p.Branch, _ = gitx.Repo{Dir: dir}.Branch(ctx)
		se.Branch = p.Branch
	}
	se.Projects = []core.Project{p}
	return nil
}

// workspace makes the workspace and each repo's worktree the kind calls for.
func (x *Worktrees) workspace(ctx context.Context, se *core.SessionState, spec core.SessionSpec, k core.KindSpec, branch string) error {
	root := filepath.Join(x.env.WorkspacesDir(), string(se.ID))
	m := hostapi.Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(spec.Kind), Ticket: spec.Ticket, Model: spec.Model}
	if err := newWorkspace(ctx, root, m, x.notesFiles()); err != nil {
		return err
	}
	se.Dir = root
	repos := slices.Clone(spec.Repos)
	if k.Worktrees == core.WorktreesPR {
		r, err := x.prRepo(ctx, spec)
		if err != nil {
			return undo(root, err)
		}
		repos = []core.RepoID{r.ID}
	}
	for _, ref := range repos {
		r, err := x.deps.Resolve(ctx, string(ref))
		if err != nil {
			return undo(root, err)
		}
		switch k.Worktrees {
		case core.WorktreesStartWork:
			// Until Start work, the plan reads the checkout itself; the guard rails keep the agent out of it.
			se.Projects = append(se.Projects, core.Project{Name: uniqueProjectName(m, r.Path), Repo: r.Path, Dir: r.Path,
				Primary: len(se.Projects) == 0, ReadOnly: true, Branch: branch})
			m.Projects = append(m.Projects, hostapi.ManifestEntry{Name: se.Projects[len(se.Projects)-1].Name, Repo: r.Path, Dir: r.Path, Primary: len(m.Projects) == 0})
		case core.WorktreesPR:
			if _, err := addPRProject(ctx, root, &m, r, spec); err != nil {
				return undo(root, err)
			}
		default:
			if _, err := addProject(ctx, root, &m, r.Path, branch, spec.Base, false); err != nil {
				return undo(root, err)
			}
		}
	}
	if k.Worktrees != core.WorktreesStartWork {
		se.Projects = manifestProjects(m, root)
	}
	if p, ok := se.Primary(); ok && p.Branch != "" {
		se.Branch = p.Branch
	}
	if k.Worktrees != core.WorktreesStartWork {
		return SaveManifest(root, m)
	}
	return nil
}

// undo takes away a workspace that failed half-made.
func undo(root string, err error) error {
	if m, ok := LoadManifest(root); ok {
		for _, p := range m.Projects {
			if !filepath.IsAbs(p.Dir) {
				_ = gitx.Repo{Dir: p.Repo}.RemoveWorktree(context.Background(), filepath.Join(root, p.Dir), true)
			}
		}
	}
	_ = os.RemoveAll(root)
	return err
}

// prRepo is the registered repo whose remote is the PR's: the spec's first repo, else the one whose remote contains
// spec.PRRemote.
func (x *Worktrees) prRepo(ctx context.Context, spec core.SessionSpec) (core.Repo, error) {
	if len(spec.Repos) > 0 {
		return x.deps.Resolve(ctx, string(spec.Repos[0]))
	}
	want := strings.ToLower(strings.TrimSpace(spec.PRRemote))
	if want == "" {
		return core.Repo{}, fmt.Errorf("no code host says which repo %s is in", spec.PR)
	}
	for _, r := range x.deps.Repos() {
		if strings.Contains(strings.ToLower(remotePath(r.Remote)), want) {
			return r, nil
		}
	}
	return core.Repo{}, fmt.Errorf("no registered repo has %s as its remote: add its checkout first", want)
}

func remotePath(remote string) string {
	if u, err := url.Parse(remote); err == nil && u.Host != "" {
		return strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), ".git")
	}
	if _, p, ok := strings.Cut(remote, ":"); ok {
		return strings.TrimSuffix(p, ".git")
	}
	return remote
}

// newRepo makes a repo from a template, registers it, and carries on as a plan for it.
func (x *Worktrees) newRepo(ctx context.Context, se *core.SessionState, spec core.SessionSpec, branch string) error {
	parent := spec.Dir
	if parent == "" {
		parent = x.deps.ReposDir()
	}
	dir := filepath.Join(parent, sessionName(spec))
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := initRepo(ctx, dir, spec.Template, filepath.Base(dir), hostcore.WhoAmI(ctx)); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	r, err := x.deps.AddRepo(ctx, dir, core.RepoSession)
	if err != nil {
		return err
	}
	spec.Repos = []core.RepoID{r.ID}
	k, _ := x.deps.Kinds.Get(core.KindPlan)
	return x.workspace(ctx, se, spec, k, branch)
}

// manifestProjects are m's projects with absolute directories under root.
func manifestProjects(m hostapi.Manifest, root string) []core.Project {
	out := make([]core.Project, 0, len(m.Projects))
	for _, p := range m.Projects {
		out = append(out, core.Project{Name: p.Name, Repo: p.Repo, Dir: filepath.Join(root, p.Dir), Branch: p.Branch, Base: p.Base,
			Primary: p.Primary, Detached: p.Detached, Commit: p.Commit, ReadOnly: p.Detached})
	}
	return out
}
