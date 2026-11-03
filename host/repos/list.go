// Package repos is the checkouts the session host knows (repos.json) and what the Repos page does to them: add,
// clone, discover, remove, read their facts, clean up their branches, tag them.
package repos

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// Registry is the checkouts the host knows (repos.json), and what the Repos page does to them.
type Registry struct {
	env  hostcore.Env
	st   hostcore.State
	list *repoList
	// defaultDir is where clones and new repos go when repos.dir isn't set.
	defaultDir func() string
}

// New reads the registry from env's home; defaultDir is where clones go when repos.dir isn't set.
func New(env hostcore.Env, st hostcore.State, defaultDir func() string) (*Registry, error) {
	x := &Registry{env: env, st: st, defaultDir: defaultDir,
		list: &repoList{path: filepath.Join(env.Home, "repos.json"), now: env.Now, scanned: map[string]bool{}}}
	return x, x.list.load()
}

// repoList is the checkouts the host knows, kept in repos.json, and the paths the user removed.
type repoList struct {
	path    string
	now     func() time.Time
	mu      sync.Mutex
	list    []core.Repo
	ignored []string
	scanned map[string]bool // parents discovery has read since the host started
	dir     string          // repos.dir, when set
}

// reposFile is repos.json.
type reposFile struct {
	Repos   []core.Repo `json:"repos"`
	Ignored []string    `json:"ignored,omitempty"`
}

func (r *repoList) load() error {
	b, err := os.ReadFile(r.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if b = bytes.TrimSpace(b); len(b) > 0 && b[0] == '[' {
		return json.Unmarshal(b, &r.list)
	}
	var f reposFile
	if err := json.Unmarshal(b, &f); err != nil {
		return err
	}
	r.list, r.ignored = f.Repos, f.Ignored
	return nil
}

func (r *repoList) all() []core.Repo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

func (r *repoList) save() error {
	b, err := json.MarshalIndent(reposFile{Repos: r.list, Ignored: r.ignored}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(b, '\n'), 0o600)
}

// add registers the checkout at path, or returns it if it's already registered; added says whether it's new. A
// path added by hand is no longer ignored.
func (r *repoList) add(ctx context.Context, path string, src core.RepoSource) (_ core.Repo, added bool, _ error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return core.Repo{}, false, err
	}
	top, err := gitx.TopLevel(ctx, abs)
	if err != nil {
		return core.Repo{}, false, fmt.Errorf("%s is not a git repository", path)
	}
	top = gitx.Repo{Dir: top}.MainClone(ctx)
	g := gitx.Repo{Dir: top}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.IndexFunc(r.list, func(x core.Repo) bool { return x.Path == top }); i >= 0 {
		return r.list[i], false, nil
	}
	if src == core.RepoDiscovered && slices.Contains(r.ignored, top) {
		return core.Repo{}, false, fmt.Errorf("%s was removed", top)
	}
	r.ignored = slices.DeleteFunc(r.ignored, func(p string) bool { return p == top })
	id := core.RepoID(hostcore.Sanitise(filepath.Base(top)))
	for n := 2; slices.ContainsFunc(r.list, func(x core.Repo) bool { return x.ID == id }); n++ {
		id = core.RepoID(fmt.Sprintf("%s-%d", hostcore.Sanitise(filepath.Base(top)), n))
	}
	repo := core.Repo{ID: id, Path: top, Remote: g.RemoteURL(ctx), BaseBranch: g.DefaultBranch(ctx), Source: src,
		Added: r.now().UTC()}
	r.list = append(r.list, repo)
	return repo, true, r.save()
}

// remove unregisters a repo and ignores its path from now on.
func (r *repoList) remove(id core.RepoID) (core.Repo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.list, func(x core.Repo) bool { return x.ID == id })
	if i < 0 {
		return core.Repo{}, fmt.Errorf("no repo %q", id)
	}
	repo := r.list[i]
	r.list = slices.Delete(slices.Clone(r.list), i, i+1)
	if !slices.Contains(r.ignored, repo.Path) {
		r.ignored = append(r.ignored, repo.Path)
	}
	return repo, r.save()
}

// used marks a repo as just used by a session.
func (r *repoList) used(id core.RepoID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := slices.IndexFunc(r.list, func(x core.Repo) bool { return x.ID == id })
	if i < 0 {
		return nil
	}
	r.list = slices.Clone(r.list)
	r.list[i].Used = r.now().UTC()
	return r.save()
}

