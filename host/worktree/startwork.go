package worktree

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// CanStartWork refuses Start work on a session that has no plan waiting for it, or on branches it can't make.
func CanStartWork(ctx context.Context, se core.SessionState, branches map[string]string) error {
	switch {
	case !se.StartWork:
		return errors.New("this session has no plan to start work on")
	case se.Started:
		return errors.New("work has already started")
	}
	if held := se.Holding(); len(held) > 0 {
		return fmt.Errorf("%q is still open: resolve or defer it first", held[0].Heading())
	}
	for name, b := range branches {
		if !slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Name == name && p.ReadOnly && p.Repo != "" }) {
			return fmt.Errorf("no project %q waits on Start work for its worktree", name)
		}
		if err := gitx.ValidBranch(ctx, b); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	return nil
}

// StartWork makes a plan's worktrees (05-plan) on the branches it names, a project's planned one where it names none,
// and clears the plan's threads unless they're kept. It returns the prompt that tells the agent to implement the plan,
// with the threads cleared that weren't settled.
func (x *Worktrees) StartWork(ctx context.Context, se core.SessionState, branches map[string]string, keepThreads bool) (string, error) {
	m, ok := LoadManifest(se.Dir)
	if !ok {
		m = Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(se.Kind)}
	}
	m.Projects = nil
	var projects []core.Project
	for _, p := range se.Projects {
		if !p.ReadOnly || p.Repo == "" {
			projects = append(projects, p)
			continue
		}
		branch := p.Branch
		if b := branches[p.Name]; b != "" {
			branch = b
		}
		e, err := addProject(ctx, se.Dir, &m, p.Repo, branch, "", false)
		if err != nil {
			return "", err
		}
		projects = append(projects, core.Project{Name: e.Name, Repo: e.Repo, Dir: filepath.Join(se.Dir, e.Dir), Branch: e.Branch, Base: e.Base, Primary: e.Primary})
	}
	m.WorkStartedAt = x.env.Now().UTC()
	if err := SaveManifest(se.Dir, m); err != nil {
		return "", err
	}
	branch := se.Branch
	if p, ok := (core.SessionState{Projects: projects}).Primary(); ok {
		branch = p.Branch
	}
	if err := x.st.Emit(core.ProjectsChanged{ID: se.ID, Branch: branch, Projects: projects, Past: se.Past, Started: true}); err != nil {
		return "", err
	}
	prompt := startWorkPrompt(projects)
	if !keepThreads {
		note, err := x.clearPlanThreads(se)
		if err != nil {
			return "", err
		}
		prompt += note
	}
	return prompt, nil
}

// clearPlanThreads takes away every thread but the plan's own, and returns the open and deferred ones as a note for
// the work prompt.
func (x *Worktrees) clearPlanThreads(se core.SessionState) (string, error) {
	plan := se.PlanThread()
	var gone []core.ThreadID
	var b strings.Builder
	for _, t := range se.Threads {
		if t.ID == plan {
			continue
		}
		gone = append(gone, t.ID)
		if t.Btw || (!t.Open() && t.Status != core.ThreadDeferred) || len(t.Comments) == 0 {
			continue
		}
		if b.Len() == 0 {
			b.WriteString("\n\nThese threads from planning weren't settled, so they come with the work:\n\n")
		}
		status := string(t.Status)
		if t.Open() {
			status = string(core.ThreadOpen)
		}
		fmt.Fprintf(&b, "- %s(%s): %s", threadPlace(t), status, oneLine(t.Comments[0].Body))
		if last, _ := t.Last(); len(t.Comments) > 1 && !last.Progress {
			fmt.Fprintf(&b, " Last: %s", oneLine(last.Body))
		}
		b.WriteString("\n")
	}
	if len(gone) == 0 {
		return "", nil
	}
	return b.String(), x.st.Emit(core.ThreadsRemoved{Session: se.ID, Threads: gone})
}

// threadPlace is where a thread is, as the work prompt names it: `PLAN.md:14` , or nothing for the whole change.
func threadPlace(t core.Thread) string {
	if t.Anchor == nil || t.Anchor.File == "" {
		return ""
	}
	return fmt.Sprintf("`%s:%d` ", t.Anchor.File, t.Anchor.Line)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// startWorkPrompt tells the agent the plan is approved, and where to implement it (crit's plan.startwork).
func startWorkPrompt(projects []core.Project) string {
	var b strings.Builder
	b.WriteString("**Start work**: the plan is approved as it stands. Implement it here, in this workspace.\n\n")
	b.WriteString("The plan is the docs at the workspace root (`PLAN.md` and the rest). Read them first: they are the whole plan.\n\n")
	if len(projects) == 0 {
		b.WriteString("There are no projects yet: add the repositories the plan needs, then implement it in them.\n\n")
	} else {
		b.WriteString("Edit inside these worktrees, not their original checkouts:\n\n")
		for _, p := range projects {
			fmt.Fprintf(&b, "- `%s`: %s (branch `%s`)\n", p.Dir, p.Repo, p.Branch)
		}
		b.WriteString("\n")
	}
	b.WriteString("Reply here with anything that deviates from the plan.")
	return b.String()
}

// AddRepo brings another repo into a session, on the session's branch, and answers the repo and its project.
func (x *Worktrees) AddRepo(ctx context.Context, se core.SessionState, ref core.RepoID) (core.Repo, core.Project, error) {
	r, err := x.deps.Resolve(ctx, string(ref))
	if err != nil {
		return r, core.Project{}, err
	}
	if slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Repo == r.Path }) {
		return r, core.Project{}, fmt.Errorf("%s is already in this session", r.ID)
	}
	k, _ := x.deps.Kinds.Get(se.Kind)
	m, ok := LoadManifest(se.Dir)
	if !ok {
		m = Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(se.Kind)}
	}
	branch := se.Branch
	if branch == "" {
		branch = BranchName(hostcore.WhoAmI(ctx), se.Ticket, string(se.ID))
	}
	projects := slices.Clone(se.Projects)
	switch {
	case k.Worktrees == core.WorktreesStartWork && !se.Started:
		projects = append(projects, core.Project{Name: uniqueProjectName(m, r.Path), Repo: r.Path, Dir: r.Path, ReadOnly: true, Branch: branch, Primary: len(projects) == 0})
	default:
		e, err := addProject(ctx, se.Dir, &m, r.Path, branch, "", false)
		if err != nil {
			return r, core.Project{}, err
		}
		if err := SaveManifest(se.Dir, m); err != nil {
			return r, core.Project{}, err
		}
		projects = append(projects, core.Project{Name: e.Name, Repo: e.Repo, Dir: filepath.Join(se.Dir, e.Dir), Branch: e.Branch, Base: e.Base,
			Primary: len(projects) == 0, Detached: e.Detached, Commit: e.Commit, ReadOnly: e.Detached})
	}
	b := se.Branch
	if b == "" {
		b = branch
	}
	if err := x.st.Emit(core.ProjectsChanged{ID: se.ID, Branch: b, Projects: projects, Past: se.Past, Started: se.Started}); err != nil {
		return r, core.Project{}, err
	}
	return r, projects[len(projects)-1], nil
}
