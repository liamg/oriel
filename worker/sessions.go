package worker

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

var _ core.Sessions = (*Worker)(nil)

func (w *Worker) workspacesDir() string { return filepath.Join(w.opts.Home, "workspaces") }

// freshName is a session name from the prompt that no session or directory is using yet.
func (w *Worker) freshName(spec core.SessionSpec) string {
	base := Sanitise(spec.Name)
	if base == "" {
		base = trimWords(Sanitise(firstLine(spec.Prompt)), 5)
	}
	if t := Sanitise(spec.Ticket); t != "" && !strings.HasPrefix(base, t) {
		base = strings.Trim(t+"-"+base, "-")
	}
	if base == "" {
		base = string(spec.Kind)
	}
	st := w.store.State()
	out := base
	for n := 2; n < 1000; n++ {
		_, taken := st.Session(core.SessionID(out))
		if _, err := os.Stat(filepath.Join(w.workspacesDir(), out)); err == nil {
			taken = true
		}
		if _, err := os.Stat(w.sessionDir(core.SessionID(out))); err == nil {
			taken = true
		}
		if !taken {
			break
		}
		out = fmt.Sprintf("%s-%d", base, n)
	}
	return out
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// Start starts a session of the spec's kind (sessions.md → Kinds).
func (w *Worker) Start(ctx context.Context, spec core.SessionSpec) (core.SessionID, error) {
	k, ok := w.kinds.Get(spec.Kind)
	if !ok {
		return "", fmt.Errorf("no kind of session %q", spec.Kind)
	}
	if k.NeedsRepo && len(spec.Repos) == 0 {
		return "", fmt.Errorf("a %s session needs a repo", strings.ToLower(k.Title))
	}
	if spec.Provider != "" {
		if _, ok := w.opts.Providers.Get(spec.Provider); !ok {
			return "", fmt.Errorf("no agent provider %q", spec.Provider)
		}
	}
	if spec.Sandbox.Container && w.opts.Sandbox == nil {
		return "", errors.New("this worker can't run a sandbox")
	}
	name := w.freshName(spec)
	id := core.SessionID(name)
	se := core.SessionState{ID: id, Name: name, Kind: spec.Kind, Worker: w.opts.ID, Prompt: spec.Prompt, Ticket: spec.Ticket,
		PR: spec.PR, Created: w.opts.Now().UTC(), Unplanned: spec.Kind == core.KindWork && spec.Ticket == "",
		StartWork: k.Worktrees == WorktreesStartWork || k.Worktrees == WorktreesInit,
		Agent: core.AgentState{Provider: spec.Provider, Model: spec.Model, Effort: spec.Effort, Policy: gate(spec),
			Sandboxed: spec.Sandbox.Container, Network: spec.Sandbox.Network}}
	branch := spec.Branch
	if branch == "" {
		branch = BranchName(whoAmI(ctx), spec.Ticket, name)
	}
	var err error
	switch k.Worktrees {
	case WorktreesNone:
		err = w.startInPlace(ctx, &se, spec)
	case WorktreesInit:
		err = w.startNewRepo(ctx, &se, spec, branch)
	default:
		err = w.startWorkspace(ctx, &se, spec, k, branch)
	}
	if err != nil {
		return "", err
	}
	se.Status = core.DeriveStatus(se)
	if err := w.emit(core.SessionSynced{Session: se}); err != nil {
		return "", err
	}
	if p := strings.TrimSpace(spec.Prompt); p != "" {
		if _, err := w.Comment(ctx, CommentReq{Session: id, Title: firstLine(p), Body: p}); err != nil {
			return id, err
		}
	} else if spec.Provider != "" {
		if err := w.Resume(ctx, id); err != nil {
			return id, err
		}
	}
	return id, nil
}

// gate is the agent's own gate for a spec: the light gate inside a sandbox by default (agents.md).
func gate(spec core.SessionSpec) string {
	if spec.Sandbox.Gate != "" {
		return spec.Sandbox.Gate
	}
	if spec.Sandbox.Container {
		return "light"
	}
	return "ask"
}

// startInPlace is a direct session (the directory itself, never a worktree) or a scratch one (no repo at all).
func (w *Worker) startInPlace(ctx context.Context, se *core.SessionState, spec core.SessionSpec) error {
	if se.Kind == core.KindScratch {
		se.Dir = filepath.Join(w.workspacesDir(), string(se.ID))
		return os.MkdirAll(se.Dir, 0o755)
	}
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

// startWorkspace makes the workspace and each repo's worktree the kind calls for.
func (w *Worker) startWorkspace(ctx context.Context, se *core.SessionState, spec core.SessionSpec, k KindSpec, branch string) error {
	root := filepath.Join(w.workspacesDir(), string(se.ID))
	m := Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(spec.Kind), Ticket: spec.Ticket, Model: spec.Model}
	if spec.Kind == core.KindTour {
		m.Tour = &TourInfo{Title: spec.Prompt, Kind: spec.TourKind}
	}
	if err := newWorkspace(ctx, root, m); err != nil {
		return err
	}
	se.Dir = root
	repos := slices.Clone(spec.Repos)
	if k.Worktrees == WorktreesPR {
		r, err := w.prRepo(ctx, spec)
		if err != nil {
			return w.undo(root, err)
		}
		repos = []core.RepoID{r.ID}
	}
	for _, ref := range repos {
		r, err := w.resolveRepo(ctx, string(ref))
		if err != nil {
			return w.undo(root, err)
		}
		switch k.Worktrees {
		case WorktreesStartWork:
			// Until Start work, the plan reads the checkout itself; the guard rails keep the agent out of it.
			se.Projects = append(se.Projects, core.Project{Name: uniqueProjectName(m, r.Path), Repo: r.Path, Dir: r.Path,
				Primary: len(se.Projects) == 0, ReadOnly: true, Branch: branch})
			m.Projects = append(m.Projects, ManifestEntry{Name: se.Projects[len(se.Projects)-1].Name, Repo: r.Path, Dir: r.Path, Primary: len(m.Projects) == 0})
		case WorktreesPR:
			if _, err := w.addPRProject(ctx, root, &m, r, spec.PR); err != nil {
				return w.undo(root, err)
			}
		case WorktreesPinned:
			if _, err := addProject(ctx, root, &m, r.Path, "", "", true); err != nil {
				return w.undo(root, err)
			}
		default:
			if _, err := addProject(ctx, root, &m, r.Path, branch, spec.Base, false); err != nil {
				return w.undo(root, err)
			}
		}
	}
	if k.Worktrees != WorktreesStartWork {
		se.Projects = m.projects(root)
	}
	if p, ok := se.Primary(); ok && p.Branch != "" {
		se.Branch = p.Branch
	}
	if k.Worktrees != WorktreesStartWork {
		return saveManifest(root, m)
	}
	return nil
}

// undo takes away a workspace that failed half-made.
func (w *Worker) undo(root string, err error) error {
	if m, _, ok := LoadManifest(root); ok {
		for _, p := range m.Projects {
			if !filepath.IsAbs(p.Dir) {
				_ = gitx.Repo{Dir: p.Repo}.RemoveWorktree(context.Background(), filepath.Join(root, p.Dir), true)
			}
		}
	}
	_ = os.RemoveAll(root)
	return err
}

var prURL = regexp.MustCompile(`github\.com[/:]([^/]+)/([^/]+?)(?:\.git)?/pull/(\d+)`)

// prRepo is the registered repo whose remote is the PR's.
func (w *Worker) prRepo(ctx context.Context, spec core.SessionSpec) (core.Repo, error) {
	m := prURL.FindStringSubmatch(spec.PR)
	if m == nil {
		return core.Repo{}, fmt.Errorf("%q is not a GitHub pull request URL", spec.PR)
	}
	if len(spec.Repos) > 0 {
		return w.resolveRepo(ctx, string(spec.Repos[0]))
	}
	want := strings.ToLower(m[1] + "/" + m[2])
	for _, r := range w.repos.all() {
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

// addPRProject fetches the PR's head and pins a worktree there.
func (w *Worker) addPRProject(ctx context.Context, root string, m *Manifest, r core.Repo, pr string) (ManifestEntry, error) {
	sm := prURL.FindStringSubmatch(pr)
	n, _ := strconv.Atoi(sm[3])
	ref := fmt.Sprintf("refs/oriel/pr/%d", n)
	g := gitx.Repo{Dir: r.Path}
	if _, err := g.Run(ctx, "fetch", "--quiet", "origin", fmt.Sprintf("+pull/%d/head:%s", n, ref)); err != nil {
		return ManifestEntry{}, fmt.Errorf("fetching #%d: %w", n, err)
	}
	return addProject(ctx, root, m, r.Path, "", ref, true)
}

// startNewRepo makes a repo from a template, registers it, and carries on as a plan for it.
func (w *Worker) startNewRepo(ctx context.Context, se *core.SessionState, spec core.SessionSpec, branch string) error {
	parent := spec.Dir
	if parent == "" {
		home, _ := os.UserHomeDir()
		parent = filepath.Join(home, "src")
	}
	dir := filepath.Join(parent, string(se.ID))
	if spec.Name != "" {
		dir = filepath.Join(parent, Sanitise(spec.Name))
	}
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("%s already exists", dir)
	}
	if err := initRepo(ctx, dir, spec.Template, filepath.Base(dir), whoAmI(ctx)); err != nil {
		_ = os.RemoveAll(dir)
		return err
	}
	r, err := w.repos.add(ctx, w.opts.ID, dir)
	if err != nil {
		return err
	}
	spec.Repos = []core.RepoID{r.ID}
	k, _ := w.kinds.Get(core.KindPlan)
	return w.startWorkspace(ctx, se, spec, k, branch)
}

// Resume starts the session's agent again: by its own id when it had one, so the conversation carries on.
func (w *Worker) Resume(ctx context.Context, id core.SessionID) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Archived {
		if err := w.emit(core.SessionArchived{ID: id}); err != nil {
			return err
		}
	}
	if se.Agent.Provider == "" {
		w.restatus(id)
		return nil
	}
	_, err = w.hostFor(id, true)
	return err
}

// Stop stops the session's agent. Its threads wait for it.
func (w *Worker) Stop(_ context.Context, id core.SessionID) error {
	if _, err := w.session(id); err != nil {
		return err
	}
	if h, _ := w.hostFor(id, false); h != nil {
		h.stop("stopped")
	}
	return nil
}

// Archive stops the agent and removes the worktrees, keeping threads, transcript and test runs. Worktrees with
// uncommitted work are refused: removing them would lose it.
func (w *Worker) Archive(ctx context.Context, id core.SessionID) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Source != "crit" && se.Kind != core.KindDirect {
		for _, p := range se.Projects {
			if p.Repo != "" && p.Dir != p.Repo && (gitx.Repo{Dir: p.Dir}).Dirty(ctx) {
				return fmt.Errorf("%s has uncommitted work: commit or discard it first", p.Name)
			}
		}
	}
	if h, _ := w.hostFor(id, false); h != nil {
		h.stop("archived")
	}
	if se.Source != "crit" && se.Kind != core.KindDirect {
		for _, p := range se.Projects {
			if p.Repo == "" || p.Dir == p.Repo {
				continue
			}
			if err := (gitx.Repo{Dir: p.Repo}).RemoveWorktree(ctx, p.Dir, false); err != nil && !errors.Is(err, os.ErrNotExist) {
				if _, serr := os.Stat(p.Dir); serr == nil {
					return err
				}
			}
		}
	}
	if err := w.emit(core.SessionArchived{ID: id, Archived: true}); err != nil {
		return err
	}
	w.restatus(id)
	return nil
}