func (r *repoList) isIgnored(path string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Contains(r.ignored, path)
}

// find resolves a registered repo by ID, or by its path.
func (r *repoList) find(ref string) (core.Repo, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, x := range r.list {
		if string(x.ID) == ref || x.Path == ref {
			return x, true
		}
	}
	return core.Repo{}, false
}

// Changed tells the store the list as it is now.
func (x *Registry) Changed(added ...core.RepoID) {
	_ = x.st.Emit(core.ReposChanged{Repos: x.list.all(), Added: added})
}

// Add registers a checkout, telling the store when it's new.
func (x *Registry) Add(ctx context.Context, path string, src core.RepoSource) (core.Repo, error) {
	r, added, err := x.list.add(ctx, path, src)
	if err == nil && added {
		x.Changed(r.ID)
	}
	return r, err
}

// Resolve finds a repo by ID or path for a session, registering a path it hasn't seen, and marks it used.
func (x *Registry) Resolve(ctx context.Context, ref string) (core.Repo, error) {
	r, ok := x.list.find(ref)
	if !ok {
		if !filepath.IsAbs(ref) && ref != "." && filepath.Base(ref) == ref {
			return core.Repo{}, fmt.Errorf("no repo %q: add its checkout (repos.add {path}) or clone it (repos.clone {url})", ref)
		}
		var err error
		if r, err = x.Add(ctx, ref, core.RepoSession); err != nil {
			return core.Repo{}, err
		}
	}
	if err := x.list.used(r.ID); err != nil {
		x.env.Log.Warn("repos.json", "err", err)
	}
	x.Changed()
	return r, nil
}

// Find is a registered repo by ID, or by its path.
func (x *Registry) Find(ref string) (core.Repo, bool) { return x.list.find(ref) }

// Repos is the registered checkouts.
func (x *Registry) Repos() []core.Repo { return x.list.all() }

// AddRepoPath registers the checkout at path, or returns it if it's already registered.
func (x *Registry) AddRepoPath(ctx context.Context, path string) (core.Repo, error) {
	return x.Add(ctx, path, core.RepoAdded)
}

// RemoveRepo unregisters a repo, leaving its files, and keeps discovery from adding it back. A live session with a
// worktree of it stops it.
func (x *Registry) RemoveRepo(id core.RepoID) error {
	r, ok := x.list.find(string(id))
	if !ok {
		return fmt.Errorf("no repo %q", id)
	}
	for _, se := range x.st.Snapshot().Sessions {
		if se.Archived {
			continue
		}
		if slices.ContainsFunc(se.Projects, func(p core.Project) bool { return p.Repo == r.Path || p.Dir == r.Path }) {
			return fmt.Errorf("session %s has a worktree of %s: archive it first", cmp.Or(se.Name, string(se.ID)), r.ID)
		}
	}
	if _, err := x.list.remove(r.ID); err != nil {
		return err
	}
	x.Changed()
	return nil
}

// RepoCheck is what a path would add, as the Add repo dialog shows it while it's typed.
type RepoCheck struct {
	Path     string      `json:"path"`               // the checkout it adds: a worktree's main clone
	Worktree bool        `json:"worktree,omitempty"` // the path is a worktree of Path
	Branch   string      `json:"branch,omitempty"`
	Remote   string      `json:"remote,omitempty"`
	Known    core.RepoID `json:"known,omitempty"` // already registered as this
	Error    string      `json:"error,omitempty"`
}

// CheckRepoPath says what adding the checkout at path would do, without adding it.
func (x *Registry) CheckRepoPath(ctx context.Context, p string) (c RepoCheck) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return RepoCheck{Error: err.Error()}
	}
	top, err := gitx.TopLevel(ctx, abs)
	if err != nil {
		return RepoCheck{Error: "not a git repository"}
	}
	main := gitx.Repo{Dir: top}.MainClone(ctx)
	g := gitx.Repo{Dir: main}
	c = RepoCheck{Path: main, Worktree: main != top, Remote: g.RemoteURL(ctx)}
	c.Branch = g.CurrentBranch(ctx)
	if r, ok := x.list.find(main); ok {
		c.Known = r.ID
	}
	return c
}

