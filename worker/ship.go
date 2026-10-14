package worker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/wire"
)

// The Ship page's side on the worker (ui.md 18): each project's way from uncommitted work to a merged PR, read from
// its worktree and GitHub, and the steps along it the user takes from the app. They run here, where the checkout and
// the user's credentials are, with the sandbox's git guard in a sandboxed workspace.

// Ship is a project on its way to a merged pull request.
type Ship struct {
	Project   string       `json:"project"`
	Branch    string       `json:"branch,omitempty"`
	Base      string       `json:"base,omitempty"`     // the branch it merges into: main
	Remote    bool         `json:"remote,omitempty"`   // it has an origin to push to
	GitHub    string       `json:"github,omitempty"`   // owner/name, when origin is on GitHub
	ReadOnly  bool         `json:"readOnly,omitempty"` // a pinned or read-only worktree, with nothing to ship
	Files     []ShipFile   `json:"files,omitempty"`    // uncommitted
	Behind    int          `json:"behind,omitempty"`   // commits on the base it lacks
	Conflicts []string     `json:"conflicts,omitempty"`
	Commits   int          `json:"commits,omitempty"` // on the branch since its base
	Unpushed  []ShipCommit `json:"unpushed,omitempty"`
	Pushed    bool         `json:"pushed,omitempty"`
	Synced    time.Time    `json:"synced,omitzero"` // when it was last rebased onto its base from here
	PR        *gh.PR       `json:"pr,omitempty"`
	Checks    []gh.Check   `json:"checks,omitempty"`
	FailLine  string       `json:"failLine,omitempty"` // what the first failing check's log says
	// CommitDraft is the commit message drafted for it (Body), PRDraft the pull request's.
	CommitDraft  Draft    `json:"commitDraft,omitzero"`
	PRDraft      Draft    `json:"prDraft,omitzero"`
	Template     string   `json:"template,omitempty"` // the repo's pull request template
	TemplateFile string   `json:"templateFile,omitempty"`
	Owners       []string `json:"owners,omitempty"` // CODEOWNERS of what the branch changes
	GitError     string   `json:"gitError,omitempty"`
	GitHubError  string   `json:"githubError,omitempty"`
}

// ShipFile is an uncommitted file.
type ShipFile struct {
	Path   string `json:"path"`
	Code   string `json:"code"` // git's letter: M, A, D, R, ? (untracked), U (conflicted)
	Add    int    `json:"add,omitempty"`
	Del    int    `json:"del,omitempty"`
	Binary bool   `json:"binary,omitempty"`
}

// ShipCommit is a commit on the branch.
type ShipCommit struct {
	Hash    string `json:"hash"`
	Subject string `json:"subject"`
	Author  string `json:"author,omitempty"`
}

// Draft is a commit message or PR drafted for a project: by the agent (its provider's name) or "you".
type Draft struct {
	Title string    `json:"title,omitempty"`
	Body  string    `json:"body,omitempty"`
	By    string    `json:"by,omitempty"`
	Saved time.Time `json:"saved,omitzero"`
}

// Empty says nothing was drafted.
func (d Draft) Empty() bool { return strings.TrimSpace(d.Title+d.Body) == "" }

// What the app asks of the Ship page's side.
const (
	MethodShipRead   = "ship.read"
	MethodShipCommit = "ship.commit"
	MethodShipPush   = "ship.push"
	MethodShipSync   = "ship.sync"
	MethodShipPR     = "ship.pr"
	MethodShipReady  = "ship.ready"
	MethodShipDraft  = "ship.draft"
)

// ShipReq reads a session's projects; Fresh asks GitHub again rather than answering from the last minute's.
type ShipReq struct {
	Session core.SessionID `json:"session"`
	Fresh   bool           `json:"fresh,omitempty"`
}

// ShipRef is one project of a session.
type ShipRef struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
}

// ShipCommitReq commits files of a project (every change when none) with a message.
type ShipCommitReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Message string         `json:"message"`
	Files   []string       `json:"files,omitempty"`
}

// PRReq raises a project's pull request, or edits the open one: its title and body, as a draft, the reviewers asked.
type PRReq struct {
	Session   core.SessionID `json:"session"`
	Project   string         `json:"project"`
	Title     string         `json:"title"`
	Body      string         `json:"body"`
	Draft     bool           `json:"draft,omitempty"`
	Reviewers []string       `json:"reviewers,omitempty"`
}

// DraftReq saves a project's commit message (Kind "commit": Body) or PR draft (Kind "pr") as the user wrote it.
type DraftReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title,omitempty"`
	Body    string         `json:"body"`
}

