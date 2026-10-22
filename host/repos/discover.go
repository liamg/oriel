package repos

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// scanCap is the most repos one scan adds.
const scanCap = 200

// ScanRepos registers the siblings of the known repos: for each folder one sits in, the git repos directly in it
// that are their own main clone. A folder read before is skipped unless all is set; removed repos stay removed.
func (x *Registry) ScanRepos(ctx context.Context, all bool) (added []core.Repo, err error) {
	home, _ := os.UserHomeDir()
	var parents []string
	for _, r := range x.list.all() {
		p := filepath.Dir(r.Path)
		if p == r.Path || p == string(filepath.Separator) || p == home || hostcore.Within(p, x.env.Home) || slices.Contains(parents, p) {
			continue
		}
		parents = append(parents, p)
	}
	for _, p := range parents {
		x.list.mu.Lock()
		seen := x.list.scanned[p]
		x.list.scanned[p] = true
		x.list.mu.Unlock()
		if seen && !all {
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if ctx.Err() != nil {
				return added, ctx.Err()
			}
			dir := filepath.Join(p, e.Name())
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") || hostcore.Within(dir, x.env.Home) || x.list.isIgnored(dir) {
				continue
			}
			if _, ok := x.list.find(dir); ok || !gitx.IsRepo(dir) || (gitx.Repo{Dir: dir}).MainClone(ctx) != dir {
				continue
			}
			if len(added) == scanCap {
				x.env.Log.Info("repo discovery stopped at its cap", "cap", scanCap, "folder", p)
				break
			}
			if r, ok, err := x.list.add(ctx, dir, core.RepoDiscovered); err == nil && ok {
				added = append(added, r)
			}
		}
	}
	if len(added) > 0 {
		ids := make([]core.RepoID, len(added))
		for i, r := range added {
			ids[i] = r.ID
		}
		x.Changed(ids...)
	}
	return added, nil
}
