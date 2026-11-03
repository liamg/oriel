package agentrun

import (
	"cmp"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
)

// markThen queues a mark, run off the event loop in the order asked, and calls then, if any, with what it found.
func (h *session) markThen(then func([]core.FileDiff)) {
	h.mu.Lock()
	h.markq = append(h.markq, then)
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
			then := h.markq[0]
			h.markq = h.markq[1:]
			h.mu.Unlock()
			if files := h.mark(); then != nil {
				then(files)
			}
		}
	})
}

// mark snapshots the session's projects' work trees, and returns what changed in them since the last mark (since HEAD
// before the first): each file's diff, by its absolute path.
func (h *session) mark() []core.FileDiff {
	var out []core.FileDiff
	for _, p := range h.snapshot().Projects {
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