// shipTTL is how long a project's GitHub reading answers before it's asked again.
const shipTTL = 30 * time.Second

// shipState is what the worker keeps between reads: GitHub's answers, and when each project was last synced.
type shipState struct {
	mu     sync.Mutex
	github map[string]githubRead // by session/project/branch
	synced map[string]time.Time  // by session/project
}

type githubRead struct {
	at       time.Time
	pr       *gh.PR
	checks   []gh.Check
	failLine string
	err      string
}

func shipKey(id core.SessionID, project string) string { return string(id) + "/" + project }

// projectRepo is a session's project and its git, guarded in a sandboxed workspace.
func (w *Worker) projectRepo(id core.SessionID, project string) (core.SessionState, core.Project, gitx.Repo, error) {
	se, err := w.session(id)
	if err != nil {
		return se, core.Project{}, gitx.Repo{}, err
	}
	i := slices.IndexFunc(se.Projects, func(p core.Project) bool { return p.Name == project })
	if i < 0 {
		return se, core.Project{}, gitx.Repo{}, fmt.Errorf("session %s has no project %q", id, project)
	}
	p := se.Projects[i]
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		return se, p, gitx.Repo{}, err
	}
	return se, p, gitx.Repo{Dir: p.Dir, Env: env}, nil
}

// github is the GitHub client for a checkout.
func (w *Worker) github(r gitx.Repo) gh.Client {
	if w.opts.GitHub != nil {
		return w.opts.GitHub(r.Dir, r.Env)
	}
	return gh.API{T: gh.CLI{Dir: r.Dir, Env: r.Env}}
}

// ShipRead is every project of a session on its way to a PR.
func (w *Worker) ShipRead(ctx context.Context, r ShipReq) ([]Ship, error) {
	se, err := w.session(r.Session)
	if err != nil {
		return nil, err
	}
	out := make([]Ship, len(se.Projects))
	var wg sync.WaitGroup
	for i, p := range se.Projects {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = w.shipProject(ctx, se, p, r.Fresh)
		}()
	}
	wg.Wait()
	return out, nil
}

func (w *Worker) shipProject(ctx context.Context, se core.SessionState, p core.Project, fresh bool) Ship {
	s := Ship{Project: p.Name, Branch: p.Branch, Base: strings.TrimPrefix(p.Base, "origin/"), ReadOnly: p.ReadOnly || p.Detached}
	s.CommitDraft = w.readDraft(se.ID, p.Name, "commit")
	s.PRDraft = w.readDraft(se.ID, p.Name, "pr")
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		s.GitError = err.Error()
		return s
	}
	repo := gitx.Repo{Dir: p.Dir, Env: env}
	remote := repo.RemoteURL(ctx)
	s.Remote = remote != ""
	ghRepo, onGitHub := gh.ParseRepo(remote)
	if onGitHub {
		s.GitHub = ghRepo.String()
	}
	if s.ReadOnly {
		return s
	}
	s.Template, s.TemplateFile = gh.Template(p.Dir)
	if fresh && s.Remote {
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
	if s.Base == "" {
		s.Base = strings.TrimPrefix(f.Base, "origin/")
	}
	for _, fs := range f.Uncommitted {
		s.Files = append(s.Files, ShipFile{Path: fs.Path, Code: string(fs.Code), Add: fs.Add, Del: fs.Del, Binary: fs.Binary})
	}
	s.Behind, s.Conflicts, s.Commits, s.Pushed = f.Behind, f.Conflicts, len(f.Commits), f.Pushed
	for _, c := range f.Unpushed {
		s.Unpushed = append(s.Unpushed, ShipCommit{Hash: c.Hash, Subject: c.Subject, Author: c.Author})
	}
	if f.Base != "" {
		if out, err := repo.Run(ctx, "diff", "--name-only", f.Base); err == nil {
			s.Owners = gh.Owners(p.Dir, strings.Fields(out))
		}
	}
	st := w.shipMu()
	st.mu.Lock()
	s.Synced = st.synced[shipKey(se.ID, p.Name)]
	st.mu.Unlock()
	if onGitHub && s.Branch != "" {
		g := w.githubFor(ctx, se.ID, p.Name, repo, ghRepo, s.Branch, fresh)
		s.PR, s.Checks, s.FailLine, s.GitHubError = g.pr, g.checks, g.failLine, g.err
	}
	return s
}

// shipMu is the worker's ship state, made on first use.
func (w *Worker) shipMu() *shipState {
	w.shipOnce.Do(func() { w.ship = &shipState{github: map[string]githubRead{}, synced: map[string]time.Time{}} })
	return w.ship
}