// NextPR moves each project onto a fresh branch cut from the updated base, keeping the last as a past PR.
func (w *Worker) NextPR(ctx context.Context, id core.SessionID) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	branch := BranchName(whoAmI(ctx), se.Ticket, string(se.ID)) + "-next"
	projects := slices.Clone(se.Projects)
	past := slices.Clone(se.Past)
	moved := false
	for i, p := range projects {
		if p.Detached || p.Repo == "" || p.Dir == p.Repo {
			continue
		}
		r := gitx.Repo{Dir: p.Repo}
		fresh := r.FreshBranch(ctx, branch)
		base := r.SyncedBase(ctx)
		if _, err := (gitx.Repo{Dir: p.Dir}).Run(ctx, "checkout", "--quiet", "-b", fresh, base); err != nil {
			return fmt.Errorf("%s: %w", p.Name, err)
		}
		past = append(past, core.PastPR{Project: p.Name, Branch: p.Branch, Closed: w.opts.Now().UTC()})
		projects[i].Branch, projects[i].Base = fresh, base
		moved = true
	}
	if !moved {
		return errors.New("no project here has a branch to move on from")
	}
	b := se.Branch
	if p, ok := (core.SessionState{Projects: projects}).Primary(); ok {
		b = p.Branch
	}
	if err := w.emit(core.ProjectsChanged{ID: id, Branch: b, Projects: projects, Past: past, Started: se.Started}); err != nil {
		return err
	}
	w.saveProjects(id)
	return nil
}

