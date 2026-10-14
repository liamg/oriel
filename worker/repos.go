package worker

import (
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

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// repoList is the checkouts this worker knows, kept in repos.json.
type repoList struct {
	path string
	mu   sync.Mutex
	list []core.Repo
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
	return json.Unmarshal(b, &r.list)
}

func (r *repoList) all() []core.Repo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.list)
}

func (r *repoList) save() error {
	b, err := json.MarshalIndent(r.list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(r.path, append(b, '\n'), 0o600)
}

// add registers the checkout at path, or returns it if it's already registered.
func (r *repoList) add(ctx context.Context, worker core.WorkerID, path string) (core.Repo, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return core.Repo{}, err
	}
	top, err := gitx.TopLevel(ctx, abs)
	if err != nil {
		return core.Repo{}, fmt.Errorf("%s is not a git repository", path)
	}
	top = gitx.Repo{Dir: top}.MainClone(ctx)
	g := gitx.Repo{Dir: top}
	r.mu.Lock()
	defer r.mu.Unlock()
	if i := slices.IndexFunc(r.list, func(x core.Repo) bool { return x.Path == top }); i >= 0 {
		return r.list[i], nil
	}
	id := core.RepoID(Sanitise(filepath.Base(top)))
	for n := 2; slices.ContainsFunc(r.list, func(x core.Repo) bool { return x.ID == id }); n++ {
		id = core.RepoID(fmt.Sprintf("%s-%d", Sanitise(filepath.Base(top)), n))
	}
	repo := core.Repo{ID: id, Worker: worker, Path: top, Remote: g.RemoteURL(ctx), BaseBranch: g.DefaultBranch(ctx)}
	r.list = append(r.list, repo)
	return repo, r.save()
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

// resolve finds a repo by ID or path, registering a path it hasn't seen.
func (w *Worker) resolveRepo(ctx context.Context, ref string) (core.Repo, error) {
	if r, ok := w.repos.find(ref); ok {
		return r, nil
	}
	if filepath.IsAbs(ref) || ref == "." || filepath.Base(ref) != ref {
		return w.repos.add(ctx, w.opts.ID, ref)
	}
	return core.Repo{}, fmt.Errorf("no repo %q on this worker: add its checkout (oriel repos add <path>) or clone it (oriel repos clone <url>)", ref)
}

// CloneReq is repos.clone: a URL, and the name its checkout gets in the repos directory (the URL's last part if "").
type CloneReq struct {
	URL  string `json:"url"`
	Name string `json:"name,omitempty"`
}

// CloneRepo clones a repo into the repos directory with the worker's own git and credentials, and registers it. A
// checkout already there of the same URL is registered as it is.
func (w *Worker) CloneRepo(ctx context.Context, r CloneReq) (core.Repo, error) {
	url := strings.TrimSpace(r.URL)
	if url == "" {
		return core.Repo{}, errors.New("clone what? No URL")
	}
	name := Sanitise(r.Name)
	if name == "" {
		name = Sanitise(strings.TrimSuffix(path.Base(strings.TrimRight(strings.ReplaceAll(url, ":", "/"), "/")), ".git"))
	}
	if name == "" {
		return core.Repo{}, fmt.Errorf("can't name a checkout of %s: give it a name", url)
	}
	dir := filepath.Join(w.opts.ReposDir, name)
	if _, err := os.Stat(dir); err == nil {
		if gitx.IsRepo(dir) && (gitx.Repo{Dir: dir}).RemoteURL(ctx) == url {
			return w.repos.add(ctx, w.opts.ID, dir)
		}
		return core.Repo{}, fmt.Errorf("%s is already there and isn't a clone of %s: give the clone another name", dir, url)
	}
	if err := gitx.Clone(ctx, url, dir); err != nil {
		_ = os.RemoveAll(dir)
		if needsCredentials(err.Error()) {
			err = fmt.Errorf("%w (the worker clones with its own machine's credentials: set them up there)", err)
		}
		return core.Repo{}, err
	}
	return w.repos.add(ctx, w.opts.ID, dir)
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