// githubFor is a branch's PR and checks: the last minute's reading unless fresh.
func (w *Worker) githubFor(ctx context.Context, id core.SessionID, project string, repo gitx.Repo, ghRepo gh.Repo, branch string, fresh bool) githubRead {
	st := w.shipMu()
	key := shipKey(id, project) + "/" + branch
	st.mu.Lock()
	g, ok := st.github[key]
	st.mu.Unlock()
	if ok && !fresh && w.opts.Now().Sub(g.at) < shipTTL {
		return g
	}
	g = githubRead{at: w.opts.Now()}
	c := w.github(repo)
	pr, err := c.PRForBranch(ctx, ghRepo, branch)
	if err != nil {
		g.err = err.Error()
	} else {
		g.pr = pr
		if pr != nil {
			if g.checks, err = c.Checks(ctx, ghRepo, branch); err != nil {
				g.err = err.Error()
			}
			for _, ch := range g.checks {
				if ch.State() == gh.CheckFail && ch.Actions {
					if log, err := c.Log(ctx, ghRepo, ch); err == nil {
						g.failLine = gh.FailLine(log)
					}
					break
				}
			}
		}
	}
	st.mu.Lock()
	st.github[key] = g
	st.mu.Unlock()
	return g
}

// forget drops a project's GitHub reading, so the next read asks again.
func (w *Worker) forget(id core.SessionID, project string) {
	st := w.shipMu()
	st.mu.Lock()
	defer st.mu.Unlock()
	for k := range st.github {
		if strings.HasPrefix(k, shipKey(id, project)+"/") {
			delete(st.github, k)
		}
	}
}

// ShipCommit commits a project's files with the user's git identity, and drops the commit draft it used.
func (w *Worker) ShipCommit(ctx context.Context, r ShipCommitReq) error {
	_, p, repo, err := w.projectRepo(r.Session, r.Project)
	if err != nil {
		return err
	}
	if p.ReadOnly || p.Detached {
		return fmt.Errorf("%s is read-only here", p.Name)
	}
	if err := repo.Commit(ctx, r.Message, r.Files...); err != nil {
		return err
	}
	w.dropDraft(r.Session, p.Name, "commit")
	return nil
}

// ShipPush pushes a project's branch to origin: the session's own branch only.
func (w *Worker) ShipPush(ctx context.Context, r ShipRef) error {
	_, p, repo, err := w.projectRepo(r.Session, r.Project)
	if err != nil {
		return err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return err
	}
	defer w.forget(r.Session, p.Name)
	return repo.Push(ctx, branch)
}

// ShipSync fetches origin and rebases a project's branch onto its base, carrying uncommitted work over. One that
// conflicts is undone and says where.
func (w *Worker) ShipSync(ctx context.Context, r ShipRef) error {
	_, p, repo, err := w.projectRepo(r.Session, r.Project)
	if err != nil {
		return err
	}
	if _, err := ownBranch(p); err != nil {
		return err
	}
	if err := repo.Fetch(ctx); err != nil {
		return err
	}
	base := repo.FlightBase(ctx, p.Base)
	if base == "" {
		return fmt.Errorf("%s has no base branch to rebase onto", p.Name)
	}
	if err := repo.Rebase(ctx, base); err != nil {
		return err
	}
	st := w.shipMu()
	st.mu.Lock()
	st.synced[shipKey(r.Session, p.Name)] = w.opts.Now()
	st.mu.Unlock()
	return nil
}

// ShipPR raises a project's pull request from its pushed branch, or edits the open one, asks its reviewers, and
// cross-links every PR of the session in their merge order (the session's project order).
func (w *Worker) ShipPR(ctx context.Context, r PRReq) (*gh.PR, error) {
	se, p, repo, err := w.projectRepo(r.Session, r.Project)
	if err != nil {
		return nil, err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return nil, err
	}
	ghRepo, ok := gh.ParseRepo(repo.RemoteURL(ctx))
	if !ok {
		return nil, fmt.Errorf("%s's origin isn't on GitHub", p.Name)
	}
	if strings.TrimSpace(r.Title) == "" {
		return nil, errors.New("a pull request needs a title")
	}
	c := w.github(repo)
	defer w.forget(r.Session, p.Name)
	pr, err := c.PRForBranch(ctx, ghRepo, branch)
	if err != nil {
		return nil, err
	}
	if pr.Open() {
		if pr, err = c.EditPR(ctx, ghRepo, pr.Number, r.Title, r.Body); err != nil {
			return nil, err
		}
	} else {
		base := strings.TrimPrefix(repo.FlightBase(ctx, p.Base), "origin/")
		if base == "" {
			return nil, fmt.Errorf("%s has no base branch for the PR", p.Name)
		}
		if pr, err = c.CreatePR(ctx, ghRepo, gh.NewPR{Title: r.Title, Body: r.Body, Head: branch, Base: base, Draft: r.Draft}); err != nil {
			return nil, err
		}
	}
	if len(r.Reviewers) > 0 {
		if err := c.RequestReviewers(ctx, ghRepo, pr.Number, r.Reviewers); err != nil {
			return pr, err
		}
	}
	return pr, w.crossLink(ctx, se)
}

