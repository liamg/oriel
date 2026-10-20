package host

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
)

var _ core.Sessions = (*Host)(nil)

func (w *Host) workspacesDir() string { return filepath.Join(w.opts.Home, "workspaces") }

// freshName is a session name from the prompt that no session or directory is using yet.
func (w *Host) freshName(spec core.SessionSpec) string {
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
func (w *Host) Start(ctx context.Context, spec core.SessionSpec) (_ core.SessionID, err error) {
	defer w.recover("Start", &err)
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
		return "", errors.New("this machine can't run a sandbox")
	}
	if b := spec.Sandbox.Backend; b != "" && spec.Sandbox.Container && !slices.Contains(w.opts.Sandbox.Backends(), b) {
		return "", fmt.Errorf("this machine can't run a %s sandbox", b)
	}
	name := w.freshName(spec)
	id := core.SessionID(name)
	se := core.SessionState{ID: id, Name: cmp.Or(spec.Title, name), Kind: spec.Kind, Prompt: spec.Prompt, Ticket: spec.Ticket,
		PR: spec.PR, Job: spec.Job, Created: w.opts.Now().UTC(), Unplanned: spec.Kind == core.KindWork && spec.Ticket == "",
		StartWork: k.Worktrees == WorktreesStartWork || k.Worktrees == WorktreesInit, CommitGate: k.Gate,
		Agent: core.AgentState{Provider: spec.Provider, Model: spec.Model, Effort: spec.Effort, Policy: gate(spec),
			Sandboxed: spec.Sandbox.Container, Network: spec.Sandbox.Network, Backend: spec.Sandbox.Backend}}
	branch := spec.Branch
	if branch == "" {
		branch = BranchName(whoAmI(ctx), spec.Ticket, name)
	}
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
	if p := strings.TrimSpace(spec.Prompt); k.PromptFile != "" && se.Dir != "" && p != "" {
		if err := os.WriteFile(filepath.Join(se.Dir, k.PromptFile), []byte(p+"\n"), 0o644); err != nil {
			return "", err
		}
	}
	se.Status = core.DeriveStatus(se)
	if err := w.emit(core.SessionSynced{Session: se}); err != nil {
		return "", err
	}
	if k.AutoReview {
		if _, err := w.AIReview(context.WithoutCancel(ctx), AIReviewReq{Session: id}); err != nil {
			w.log.Warn("AI review", "session", id, "err", err)
		}
	}
	if p, body := strings.TrimSpace(spec.Prompt), firstRequest(spec); body != "" {
		if _, err := w.Comment(ctx, CommentReq{Session: id, Title: firstLine(cmp.Or(p, body)), Body: body}); err != nil {
			return id, err
		}
	} else if spec.Provider != "" {
		w.sessionOf(id).startAside()
	}
	return id, nil
}