// StartWork makes a plan's worktrees and tells its agent to implement the plan (05-plan).
func (w *Worker) StartWork(ctx context.Context, id core.SessionID) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	switch {
	case !se.StartWork:
		return errors.New("this session has no plan to start work on")
	case se.Started:
		return errors.New("work has already started")
	}
	if held := se.Holding(); len(held) > 0 {
		return fmt.Errorf("%q is still open: resolve or defer it first", held[0].Heading())
	}
	m, _, ok := LoadManifest(se.Dir)
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
		e, err := addProject(ctx, se.Dir, &m, p.Repo, p.Branch, "", false)
		if err != nil {
			return err
		}
		projects = append(projects, core.Project{Name: e.Name, Repo: e.Repo, Dir: filepath.Join(se.Dir, e.Dir), Branch: e.Branch, Base: e.Base, Primary: e.Primary})
	}
	m.WorkStartedAt = w.opts.Now().UTC()
	if err := saveManifest(se.Dir, m); err != nil {
		return err
	}
	branch := se.Branch
	if p, ok := (core.SessionState{Projects: projects}).Primary(); ok {
		branch = p.Branch
	}
	if err := w.emit(core.ProjectsChanged{ID: id, Branch: branch, Projects: projects, Past: se.Past, Started: true}); err != nil {
		return err
	}
	_, err = w.Comment(ctx, CommentReq{Session: id, Thread: se.PlanThread(), Title: "Start work", Body: startWorkPrompt(projects)})
	return err
}

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

