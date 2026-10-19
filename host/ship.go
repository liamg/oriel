package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
)

// The Ship page's side on the host (ui.md 18): each project's way from uncommitted work to a pushed branch, read from
// its worktree, and the git steps along it the user takes from the app. They run here, where the checkout and the
// user's credentials are, with the sandbox's git guard in a sandboxed workspace. The pull request is the app's, through
// its code host (registry.CodeHost).

// Ship is a project on its way to a merged pull request.
type Ship struct {
	Project   string       `json:"project"`
	Branch    string       `json:"branch,omitempty"`
	Base      string       `json:"base,omitempty"`     // the branch it merges into: main
	Remote    bool         `json:"remote,omitempty"`   // it has an origin to push to
	Origin    string       `json:"origin,omitempty"`   // origin's URL
	ReadOnly  bool         `json:"readOnly,omitempty"` // a pinned or read-only worktree, with nothing to ship
	Files     []ShipFile   `json:"files,omitempty"`    // uncommitted
	Behind    int          `json:"behind,omitempty"`   // commits on the base it lacks
	Conflicts []string     `json:"conflicts,omitempty"`
	Commits   int          `json:"commits,omitempty"` // on the branch since its base
	Unpushed  []ShipCommit `json:"unpushed,omitempty"`
	Pushed    bool         `json:"pushed,omitempty"`
	Synced    time.Time    `json:"synced,omitzero"` // when it was last rebased onto its base from here
	// CommitDraft is the commit message drafted for it (Body), PRDraft the pull request's.
	CommitDraft  Draft    `json:"commitDraft,omitzero"`
	PRDraft      Draft    `json:"prDraft,omitzero"`
	Template     string   `json:"template,omitempty"` // the repo's pull request template
	TemplateFile string   `json:"templateFile,omitempty"`
	Owners       []string `json:"owners,omitempty"` // CODEOWNERS of what the branch changes
	GitError     string   `json:"gitError,omitempty"`
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

// Draft is a commit message or PR drafted for a project: by the agent (its provider's name) or "you". Stale says
// the code has moved on since it was written.
type Draft struct {
	Title string    `json:"title,omitempty"`
	Body  string    `json:"body,omitempty"`
	By    string    `json:"by,omitempty"`
	Saved time.Time `json:"saved,omitzero"`
	Stale bool      `json:"stale,omitempty"`
	code  string    // the code's fingerprint when it was written (draftCode)
}

// DraftByYou marks a draft the user wrote: the agent never overwrites it.
const DraftByYou = "you"

// Empty says nothing was drafted.
func (d Draft) Empty() bool { return strings.TrimSpace(d.Title+d.Body) == "" }

// ShipReq reads a session's projects; Fresh fetches first.
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
	Session  core.SessionID `json:"session"`
	Project  string         `json:"project"`
	Message  string         `json:"message"`
	Files    []string       `json:"files,omitempty"`
	Trailers []string       `json:"trailers,omitempty"` // a commit gate's, when committed past it
}

// DraftReq saves a project's commit message (Kind "commit": Body) or PR draft (Kind "pr") as the user wrote it, or
// with Drop clears it, so the agent drafts it again.
type DraftReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Kind    string         `json:"kind"`
	Title   string         `json:"title,omitempty"`
	Body    string         `json:"body"`
	Drop    bool           `json:"drop,omitempty"`
}

// shipState is what the host keeps between reads: each branch's PR on GitHub, for review mirroring, and when each
// project was last synced.
type shipState struct {
	mu     sync.Mutex
	github map[string]githubRead // by session/project/branch
	synced map[string]time.Time  // by session/project
}

type githubRead struct {
	at  time.Time
	pr  *gh.PR
	err string
}

// githubTTL is how long a branch's PR answers before GitHub is asked again.
const githubTTL = 30 * time.Second

func shipKey(id core.SessionID, project string) string { return string(id) + "/" + project }

// projectRepo is a session's project and its git, guarded in a sandboxed workspace.
func (w *Host) projectRepo(ctx context.Context, id core.SessionID, project string) (core.SessionState, core.Project, gitx.Repo, error) {
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
	repo := gitx.Repo{Dir: p.Dir, Env: env}
	if se.Kind == core.KindDirect {
		p = directBranch(ctx, p, repo)
	}
	return se, p, repo, nil
}

// directBranch is a direct session's project on the branch its checkout is on now: it's the user's own, and they
// switch it. A detached HEAD has none.
func directBranch(ctx context.Context, p core.Project, repo gitx.Repo) core.Project {
	p.Branch = ""
	if b, err := repo.Branch(ctx); err == nil && b != "HEAD" {
		p.Branch = b
	}
	return p
}