// firstRequest is what the agent is first asked: the prompt, then what came with it.
func firstRequest(spec core.SessionSpec) string {
	p, c := strings.TrimSpace(spec.Prompt), strings.TrimSpace(spec.Context)
	switch {
	case c == "":
		return p
	case p == "":
		return c
	}
	return p + "\n\n" + c
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

// startInPlace is a direct session: the directory itself, never a worktree.
func (w *Host) startInPlace(ctx context.Context, se *core.SessionState, spec core.SessionSpec) error {
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
func (w *Host) startWorkspace(ctx context.Context, se *core.SessionState, spec core.SessionSpec, k KindSpec, branch string) error {
	root := filepath.Join(w.workspacesDir(), string(se.ID))
	m := Manifest{Name: string(se.ID), CreatedAt: se.Created, Kind: string(spec.Kind), Ticket: spec.Ticket, Model: spec.Model}
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
			if _, err := w.addPRProject(ctx, root, &m, r, spec); err != nil {
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
func (w *Host) undo(root string, err error) error {
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
func (w *Host) prRepo(ctx context.Context, spec core.SessionSpec) (core.Repo, error) {
	want := strings.TrimSpace(spec.PRRemote)
	if want == "" {
		pr, _, err := gh.ParsePR(spec.PR)
		if err != nil {
			return core.Repo{}, err
		}
		want = pr.String()
	}
	if len(spec.Repos) > 0 {
		return w.resolveRepo(ctx, string(spec.Repos[0]))
	}
	want = strings.ToLower(want)
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

// addPRProject fetches the PR's head (spec.PRRef, else GitHub's pull/N/head) and pins a worktree there.
func (w *Host) addPRProject(ctx context.Context, root string, m *Manifest, r core.Repo, spec core.SessionSpec) (ManifestEntry, error) {
	from, ref := strings.TrimSpace(spec.PRRef), "refs/oriel/pr/"+filepath.Base(root)
	if from == "" {
		_, n, err := gh.ParsePR(spec.PR)
		if err != nil {
			return ManifestEntry{}, err
		}
		from, ref = fmt.Sprintf("pull/%d/head", n), fmt.Sprintf("refs/oriel/pr/%d", n)
	}
	g := gitx.Repo{Dir: r.Path}
	if _, err := g.Run(ctx, "fetch", "--quiet", "origin", "+"+from+":"+ref); err != nil {
		return ManifestEntry{}, fmt.Errorf("fetching %s: %w", from, err)
	}
	return addProject(ctx, root, m, r.Path, "", ref, true)
}

// startNewRepo makes a repo from a template, registers it, and carries on as a plan for it.
func (w *Host) startNewRepo(ctx context.Context, se *core.SessionState, spec core.SessionSpec, branch string) error {
	parent := spec.Dir
	if parent == "" {
		parent = w.reposDir()
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
	r, err := w.addRepo(ctx, dir, core.RepoSession)
	if err != nil {
		return err
	}
	spec.Repos = []core.RepoID{r.ID}
	k, _ := w.kinds.Get(core.KindPlan)
	return w.startWorkspace(ctx, se, spec, k, branch)
}

// Resume starts the session's agent again: by its own id when it had one, so the conversation carries on.
func (w *Host) Resume(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("Resume", &err)
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
	_, err = w.sessionFor(id, true)
	return err
}

// Stop stops the session's agent. Its threads wait for it.
func (w *Host) Stop(_ context.Context, id core.SessionID) (err error) {
	defer w.recover("Stop", &err)
	if _, err := w.session(id); err != nil {
		return err
	}
	if h, _ := w.sessionFor(id, false); h != nil {
		h.stop("stopped", core.StoppedByYou)
	}
	return nil
}

// Archive stops the agent and removes the worktrees, keeping threads, transcript and test runs. Worktrees with
// uncommitted work are refused: removing them would lose it.
func (w *Host) Archive(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("Archive", &err)
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Kind != core.KindDirect {
		for _, p := range se.Projects {
			if p.Repo != "" && p.Dir != p.Repo && (gitx.Repo{Dir: p.Dir}).Dirty(ctx) {
				return fmt.Errorf("%s has uncommitted work: commit or discard it first", p.Name)
			}
		}
	}
	if h, _ := w.sessionFor(id, false); h != nil {
		h.stop("archived", core.StoppedByYou)
	}
	if se.Kind != core.KindDirect {
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
func (w *Host) NextPR(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("NextPR", &err)
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
	w.writeNotes(id)
	w.saveProjects(id)
	return nil
}

// StartWork makes a plan's worktrees and tells its agent to implement the plan (05-plan), as r asks: on the branches
// it names, clearing the plan's threads into a note unless they're kept, and with the agent restarted fresh on the
// work pack unless its conversation is kept, on the sandbox, network, model and effort given.
func (w *Host) StartWork(ctx context.Context, r StartWorkReq) (err error) {
	defer w.recover("StartWork", &err)
	id := r.Session
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
	for name, b := range r.Branches {
		if !slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Name == name && p.ReadOnly && p.Repo != "" }) {
			return fmt.Errorf("no project %q waits on Start work for its worktree", name)
		}
		if err := gitx.ValidBranch(ctx, b); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	if r.Sandbox != nil && *r.Sandbox && w.opts.Sandbox == nil {
		return errors.New("this machine can't run a sandbox")
	}
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
		if b := r.Branches[p.Name]; b != "" {
			branch = b
		}
		e, err := addProject(ctx, se.Dir, &m, p.Repo, branch, "", false)
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
	w.writeNotes(id)
	prompt := startWorkPrompt(projects)
	if !r.KeepThreads {
		note, err := w.clearPlanThreads(se)
		if err != nil {
			return err
		}
		prompt += note
	}
	if err := w.workAgent(id, r, "start work"); err != nil {
		return err
	}
	_, err = w.Comment(ctx, CommentReq{Session: id, Thread: se.PlanThread(), Title: "Start work", Body: prompt})
	return err
}

// clearPlanThreads takes away every thread but the plan's own, and returns the open and deferred ones as a note for
// the work prompt.
func (w *Host) clearPlanThreads(se core.SessionState) (string, error) {
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
	return b.String(), w.emit(core.ThreadsRemoved{Session: se.ID, Threads: gone})
}

// threadPlace is where a thread is, as the work prompt names it: `PLAN.md:14` , or nothing for the whole change.
func threadPlace(t core.Thread) string {
	if t.Anchor == nil || t.Anchor.File == "" {
		return ""
	}
	return fmt.Sprintf("`%s:%d` ", t.Anchor.File, t.Anchor.Line)
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// Configure restarts the session's agent on another model or effort, its conversation kept. It refuses mid-turn,
// which a restart would cut off.
func (w *Host) Configure(ctx context.Context, r ConfigureReq) (err error) {
	defer w.recover("Configure", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	switch {
	case se.Agent.Provider == "":
		return errors.New("this session has no agent")
	case se.Agent.Working:
		return errors.New("switch when the agent is idle")
	}
	running := se.Agent.Live()
	if err := w.workAgent(r.Session, StartWorkReq{Model: r.Model, Effort: r.Effort, KeepConversation: true}, "switching model"); err != nil {
		return err
	}
	if !running {
		return nil
	}
	return w.Resume(ctx, r.Session)
}

// workAgent readies the session's agent: on the model, effort, sandbox and network r gives, restarted fresh on the
// work pack unless its conversation is kept, and restarted by its own id when only those changed; why is the stop's.
func (w *Host) workAgent(id core.SessionID, r StartWorkReq, why string) error {
	se, err := w.session(id)
	if err != nil {
		return err
	}
	a := se.Agent
	if a.Provider == "" {
		return nil
	}
	if r.Model != "" && r.Model != a.Model {
		a.Model, a.Effort = r.Model, ""
	}
	if r.Effort != "" {
		a.Effort = r.Effort
	}
	if r.Sandbox != nil && *r.Sandbox != a.Sandboxed {
		was := core.SessionSpec{Sandbox: core.SandboxPolicy{Container: a.Sandboxed}}
		if a.Policy == gate(was) {
			a.Policy = gate(core.SessionSpec{Sandbox: core.SandboxPolicy{Container: *r.Sandbox}})
		}
		a.Sandboxed = *r.Sandbox
	}
	if r.Network != "" {
		a.Network = r.Network
	}
	if !r.KeepConversation {
		a.Session, a.Pack = "", "work"
	}
	if a.Model == se.Agent.Model && a.Effort == se.Agent.Effort && a.Sandboxed == se.Agent.Sandboxed && a.Network == se.Agent.Network &&
		a.Session == se.Agent.Session && a.Pack == se.Agent.Pack {
		return nil
	}
	if h, _ := w.sessionFor(id, false); h != nil {
		h.stop(why, "")
	}
	w.edit.Lock()
	defer w.edit.Unlock()
	se, err = w.session(id)
	if err != nil {
		return err
	}
	a.Running, a.Working, a.TurnStarted, a.Queue = se.Agent.Running, se.Agent.Working, se.Agent.TurnStarted, se.Agent.Queue
	return w.emit(core.AgentChanged{ID: id, Agent: a})
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
func (w *Host) AddRepo(ctx context.Context, id core.SessionID, ref core.RepoID) (err error) {
	defer w.recover("AddRepo", &err)
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Kind == core.KindDirect {
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
	m, ok := LoadManifest(se.Dir)
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
		e, err := addProject(ctx, se.Dir, &m, r.Path, branch, "", false)
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
	w.writeNotes(id)
	if h, _ := w.sessionFor(id, false); h != nil {
		h.note(fmt.Sprintf("The repo %s joined this session at %s.", r.ID, projects[len(projects)-1].Dir))
	}
	return nil
}

// saveProjects writes a workspace's projects back to its manifest.
func (w *Host) saveProjects(id core.SessionID) {
	se, err := w.session(id)
	if err != nil {
		return
	}
	m, ok := LoadManifest(se.Dir)
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
func (w *Host) Rename(id core.SessionID, name string) (err error) {
	defer w.recover("Rename", &err)
	return w.emit(core.SessionRenamed{ID: id, Name: strings.TrimSpace(name)})
}

// Delete throws a session away: its agent stops, its worktrees go with whatever is in them, then its workspace and
// its log. The repos themselves are never touched. A direct session's directory isn't Oriel's to remove, so it's
// refused.
func (w *Host) Delete(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("Delete", &err)
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Kind == core.KindDirect {
		return fmt.Errorf("%s isn't Oriel's to delete: archive it instead", id)
	}
	if h, _ := w.sessionFor(id, false); h != nil {
		h.stop("deleted", "")
	}
	for _, p := range se.Projects {
		if p.Repo == "" || p.Dir == p.Repo {
			continue
		}
		if err := (gitx.Repo{Dir: p.Repo}).RemoveWorktree(ctx, p.Dir, true); err != nil {
			if _, serr := os.Stat(p.Dir); serr == nil {
				return fmt.Errorf("%s: %w", p.Name, err)
			}
		}
	}
	if se.Dir != "" && within(se.Dir, w.workspacesDir()) {
		if err := os.RemoveAll(se.Dir); err != nil {
			return err
		}
	}
	if err := w.emit(core.SessionClosed{ID: id}); err != nil {
		return err
	}
	w.mu.Lock()
	if l := w.recs[id]; l != nil {
		_ = l.f.Close()
		delete(w.recs, id)
	}
	if l := w.open[id]; l != nil {
		_ = l.Close()
		delete(w.open, id)
	}
	delete(w.live, id)
	w.mu.Unlock()
	return os.RemoveAll(w.sessionDir(id))
}

// within says whether path is under dir.
func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
