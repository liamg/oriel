package shell

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
)

// dirScanDepth and maxDirs bound the walk of the home directory for the directories picker; a walk older than
// dirFresh is walked again.
const (
	dirScanDepth = 3
	maxDirs      = 5000
	dirFresh     = time.Minute
)

// dirList is the directories under home, git repos marked, walked off the loop: a list older than dirFresh is walked
// again in the background, the old one shown until the new is done.
type dirList struct {
	root string
	h    plugin.Host

	mu      sync.Mutex
	at      time.Time // when the last walk finished
	dirs    []string
	repos   map[string]bool
	walking bool
	done    bool // a walk has finished
	busy    kit.Busy
	work    kit.Work // the OS chooser, open while a person picks
}

// list is the directories found by the last walk and which are repos, starting a walk when it's due.
func (l *dirList) list() ([]string, map[string]bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.walking && (!l.done || time.Since(l.at) >= dirFresh) {
		l.walking = true
		l.busy.Add(1)
		plugin.Go(l.h, "walking directories", l.walk)
	}
	return l.dirs, l.repos
}

func (l *dirList) loading() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.done
}

func (l *dirList) walk() {
	defer l.busy.Done()
	dirs, repos := walkDirs(l.root)
	l.mu.Lock()
	l.dirs, l.repos, l.at, l.done, l.walking = dirs, repos, time.Now(), true, false
	l.mu.Unlock()
	l.h.Redraw()
}

// walkDirs is the directories under root, dirScanDepth deep and at most maxDirs, not into a git repo, and which of
// them (root too) are repos.
func walkDirs(root string) ([]string, map[string]bool) {
	dirs, repos := []string{}, map[string]bool{}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != root && kit.SkipDir(d.Name()) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		if p != root {
			dirs = append(dirs, p)
		}
		if isRepo(p) {
			repos[p] = true
			if p != root {
				return filepath.SkipDir
			}
		}
		if strings.Count(rel, string(filepath.Separator)) >= dirScanDepth-1 || len(dirs) >= maxDirs {
			return filepath.SkipDir
		}
		return nil
	})
	return dirs, repos
}

// directoriesSource is Open Directory's picker: recent directories first, then git repos and the other directories
// under home; a typed path lists what's in it; and Browse… asks the OS.
func directoriesSource(h plugin.Host, sh *shell) (uireg.PickerSource, registry.Disposable) {
	home, _ := os.UserHomeDir()
	all := &dirList{root: home, h: h}
	return uireg.PickerSource{ID: "directories", Title: "Directories", Order: 15, PickerOnly: true, Loading: all.loading,
		Items: func(_ registry.Ctx, query string) []uireg.PickerItem {
			var out []uireg.PickerItem
			seen := map[string]bool{}
			dirs, repos := all.list()
			add := func(p string, boost int, note string, repo bool) {
				if seen[p] {
					return
				}
				seen[p] = true
				if note == "" && repo {
					note = "git"
				}
				out = append(out, uireg.PickerItem{ID: p, Label: kit.TildePath(p), Icon: "▤", Boost: boost, Note: note, Data: p})
			}
			if q := strings.TrimSpace(query); strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") || strings.HasPrefix(q, ".") {
				for _, p := range kit.TypedDirs(q, home) {
					add(p, 50, "", isRepo(p))
				}
			}
			if kit.ChooseDirAvailable() {
				out = append(out, uireg.PickerItem{ID: "browse", Label: "Browse…", Detail: "the system's folder chooser", Icon: "…", Boost: 30})
			}
			for _, d := range sh.recents.list() {
				add(d.Path, 40, "recent", false)
			}
			for _, p := range dirs {
				b := 0
				if repos[p] {
					b = 20
				}
				add(p, b, "", repos[p])
			}
			return out
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			if it.ID == "browse" {
				all.work.Loop(func(cctx context.Context) { // waits on a person
					dir, err := kit.ChooseDir(cctx)
					h.Do(func() {
						if err == nil && dir != "" {
							err = sh.openDir(context.Background(), h, dir)
						}
						if err != nil {
							h.Log().Error("browse", "err", err)
						}
					})
				})
				return nil
			}
			return sh.openDir(ctx, h, it.Data.(string))
		},
	}, kit.OnDispose(func() { all.work.Stop(); all.busy.Wait() })
}