// github is the GitHub client for a checkout.
func (w *Host) github(r gitx.Repo) gh.Client { return w.githubOn(r, "", "") }

// githubOn is the GitHub client for a checkout, on a GitHub Enterprise server's hostname when given: the user's gh,
// with token as its GH_TOKEN when one was found; without gh, GitHub's API with the token.
func (w *Host) githubOn(r gitx.Repo, hostname, token string) gh.Client {
	if w.opts.GitHub != nil {
		return w.opts.GitHub(r.Dir, r.Env)
	}
	enterprise := hostname != "" && !strings.EqualFold(hostname, "github.com")
	if _, err := exec.LookPath("gh"); err == nil {
		env := r.Env
		if token != "" {
			env = append(slices.Clone(env), map[bool]string{false: "GH_TOKEN=", true: "GH_ENTERPRISE_TOKEN="}[enterprise]+token)
		}
		return gh.API{T: gh.CLI{Dir: r.Dir, Env: env, Host: hostname}}
	}
	base := "https://api.github.com"
	if enterprise {
		base = "https://" + hostname + "/api/v3"
	}
	if token == "" {
		return gh.API{T: noToken{}}
	}
	return gh.API{T: gh.HTTP{Base: base, Token: token}}
}

// noToken is GitHub with neither gh nor a token: every call is refused, as GitHub would refuse it.
type noToken struct{}

func (noToken) Do(context.Context, string, string, any, any) error {
	return &gh.Error{Status: 401, Message: "gh isn't installed and no GitHub token is set"}
}

// ShipRead is every project of a session on its way to a PR.
func (w *Host) ShipRead(ctx context.Context, r ShipReq) (_ []Ship, err error) {
	defer w.recover("ShipRead", &err)
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
			defer w.recover("ShipRead", nil)
			out[i] = w.shipProject(ctx, se, p, r.Fresh)
		}()
	}
	wg.Wait()
	return out, nil
}

func (w *Host) shipProject(ctx context.Context, se core.SessionState, p core.Project, fresh bool) Ship {
	s := Ship{Project: p.Name, Branch: p.Branch, Base: strings.TrimPrefix(p.Base, "origin/"), ReadOnly: p.ReadOnly || p.Detached}
	s.CommitDraft = w.readDraft(se.ID, p.Name, "commit")
	s.PRDraft = w.readDraft(se.ID, p.Name, "pr")
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		s.GitError = err.Error()
		return s
	}
	repo := gitx.Repo{Dir: p.Dir, Env: env}
	if se.Kind == core.KindDirect {
		s.Branch = directBranch(ctx, p, repo).Branch
	}
	s.Origin = repo.RemoteURL(ctx)
	s.Remote = s.Origin != ""
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
	for kind, d := range map[string]*Draft{"commit": &s.CommitDraft, "pr": &s.PRDraft} {
		if d.code != "" {
			d.Stale = d.code != draftCode(ctx, repo, f.Base, kind)
		}
	}
	st := w.shipMu()
	st.mu.Lock()
	s.Synced = st.synced[shipKey(se.ID, p.Name)]
	st.mu.Unlock()
	return s
}

// shipMu is the host's ship state, made on first use.
func (w *Host) shipMu() *shipState {
	w.shipOnce.Do(func() { w.ship = &shipState{github: map[string]githubRead{}, synced: map[string]time.Time{}} })
	return w.ship
}

