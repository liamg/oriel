package github

import (
	"cmp"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
	"github.com/liamg/oriel/sandbox"
)

// The Ship page's side on the host: each project's way from uncommitted work to a pushed branch, read from
// its worktree, and the git steps along it the user takes from the app. They run here, where the checkout and the
// user's credentials are, with the sandbox's git guard in a sandboxed workspace. The pull request is the app's, through
// its code host (registry.CodeHost).

// Ship is a project on its way to a merged pull request.
type Ship struct {
	Project   string       `json:"project"`
	Branch    string       `json:"branch,omitempty"`
	Base      string       `json:"base,omitempty"`      // the branch it merges into: main
	Default   string       `json:"default,omitempty"`   // the repo's default branch, when it has it locally: main
	Remote    bool         `json:"remote,omitempty"`    // it has an origin to push to
	Origin    string       `json:"origin,omitempty"`    // origin's URL
	ReadOnly  bool         `json:"readOnly,omitempty"`  // a pinned or read-only worktree, with nothing to ship
	Files     []ShipFile   `json:"files,omitempty"`     // uncommitted
	Behind    int          `json:"behind,omitempty"`    // commits on the base it lacks
	BaseAhead int          `json:"baseAhead,omitempty"` // commits it has from the local base that origin's lacks: a PR would show them again
	Conflicts []string     `json:"conflicts,omitempty"`
	Commits   int          `json:"commits,omitempty"` // on the branch since its base
	Unpushed  []ShipCommit `json:"unpushed,omitempty"`
	First     ShipCommit   `json:"first,omitzero"` // the branch's own first commit, pushed or not
	Pushed    bool         `json:"pushed,omitempty"`
	Head      string       `json:"head,omitempty"`     // the commit checked out
	Replaced  int          `json:"replaced,omitempty"` // commits on origin's branch a rebase rewrote: the next push replaces them
	Synced    time.Time    `json:"synced,omitzero"`    // when it was last rebased onto its base from here
	// CommitDraft is the commit message drafted for it (Body), PRDraft the pull request's.
	CommitDraft  hostcore.Draft `json:"commitDraft,omitzero"`
	PRDraft      hostcore.Draft `json:"prDraft,omitzero"`
	Template     string         `json:"template,omitempty"` // the repo's pull request template
	TemplateFile string         `json:"templateFile,omitempty"`
	Owners       []string       `json:"owners,omitempty"` // CODEOWNERS of what the branch changes
	GitError     string         `json:"gitError,omitempty"`
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
	Body    string `json:"body,omitempty"`
	Author  string `json:"author,omitempty"`
}

func shipCommit(c gitx.Commit) ShipCommit {
	return ShipCommit{Hash: c.Hash, Subject: c.Subject, Body: c.Body, Author: c.Author}
}

// ShipReq reads a session's projects; Fresh fetches first.
type ShipReq struct {
	Session  core.SessionID `json:"session"`
	Fresh    bool           `json:"fresh,omitempty"`
	Projects []string       `json:"projects,omitempty"` // only these projects, in the session's order; every one when empty
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
	Trailers []string       `json:"trailers,omitempty"` // added to the message, as git commit --trailer does
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

// github is the GitHub client for a checkout.
func (x *GitHub) github(r gitx.Repo) gh.Client { return x.githubOn(r, "", "") }

// githubOn is the GitHub client for a checkout, on a GitHub Enterprise server's hostname when given: GitHub's API with
// token, else the user's own (gh.TokenFor). gh needn't be installed.
func (x *GitHub) githubOn(r gitx.Repo, hostname, token string) gh.Client {
	if c := x.deps.Client(); c != nil {
		return c(r.Dir, r.Env)
	}
	host := strings.ToLower(cmp.Or(hostname, "github.com"))
	base := "https://api.github.com"
	if host != "github.com" {
		base = "https://" + host + "/api/v3"
	}
	if token == "" {
		token = x.tokens.For(context.Background(), host)
	}
	if token == "" {
		return gh.API{T: noToken{host: host}}
	}
	var client *http.Client
	if x.deps.HTTP != nil {
		client = x.deps.HTTP()
	}
	return gh.API{T: gh.HTTP{Base: base, Token: token, Client: client}}
}

// noToken is GitHub with no token found: every call is refused, as GitHub would refuse it.
type noToken struct{ host string }

func (n noToken) Do(context.Context, string, string, any, any) error {
	return &gh.Error{Status: 401, Message: "no GitHub token found: set GITHUB_TOKEN, sign in with gh, or store a git credential for " + n.host}
}

// ShipRead is every project of a session on its way to a PR, or those r names.
func (x *GitHub) ShipRead(ctx context.Context, r ShipReq) (_ []Ship, err error) {
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

func (x *GitHub) shipProject(ctx context.Context, se core.SessionState, p core.Project, fresh bool) Ship {
	s := Ship{Project: p.Name, Branch: p.Branch, Base: strings.TrimPrefix(p.Base, "origin/"), ReadOnly: p.ReadOnly || p.Detached}
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
	s.Origin = repo.RemoteURL(ctx)
	s.Remote = s.Origin != ""
	if s.ReadOnly {
		return s
	}
	s.Default = localDefault(ctx, repo)
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
			s.Owners = gh.Owners(p.Dir, files)
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
func (x *GitHub) ShipCommit(ctx context.Context, r ShipCommitReq) (_ string, err error) {
	_, p, repo, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	if p.ReadOnly || p.Detached {
		return "", fmt.Errorf("%s is read-only here", p.Name)
	}
	var hash string
	err = x.deps.Keyed(ctx, r.Session, "git commit", p.Name, func(ctx context.Context) (err error) {
		hash, err = repo.CommitWith(ctx, r.Message, r.Trailers, r.Files...)
		return err
	})
	if err != nil {
		return "", err
	}
	x.env.DropDraft(r.Session, p.Name, "commit")
	return hash, nil
}

// ShipPush pushes a project's branch to origin: the session's own branch only.
func (x *GitHub) ShipPush(ctx context.Context, r ShipRef) (err error) {
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
func (x *GitHub) ShipSync(ctx context.Context, r ShipRef) (err error) {
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
func (x *GitHub) ShipMergeDefault(ctx context.Context, r ShipRef) (m gitx.Merge, err error) {
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

// RenameReq renames a project's branch.
type RenameReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Name    string         `json:"name"`
}

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (x *GitHub) ShipRename(ctx context.Context, r RenameReq) (err error) {
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
	projects := slices.Clone(se.Projects)
	for i := range projects {
		if projects[i].Name == p.Name {
			projects[i].Branch = name
		}
	}
	if err := x.deps.BranchRenamed(se.Dir, p.Name, name); err != nil {
		return err
	}
	branch := se.Branch
	if branch == old && !slices.ContainsFunc(projects, func(q core.Project) bool { return q.Branch == old }) {
		branch = name
	}
	return x.st.Emit(core.ProjectsChanged{ID: se.ID, Branch: branch, Projects: projects, Past: se.Past, Started: se.Started})
}

// ShipDraft saves a draft the user wrote, or drops it.
func (x *GitHub) ShipDraft(ctx context.Context, r DraftReq) (err error) {
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
	return x.env.WriteDraft(r.Session, p.Name, r.Kind, r.Title, r.Body, hostcore.DraftByYou, code)
}