// ShipReady takes a project's draft PR out of draft.
func (w *Worker) ShipReady(ctx context.Context, r ShipRef) error {
	_, p, repo, err := w.projectRepo(r.Session, r.Project)
	if err != nil {
		return err
	}
	ghRepo, ok := gh.ParseRepo(repo.RemoteURL(ctx))
	if !ok {
		return fmt.Errorf("%s's origin isn't on GitHub", p.Name)
	}
	c := w.github(repo)
	defer w.forget(r.Session, p.Name)
	pr, err := c.PRForBranch(ctx, ghRepo, p.Branch)
	if err != nil {
		return err
	}
	if !pr.Open() {
		return fmt.Errorf("%s has no open pull request", p.Name)
	}
	return c.MarkReady(ctx, ghRepo, pr)
}

// Cross-linked PRs carry a block naming each other, between these markers, kept up to date as each is raised.
const (
	linkStart = "<!-- oriel:linked-prs -->"
	linkEnd   = "<!-- /oriel:linked-prs -->"
)

// crossLink writes the block into every open PR of a session with more than one, in the projects' order.
func (w *Worker) crossLink(ctx context.Context, se core.SessionState) error {
	type linked struct {
		c    gh.Client
		repo gh.Repo
		pr   *gh.PR
	}
	var prs []linked
	for _, p := range se.Projects {
		if p.ReadOnly || p.Detached || p.Branch == "" {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		ghRepo, ok := gh.ParseRepo(repo.RemoteURL(ctx))
		if !ok {
			continue
		}
		c := w.github(repo)
		pr, err := c.PRForBranch(ctx, ghRepo, p.Branch)
		if err != nil {
			return err
		}
		if pr.Open() {
			prs = append(prs, linked{c, ghRepo, pr})
		}
	}
	if len(prs) < 2 {
		return nil
	}
	for i, l := range prs {
		var b strings.Builder
		b.WriteString(linkStart + "\n**Ships with** (merge in this order):\n")
		for j, o := range prs {
			fmt.Fprintf(&b, "%d. %s#%d", j+1, o.repo, o.pr.Number)
			if j == i {
				b.WriteString(" (this PR)")
			}
			b.WriteString("\n")
		}
		b.WriteString(linkEnd)
		body := WithLinks(l.pr.Body, b.String())
		if body == l.pr.Body {
			continue
		}
		if _, err := l.c.EditPR(ctx, l.repo, l.pr.Number, l.pr.Title, body); err != nil {
			return err
		}
	}
	return nil
}

// WithLinks is body with its cross-linked PRs block replaced by block, or block added at its end.
func WithLinks(body, block string) string {
	if i := strings.Index(body, linkStart); i >= 0 {
		if j := strings.Index(body[i:], linkEnd); j >= 0 {
			return body[:i] + block + body[i+j+len(linkEnd):]
		}
	}
	if strings.TrimSpace(body) == "" {
		return block
	}
	return strings.TrimRight(body, "\n") + "\n\n" + block
}

// draftPath is where a project's commit message or PR draft is kept: "# title\n\nbody" for a PR.
func (w *Worker) draftPath(id core.SessionID, project, kind string) string {
	return filepath.Join(w.sessionDir(id), "drafts", Sanitise(project)+"."+kind+".md")
}

// readDraft is a project's draft and who wrote it.
func (w *Worker) readDraft(id core.SessionID, project, kind string) Draft {
	path := w.draftPath(id, project, kind)
	data, err := os.ReadFile(path)
	if err != nil {
		return Draft{}
	}
	d := Draft{Body: strings.TrimSpace(string(data))}
	if fi, err := os.Stat(path); err == nil {
		d.Saved = fi.ModTime()
	}
	if kind == "pr" {
		first, rest, _ := strings.Cut(string(data), "\n")
		d.Title, d.Body = strings.TrimSpace(strings.TrimPrefix(first, "#")), strings.TrimSpace(rest)
	}
	if by, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".by"); err == nil {
		d.By = strings.TrimSpace(string(by))
	}
	return d
}

