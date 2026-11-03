package lang

import (
	"context"
	"path/filepath"
	"unicode"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/testrun"
)

// scan finds every test in the workspace off the loop, then follows the files git sees change.
func (x *Tests) scan() {
	ws := x.workspace()
	if ws.Root == "" {
		x.setFound("", nil)
		return
	}
	x.scanFrom = kit.WindowNow(x.h)
	x.mu.Lock()
	gen := x.scanGen
	x.mu.Unlock()
	x.work.Go(func(ctx context.Context) {
		byFile := map[string][]testrun.Item{}
		for _, it := range x.set.Scan(ctx, ws) {
			byFile[it.File] = append(byFile[it.File], it)
		}
		if ctx.Err() != nil {
			return
		}
		x.mu.Lock()
		if gen != x.scanGen { // the window moved on while it ran
			x.mu.Unlock()
			return
		}
		for f, items := range x.scanned {
			byFile[f] = items // found in an open buffer while the scan ran
		}
		x.scanned, x.byKey = byFile, nil
		x.mu.Unlock()
		x.h.Redraw()
	})
	if st := kit.GitOf(x.h).Tree("").Status(); st != nil {
		root := st.Repo().Dir
		x.unwatch = st.Watch(func(paths []string) {
			for _, p := range paths {
				x.rescan(filepath.Join(root, filepath.FromSlash(p)))
			}
		})
	}
}

// rescan reads one file's tests again from disk; a file that's gone has none.
func (x *Tests) rescan(path string) {
	if !x.set.TestFile(path) {
		return
	}
	x.setFound(path, x.set.ScanFile(x.workspace(), path))
	x.h.Redraw()
}

// setFound records a file's tests for the panel.
func (x *Tests) setFound(path string, items []testrun.Item) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.scanned == nil {
		x.scanned = map[string][]testrun.Item{}
	}
	x.byKey = nil
	if len(items) == 0 {
		delete(x.scanned, path)
		return
	}
	x.scanned[path] = items
}

// known is every test found, by key, and the packages they're in; nil before the first scan has finished.
func (x *Tests) known() (map[string]testrun.Item, map[string]bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.scanned == nil {
		return nil, nil
	}
	if x.byKey == nil {
		x.byKey, x.pkgs = map[string]testrun.Item{}, map[string]bool{}
		for _, items := range x.scanned {
			for _, it := range items {
				if it.Kind != testrun.KindPackage && it.Kind != testrun.KindFile {
					x.byKey[it.Key()] = it
					x.pkgs[it.Package] = true
				}
			}
		}
	}
	return x.byKey, x.pkgs
}

// current says whether a result is for a test that still exists: its package still has tests, and it's one of
// them or a case of one (a subtest found only when it ran).
func current(known map[string]testrun.Item, pkgs map[string]bool, r *testrun.Result) bool {
	if known == nil {
		return true
	}
	if !pkgs[r.Package] {
		return false
	}
	if r.Test == "" {
		return true
	}
	for i, c := range r.Test {
		if i > 0 && !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '_' {
			if _, ok := known[testrun.Key(r.Package, r.Test[:i])]; ok {
				return true
			}
		}
	}
	_, ok := known[r.Key()]
	return ok
}