// CloneReq is repos.clone: a URL, the folder its checkout goes in, and its name there.
type CloneReq struct {
	URL    string `json:"url"`
	Parent string `json:"parent,omitempty"` // CloneParent if ""
	Name   string `json:"name,omitempty"`   // the URL's last part if ""
	// Progress has git's progress lines as the clone runs.
	Progress func(line string) `json:"-"`
}

// CloneTarget is where a clone would go, and what's there now.
type CloneTarget struct {
	Dir    string `json:"dir"`
	Name   string `json:"name"`
	Exists bool   `json:"exists,omitempty"` // something is at Dir
	Same   bool   `json:"same,omitempty"`   // it's a clone of the URL: it's added as it is
}

// CloneName is the folder a clone of url is named by default: its last part, with .git dropped.
func CloneName(url string) string {
	return hostcore.Sanitise(strings.TrimSuffix(path.Base(strings.TrimRight(strings.ReplaceAll(strings.TrimSpace(url), ":", "/"), "/")), ".git"))
}

// CloneParent is the folder clones go in by default: the one most known repos share, else the repos directory.
func (x *Registry) CloneParent() string {
	n := map[string]int{}
	best := ""
	for _, r := range x.list.all() {
		p := filepath.Dir(r.Path)
		if hostcore.Within(p, x.env.Home) {
			continue
		}
		n[p]++
		if n[p] > n[best] || n[p] == n[best] && p < best {
			best = p
		}
	}
	return cmp.Or(best, x.Dir())
}

// SetReposDir sets where clones and new repos go when nothing else says (repos.dir); "" puts back the default.
func (x *Registry) SetReposDir(dir string) {
	x.list.mu.Lock()
	defer x.list.mu.Unlock()
	x.list.dir = expandHome(strings.TrimSpace(dir))
}

// Dir is where clones and new repos go when nothing else says.
func (x *Registry) Dir() string {
	x.list.mu.Lock()
	defer x.list.mu.Unlock()
	return cmp.Or(x.list.dir, x.defaultDir())
}

// CloneTarget says where a clone would go and what's there.
func (x *Registry) CloneTarget(ctx context.Context, r CloneReq) (CloneTarget, error) {
	url := strings.TrimSpace(r.URL)
	name := hostcore.Sanitise(r.Name)
	if name == "" {
		name = CloneName(url)
	}
	if name == "" {
		return CloneTarget{}, fmt.Errorf("can't name a checkout of %s: give it a name", url)
	}
	parent := cmp.Or(expandHome(strings.TrimSpace(r.Parent)), x.CloneParent())
	t := CloneTarget{Dir: filepath.Join(parent, name), Name: name}
	if _, err := os.Stat(t.Dir); err == nil {
		t.Exists = true
		t.Same = gitx.IsRepo(t.Dir) && (gitx.Repo{Dir: t.Dir}).RemoteURL(ctx) == url
	}
	return t, nil
}

// CloneRepo clones a repo with the user's own git and credentials, and registers it. A checkout already there of
// the same URL is registered as it is. Cancelling ctx stops the clone and takes away what it made.
func (x *Registry) CloneRepo(ctx context.Context, r CloneReq) (core.Repo, error) {
	url := strings.TrimSpace(r.URL)
	if url == "" {
		return core.Repo{}, errors.New("clone what? No URL")
	}
	t, err := x.CloneTarget(ctx, r)
	if err != nil {
		return core.Repo{}, err
	}
	if t.Exists {
		if t.Same {
			return x.Add(ctx, t.Dir, core.RepoCloned)
		}
		return core.Repo{}, fmt.Errorf("%s is already there and isn't a clone of %s: give the clone another name", t.Dir, url)
	}
	if err := gitx.CloneProgress(ctx, url, t.Dir, r.Progress); err != nil {
		_ = os.RemoveAll(t.Dir)
		if needsCredentials(err.Error()) {
			err = fmt.Errorf("%w (it clones with this machine's git credentials: set them up)", err)
		}
		return core.Repo{}, err
	}
	return x.Add(ctx, t.Dir, core.RepoCloned)
}

// needsCredentials says whether git's message is about credentials it didn't have.
func needsCredentials(msg string) bool {
	for _, s := range []string{"Permission denied", "could not read Username", "Authentication failed", "terminal prompts disabled"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

// expandHome expands a leading ~.
func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[1:])
		}
	}
	return p
}
