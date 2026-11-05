package agentrun

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
)

// markThen queues a mark, run off the event loop in the order asked, and calls then, if any, with what it found.
// Marks asked for while one is made are made once, after it: what it finds is the newest asker's, and the others
// find nothing. A mark snapshots only the projects the file watcher saw change, unless it's full.
func (h *session) markThen(full bool, then func([]core.FileDiff)) {
	h.mu.Lock()
	h.markq = append(h.markq, then)
	h.markFull = h.markFull || full
	h.markAt = time.Now()
	busy := h.marking
	h.marking = true
	h.mu.Unlock()
	if busy {
		return
	}
	h.r.env.Go("a work-tree mark", func() {
		for {
			h.mu.Lock()
			if len(h.markq) == 0 {
				h.marking = false
				h.mu.Unlock()
				return
			}
			waiting, full, at := h.markq, h.markFull, h.markAt
			h.markq, h.markFull = nil, false
			watching := h.watch != nil
			h.mu.Unlock()
			if wait := time.Until(at.Add(eventsSettle)); watching && wait > 0 {
				time.Sleep(wait) // what changed up to the ask has been heard
			}
			files := h.mark(full)
			for i := len(waiting) - 1; i >= 0; i-- {
				if then := waiting[i]; then != nil {
					then(files)
					files = nil
				}
			}
		}
	})
}

// mark snapshots the session's projects' work trees (all of them when full, else those that may have changed since),
// and returns what changed in them since the last mark (since HEAD before the first): each file's diff, by its
// absolute path.
func (h *session) mark(full bool) []core.FileDiff {
	m := h.r.appWatches(h.id)
	h.mu.Lock()
	var gone *treeWatch
	if h.watch != nil && h.watch.m != m {
		gone, h.watch = h.watch, nil // the app's manager went, or another took its place
	}
	if h.watch == nil && h.sess != nil {
		h.watch = newTreeWatch(m)
	}
	watch, trees := h.watch, h.trees
	h.mu.Unlock()
	gone.close()
	var out []core.FileDiff
	for _, p := range h.snapshot().Projects {
		if changed := watch.changed(p.Dir); !full && !changed && trees[p.Dir] != "" {
			continue
		}
		env, err := sandbox.GitEnv(p.Dir)
		if err != nil {
			continue
		}
		repo := gitx.Repo{Dir: p.Dir, Env: env}
		tree, err := repo.Snapshot(h.ctx)
		if err != nil {
			continue
		}
		h.mu.Lock()
		prev := h.trees[p.Dir]
		if h.trees == nil {
			h.trees = map[string]string{}
		}
		h.trees[p.Dir] = tree
		h.mu.Unlock()
		if prev == tree {
			continue
		}
		diff, err := repo.Diff(h.ctx, cmp.Or(prev, "HEAD"), tree)
		if err != nil {
			continue
		}
		for path, patch := range gitx.FilePatches(diff) {
			if d := core.PatchDiff(filepath.Join(p.Dir, filepath.FromSlash(path)), patch); len(d.Lines) > 0 {
				out = append(out, *d)
			}
		}
	}
	slices.SortFunc(out, func(a, b core.FileDiff) int { return strings.Compare(a.Path, b.Path) })
	return out
}
