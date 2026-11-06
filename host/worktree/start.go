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
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// freshName is a session name from the prompt that no session or directory is using yet.
func (x *Worktrees) freshName(spec core.SessionSpec) string {
	base := hostcore.Sanitise(spec.Name)
	if base == "" {
		base = hostcore.TrimWords(hostcore.Sanitise(core.Summary(spec.Prompt)), 5)
	}
	if t := hostcore.Sanitise(spec.Ticket); t != "" && !strings.HasPrefix(base, t) {
		base = strings.Trim(t+"-"+base, "-")
	}
	if base == "" {
		base = string(spec.Kind)
	}
	st := x.st.Snapshot()
	out := base
	for n := 2; n < 1000; n++ {
		_, taken := st.Session(core.SessionID(out))
		if _, err := os.Stat(filepath.Join(x.env.WorkspacesDir(), out)); err == nil {
			taken = true
		}
		if _, err := os.Stat(x.env.SessionDir(core.SessionID(out))); err == nil {
			taken = true
		}
		if !taken {
			break
		}
		out = fmt.Sprintf("%s-%d", base, n)
	}
	return out
}

// New is a fresh session of kind k as spec asks, its workspace and worktrees made as k calls for.
func (x *Worktrees) New(ctx context.Context, spec core.SessionSpec, k core.KindSpec) (core.SessionState, error) {
	name := x.freshName(spec)
	se := core.SessionState{ID: core.SessionID(name), Name: cmp.Or(spec.Title, name), Kind: spec.Kind, Prompt: spec.Prompt, Ticket: spec.Ticket,
		Created: x.env.Now().UTC(), StartWork: k.Worktrees == core.WorktreesStartWork || k.Worktrees == core.WorktreesInit,
		Agent: core.AgentState{Provider: spec.Provider, Model: spec.Model, Effort: spec.Effort, Policy: hostcore.GateOf(spec, k),
			Sandboxed: spec.Sandbox.Container, Network: spec.Sandbox.Network, Backend: spec.Sandbox.Backend}}
	se.SetData(spec.Data())
	branch := spec.Branch
	if branch == "" {
		branch = BranchName(hostcore.WhoAmI(ctx), spec.Ticket, name)
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
	if top, err := gitx.TopLevel(ctx, dir); err == nil && top == dir {
		p.Branch, _ = gitx.Repo{Dir: dir}.Branch(ctx)
		se.Branch = p.Branch
	}
	se.Projects = []core.Project{p}
	return nil
}

// workspace makes the workspace and each repo's worktree the kind calls for.
func (x *Worktrees) workspace(ctx context.Context, se *core.SessionState, spec core.SessionSpec, k core.KindSpec, branch string) error {
	root := filepath.Join(x.env.WorkspacesDir(), string(se.ID))
	m := Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(spec.Kind), Ticket: spec.Ticket, Model: spec.Model}
	if err := newWorkspace(ctx, root, m); err != nil {
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
			m.Projects = append(m.Projects, ManifestEntry{Name: se.Projects[len(se.Projects)-1].Name, Repo: r.Path, Dir: r.Path, Primary: len(m.Projects) == 0})
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
		se.Projects = m.projects(root)
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

// prRepo is the registered repo whose remote is the PR's: spec.PRRemote, else the GitHub repo its URL names.
func (x *Worktrees) prRepo(ctx context.Context, spec core.SessionSpec) (core.Repo, error) {
	want := strings.TrimSpace(spec.PRRemote)
	if want == "" {
		pr, _, err := gh.ParsePR(spec.PR)
		if err != nil {
			return core.Repo{}, err
		}
		want = pr.String()
	}
	if len(spec.Repos) > 0 {
		return x.deps.Resolve(ctx, string(spec.Repos[0]))
	}
	want = strings.ToLower(want)
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
	dir := filepath.Join(parent, string(se.ID))
	if spec.Name != "" {
		dir = filepath.Join(parent, hostcore.Sanitise(spec.Name))
	}
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