// AddRepo brings another repo into a session, on the session's branch.
func (w *Worker) AddRepo(ctx context.Context, id core.SessionID, ref core.RepoID) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Kind == core.KindDirect || se.Kind == core.KindScratch {
		return fmt.Errorf("a %s session has no workspace to add a repo to", se.Kind)
	}
	r, err := w.resolveRepo(ctx, string(ref))
	if err != nil {
		return err
	}
	if slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Repo == r.Path }) {
		return fmt.Errorf("%s is already in this session", r.ID)
	}
	k, _ := w.kinds.Get(se.Kind)
	m, _, ok := LoadManifest(se.Dir)
	if !ok {
		m = Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(se.Kind)}
	}
	branch := se.Branch
	if branch == "" {
		branch = BranchName(whoAmI(ctx), se.Ticket, string(se.ID))
	}
	projects := slices.Clone(se.Projects)
	switch {
	case k.Worktrees == WorktreesStartWork && !se.Started:
		projects = append(projects, core.Project{Name: uniqueProjectName(m, r.Path), Repo: r.Path, Dir: r.Path, ReadOnly: true, Branch: branch, Primary: len(projects) == 0})
	default:
		e, err := addProject(ctx, se.Dir, &m, r.Path, branch, "", k.Worktrees == WorktreesPinned)
		if err != nil {
			return err
		}
		if err := saveManifest(se.Dir, m); err != nil {
			return err
		}
		projects = append(projects, core.Project{Name: e.Name, Repo: e.Repo, Dir: filepath.Join(se.Dir, e.Dir), Branch: e.Branch, Base: e.Base,
			Primary: len(projects) == 0, Detached: e.Detached, Commit: e.Commit, ReadOnly: e.Detached})
	}
	b := se.Branch
	if b == "" {
		b = branch
	}
	if err := w.emit(core.ProjectsChanged{ID: id, Branch: b, Projects: projects, Past: se.Past, Started: se.Started}); err != nil {
		return err
	}
	if h, _ := w.hostFor(id, false); h != nil {
		h.note(fmt.Sprintf("The repo %s joined this session at %s.", r.ID, projects[len(projects)-1].Dir))
	}
	return nil
}

// saveProjects writes a workspace's projects back to its manifest.
func (w *Worker) saveProjects(id core.SessionID) {
	se, err := w.session(id)
	if err != nil || se.Source == "crit" {
		return
	}
	m, _, ok := LoadManifest(se.Dir)
	if !ok {
		return
	}
	for i, e := range m.Projects {
		for _, p := range se.Projects {
			if p.Name == e.Name {
				m.Projects[i].Branch, m.Projects[i].Base = p.Branch, p.Base
			}
		}
		m.Projects[i].Past = nil
		for _, pr := range se.Past {
			if pr.Project == e.Name {
				m.Projects[i].Past = append(m.Projects[i].Past, ManifestPR{Branch: pr.Branch, Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State, ClosedAt: pr.Closed})
			}
		}
	}
	_ = saveManifest(se.Dir, m)
}

// Rename names a session.
func (w *Worker) Rename(id core.SessionID, name string) error {
	return w.emit(core.SessionRenamed{ID: id, Name: strings.TrimSpace(name)})
}
