// Package gitsrc is a work tree's git status as watch sources, each read again only when what it depends on changes:
//
//   - Refs: HEAD, the refs, the base set and the config; about 6 gits, only when one of those moves.
//   - Tracked: the tracked files (ls-files), when the index changes.
//   - Work: git status and the diff against the base, when any file in the tree, or the index, changes.
//   - Status: the three put together, with no git of its own.
//   - Counted: Status with line counts, three numstats more, only while something shows them.
//   - Remote: origin's URL, when the config changes.
package gitsrc

import (
	"path"
	"path/filepath"
	"slices"
	"sync/atomic"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/watch"
)

// Tree is one work tree's sources. Make one per root and manager, and keep it: its watch leaves out what its last
// Work reading said git ignores.
type Tree struct {
	Root        string
	repo        gitx.Repo
	dir, common string
	work        atomic.Pointer[gitx.Work]
}

// New is root's sources; cache is shared by a manager's trees.
func New(root string, cache *gitx.Cache) *Tree {
	root = filepath.Clean(root)
	t := &Tree{Root: root, repo: gitx.Repo{Dir: root, Cache: cache}}
	t.dir, t.common = gitx.Dirs(root)
	return t
}

// Repo is the tree's repo, for git's other calls.
func (t *Tree) Repo() gitx.Repo { return t.repo }

func (t *Tree) key(what string) string { return "git." + what + ":" + t.Root }

// refFiles are what Refs depends on: HEAD and the base set in the work tree's git folder, and the refs and config
// in the repository's.
func (t *Tree) refFiles() []watch.Files {
	return []watch.Files{
		{Path: filepath.Join(t.dir, "HEAD")},
		{Path: filepath.Join(t.dir, "oriel-base")},
		{Path: filepath.Join(t.common, "refs"), Tree: true},
		{Path: filepath.Join(t.common, "packed-refs")},
		{Path: filepath.Join(t.common, "config")},
	}
}

func (t *Tree) index() watch.Files { return watch.Files{Path: filepath.Join(t.dir, "index")} }

func (t *Tree) Refs() watch.Source[gitx.Refs] {
	return watch.Source[gitx.Refs]{Key: t.key("refs"), Files: t.refFiles(), Same: gitx.Refs.Same,
		Read: func(r watch.Reader) (gitx.Refs, error) { return t.repo.ReadRefs(r.Context()) }}
}

func (t *Tree) Tracked() watch.Source[[]string] {
	return watch.Source[[]string]{Key: t.key("tracked"), Files: []watch.Files{t.index()}, Same: slices.Equal[[]string],
		Read: func(r watch.Reader) ([]string, error) { return t.repo.ReadTracked(r.Context()) }}
}

func (t *Tree) Work() watch.Source[gitx.Work] {
	refs := t.Refs()
	return watch.Source[gitx.Work]{Key: t.key("work"), Deps: []watch.AnySource{refs}, Same: gitx.Work.Same,
		Files: []watch.Files{{Path: t.Root, Tree: true, Skip: t.skip}, t.index()},
		Read: func(r watch.Reader) (gitx.Work, error) {
			rf, _ := watch.Dep(r, refs)
			w, err := t.repo.ReadWork(r.Context(), rf)
			if err == nil {
				t.work.Store(&w)
			}
			return w, err
		}}
}

// skip leaves out of the tree's watch what doesn't change its status: .git (Refs and the index watch what matters
// there), node_modules, and what git ignores.
func (t *Tree) skip(rel string) bool {
	if rel == ".git" || path.Base(rel) == "node_modules" {
		return true
	}
	w := t.work.Load()
	return w != nil && (w.Ignored(rel) || w.Ignored(rel+"/"))
}

// Status is the tree's status in every scope, without line counts.
func (t *Tree) Status() watch.Source[*gitx.State] {
	refs, tracked, work := t.Refs(), t.Tracked(), t.Work()
	return watch.Source[*gitx.State]{Key: t.key("status"), Deps: []watch.AnySource{refs, tracked, work}, Same: (*gitx.State).Same,
		Read: func(r watch.Reader) (*gitx.State, error) {
			rf, _ := watch.Dep(r, refs)
			ts, _ := watch.Dep(r, tracked)
			w, _ := watch.Dep(r, work)
			return gitx.Compose(rf, ts, w, nil), nil
		}}
}

// Counted is Status with each file's line counts: three numstats more, so subscribe only while they're shown.
func (t *Tree) Counted() watch.Source[*gitx.State] {
	refs, tracked, work := t.Refs(), t.Tracked(), t.Work()
	return watch.Source[*gitx.State]{Key: t.key("counted"), Deps: []watch.AnySource{refs, tracked, work}, Same: (*gitx.State).Same,
		Read: func(r watch.Reader) (*gitx.State, error) {
			rf, _ := watch.Dep(r, refs)
			ts, _ := watch.Dep(r, tracked)
			w, _ := watch.Dep(r, work)
			l, err := t.repo.ReadLines(r.Context(), rf, w)
			if err != nil {
				return nil, err
			}
			return gitx.Compose(rf, ts, w, &l), nil
		}}
}

// Remote is origin's URL.
func (t *Tree) Remote() watch.Source[string] {
	return watch.Source[string]{Key: t.key("remote"), Files: []watch.Files{{Path: filepath.Join(t.common, "config")}},
		Same: func(a, b string) bool { return a == b },
		Read: func(r watch.Reader) (string, error) { return t.repo.Origin(r.Context()) }}
}