// writeDraft keeps a project's draft, as by wrote it.
func (w *Worker) writeDraft(id core.SessionID, project, kind, title, body, by string) error {
	path := w.draftPath(id, project, kind)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	text := body
	if kind == "pr" {
		text = "# " + title + "\n\n" + body
	}
	if strings.TrimSpace(title+body) == "" {
		return errors.New("the draft is empty")
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(path, ".md")+".by", []byte(by), 0o600)
}

func (w *Worker) dropDraft(id core.SessionID, project, kind string) {
	path := w.draftPath(id, project, kind)
	_ = os.Remove(path)
	_ = os.Remove(strings.TrimSuffix(path, ".md") + ".by")
}

// ShipDraft saves a draft the user wrote.
func (w *Worker) ShipDraft(r DraftReq) error {
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Name == r.Project }) {
		return fmt.Errorf("session %s has no project %q", r.Session, r.Project)
	}
	if r.Kind != "commit" && r.Kind != "pr" {
		return fmt.Errorf("a draft is a commit or a pr, not %q", r.Kind)
	}
	return w.writeDraft(r.Session, r.Project, r.Kind, r.Title, r.Body, "you")
}

// ownBranch refuses a push, pull or PR unless the worktree is on the session's branch for it: the sandbox can
// switch branches, and "push this workspace" must not push anything else.
func ownBranch(p core.Project) (string, error) {
	if p.Branch == "" || p.Detached || p.ReadOnly {
		return "", fmt.Errorf("%s has no branch of this session's to push", p.Name)
	}
	out, err := sandbox.Git(p.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if got := strings.TrimSpace(out); got != p.Branch {
		return "", fmt.Errorf("%s is on %q, not this session's branch %q: Oriel only pushes and raises PRs from %q. Switch back first",
			p.Name, got, p.Branch, p.Branch)
	}
	return p.Branch, nil
}

func (w *Worker) serveShip(fn func(string, wire.Handler)) {
	fn(MethodShipRead, wire.Func(func(ctx context.Context, r ShipReq) ([]Ship, error) { return w.ShipRead(ctx, r) }))
	fn(MethodShipCommit, wire.Func(func(ctx context.Context, r ShipCommitReq) (any, error) { return nil, w.ShipCommit(ctx, r) }))
	fn(MethodShipPush, wire.Func(func(ctx context.Context, r ShipRef) (any, error) { return nil, w.ShipPush(ctx, r) }))
	fn(MethodShipSync, wire.Func(func(ctx context.Context, r ShipRef) (any, error) { return nil, w.ShipSync(ctx, r) }))
	fn(MethodShipPR, wire.Func(func(ctx context.Context, r PRReq) (*gh.PR, error) { return w.ShipPR(ctx, r) }))
	fn(MethodShipReady, wire.Func(func(ctx context.Context, r ShipRef) (any, error) { return nil, w.ShipReady(ctx, r) }))
	fn(MethodShipDraft, wire.Func(func(_ context.Context, r DraftReq) (any, error) { return nil, w.ShipDraft(r) }))
}

// ShipRead is a session's projects on their way to a PR (ship.read).
func (c Client) ShipRead(ctx context.Context, r ShipReq) ([]Ship, error) {
	var out []Ship
	return out, c.Conn.Call(ctx, MethodShipRead, r, &out)
}

func (c Client) ShipCommit(ctx context.Context, r ShipCommitReq) error {
	return c.Conn.Call(ctx, MethodShipCommit, r, nil)
}

func (c Client) ShipPush(ctx context.Context, r ShipRef) error {
	return c.Conn.Call(ctx, MethodShipPush, r, nil)
}

func (c Client) ShipSync(ctx context.Context, r ShipRef) error {
	return c.Conn.Call(ctx, MethodShipSync, r, nil)
}

// ShipPR raises a project's PR, or edits its open one, and returns it.
func (c Client) ShipPR(ctx context.Context, r PRReq) (*gh.PR, error) {
	var out *gh.PR
	return out, c.Conn.Call(ctx, MethodShipPR, r, &out)
}

func (c Client) ShipReady(ctx context.Context, r ShipRef) error {
	return c.Conn.Call(ctx, MethodShipReady, r, nil)
}

func (c Client) ShipDraft(ctx context.Context, r DraftReq) error {
	return c.Conn.Call(ctx, MethodShipDraft, r, nil)
}