// ShipCommit commits a project's files with the user's git identity, drops the commit draft it used, and returns the
// commit's hash.
func (w *Host) ShipCommit(ctx context.Context, r ShipCommitReq) (_ string, err error) {
	defer w.recover("ShipCommit", &err)
	_, p, repo, err := w.projectRepo(ctx, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	if p.ReadOnly || p.Detached {
		return "", fmt.Errorf("%s is read-only here", p.Name)
	}
	var hash string
	err = w.keyed(ctx, r.Session, "git commit", p.Name, func(ctx context.Context) (err error) {
		hash, err = repo.CommitWith(ctx, r.Message, r.Trailers, r.Files...)
		return err
	})
	if err != nil {
		return "", err
	}
	w.dropDraft(r.Session, p.Name, "commit")
	return hash, nil
}

// ShipPush pushes a project's branch to origin: the session's own branch only.
func (w *Host) ShipPush(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipPush", &err)
	_, p, repo, err := w.projectRepo(ctx, r.Session, r.Project)
	if err != nil {
		return err
	}
	branch, err := ownBranch(p)
	if err != nil {
		return err
	}
	return w.keyed(ctx, r.Session, "git push", p.Name, func(ctx context.Context) error { return repo.Push(ctx, branch) })
}

// ShipSync fetches origin and rebases a project's branch onto its base, carrying uncommitted work over. One that
// conflicts is undone and says where.
func (w *Host) ShipSync(ctx context.Context, r ShipRef) (err error) {
	defer w.recover("ShipSync", &err)
	_, p, repo, err := w.projectRepo(ctx, r.Session, r.Project)
	if err != nil {
		return err
	}
	if _, err := ownBranch(p); err != nil {
		return err
	}
	if err := w.keyed(ctx, r.Session, "git fetch origin", p.Name, repo.Fetch); err != nil {
		return err
	}
	base := repo.FlightBase(ctx, p.Base)
	if base == "" {
		return fmt.Errorf("%s has no base branch to rebase onto", p.Name)
	}
	if err := w.keyed(ctx, r.Session, "git rebase "+base+" (signing commits)", p.Name, func(ctx context.Context) error { return repo.Rebase(ctx, base) }); err != nil {
		return err
	}
	st := w.shipMu()
	st.mu.Lock()
	st.synced[shipKey(r.Session, p.Name)] = w.opts.Now()
	st.mu.Unlock()
	return nil
}

// RenameReq renames a project's branch.
type RenameReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Name    string         `json:"name"`
}

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (w *Host) ShipRename(ctx context.Context, r RenameReq) (err error) {
	defer w.recover("ShipRename", &err)
	se, p, repo, err := w.projectRepo(ctx, r.Session, r.Project)
	if err != nil {
		return err
	}
	name := strings.TrimSpace(r.Name)
	old, err := ownBranch(p)
	if err != nil {
		return err
	}
	if name == old {
		return nil
	}
	if err := w.keyed(ctx, r.Session, "git branch -m "+old+" "+name, p.Name, func(ctx context.Context) error {
		return repo.RenameBranch(ctx, old, name)
	}); err != nil {
		return err
	}
	if se.Kind == core.KindDirect {
		return nil
	}
	projects := slices.Clone(se.Projects)
	for i := range projects {
		if projects[i].Name == p.Name {
			projects[i].Branch = name
		}
	}
	if m, ok := LoadManifest(se.Dir); ok {
		for i := range m.Projects {
			if m.Projects[i].Name == p.Name {
				m.Projects[i].Branch = name
			}
		}
		if err := saveManifest(se.Dir, m); err != nil {
			return err
		}
	}
	branch := se.Branch
	if branch == old && !slices.ContainsFunc(projects, func(q core.Project) bool { return q.Branch == old }) {
		branch = name
	}
	return w.emit(core.ProjectsChanged{ID: se.ID, Branch: branch, Projects: projects, Past: se.Past, Started: se.Started})
}

// draftPath is where a project's commit message or PR draft is kept: "# title\n\nbody" for a PR.
func (w *Host) draftPath(id core.SessionID, project, kind string) string {
	return filepath.Join(w.sessionDir(id), "drafts", Sanitise(project)+"."+kind+".md")
}

// readDraft is a project's draft and who wrote it.
func (w *Host) readDraft(id core.SessionID, project, kind string) Draft {
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
	if code, err := os.ReadFile(strings.TrimSuffix(path, ".md") + ".code"); err == nil {
		d.code = strings.TrimSpace(string(code))
	}
	return d
}

// writeDraft keeps a project's draft, as by wrote it against the code fingerprinted code.
func (w *Host) writeDraft(id core.SessionID, project, kind, title, body, by, code string) error {
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
	if err := os.WriteFile(strings.TrimSuffix(path, ".md")+".code", []byte(code), 0o600); err != nil {
		return err
	}
	return os.WriteFile(strings.TrimSuffix(path, ".md")+".by", []byte(by), 0o600)
}

func (w *Host) dropDraft(id core.SessionID, project, kind string) {
	path := w.draftPath(id, project, kind)
	_ = os.Remove(path)
	_ = os.Remove(strings.TrimSuffix(path, ".md") + ".by")
	_ = os.Remove(strings.TrimSuffix(path, ".md") + ".code")
}

// ShipDraft saves a draft the user wrote, or drops it.
func (w *Host) ShipDraft(ctx context.Context, r DraftReq) (err error) {
	defer w.recover("ShipDraft", &err)
	_, p, repo, err := w.projectRepo(ctx, r.Session, r.Project)
	if err != nil {
		return err
	}
	if r.Kind != "commit" && r.Kind != "pr" {
		return fmt.Errorf("a draft is a commit or a pr, not %q", r.Kind)
	}
	if r.Drop {
		w.dropDraft(r.Session, p.Name, r.Kind)
		return nil
	}
	code := draftCode(ctx, repo, repo.FlightBase(ctx, p.Base), r.Kind)
	return w.writeDraft(r.Session, p.Name, r.Kind, r.Title, r.Body, DraftByYou, code)
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
