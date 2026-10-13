package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
)

// dirScanDepth and maxDirs bound the walk of the home directory for the directories picker.
const (
	dirScanDepth = 3
	maxDirs      = 5000
)

// dirList is the directories under home, git repos marked, listed again when older than a minute.
type dirList struct {
	root  string
	at    time.Time
	dirs  []string
	repos map[string]bool
}

func (l *dirList) list() ([]string, map[string]bool) {
	if l.dirs != nil && time.Since(l.at) < time.Minute {
		return l.dirs, l.repos
	}
	l.at, l.dirs, l.repos = time.Now(), []string{}, map[string]bool{}
	filepath.WalkDir(l.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if p != l.root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
			return filepath.SkipDir
		}
		rel, _ := filepath.Rel(l.root, p)
		if p != l.root {
			l.dirs = append(l.dirs, p)
		}
		if isRepo(p) {
			l.repos[p] = true
			if p != l.root {
				return filepath.SkipDir
			}
		}
		if strings.Count(rel, string(filepath.Separator)) >= dirScanDepth-1 || len(l.dirs) >= maxDirs {
			return filepath.SkipDir
		}
		return nil
	})
	return l.dirs, l.repos
}

// directoriesSource is Open Directory's picker: recent directories first, then git repos and the other directories
// under home; a typed path lists what's in it; and Browse… asks the OS.
func directoriesSource(h ext.Host, sh *shell) registry.PickerSource {
	home, _ := os.UserHomeDir()
	all := &dirList{root: home}
	return registry.PickerSource{ID: "directories", Title: "Directories", Order: 15, PickerOnly: true,
		Items: func(_ registry.Ctx, query string) []registry.PickerItem {
			var out []registry.PickerItem
			seen := map[string]bool{}
			add := func(p string, boost int, note string) {
				if seen[p] {
					return
				}
				seen[p] = true
				if note == "" && isRepo(p) {
					note = "git"
				}
				out = append(out, registry.PickerItem{ID: p, Label: tildePath(p), Icon: "▤", Boost: boost, Note: note, Data: p})
			}
			if q := strings.TrimSpace(query); strings.HasPrefix(q, "/") || strings.HasPrefix(q, "~") || strings.HasPrefix(q, ".") {
				for _, p := range typedDirs(q, home) {
					add(p, 50, "")
				}
			}
			if chooseDirAvailable() {
				out = append(out, registry.PickerItem{ID: "browse", Label: "Browse…", Detail: "the system's folder chooser", Icon: "…", Boost: 30})
			}
			for _, d := range sh.recents.list() {
				add(d.Path, 40, "recent")
			}
			dirs, repos := all.list()
			for _, p := range dirs {
				b := 0
				if repos[p] {
					b = 20
				}
				add(p, b, "")
			}
			return out
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
			if it.ID == "browse" {
				go func() {
					dir, err := chooseDir()
					h.Do(func() {
						if err == nil && dir != "" {
							err = sh.openDir(dir)
						}
						if err != nil {
							h.Log().Error("browse", "err", err)
						}
					})
				}()
				return nil
			}
			return sh.openDir(it.Data.(string))
		},
	}
}

// typedDirs are the directory a typed path names, and the directories in it that start with its last part.
func typedDirs(q, home string) []string {
	p := q
	if strings.HasPrefix(p, "~") {
		p = filepath.Join(home, p[1:])
	}
	p, _ = filepath.Abs(p)
	var out []string
	if st, err := os.Stat(p); err == nil && st.IsDir() {
		out = append(out, p)
	}
	parent, prefix := p, ""
	if !strings.HasSuffix(q, "/") {
		parent, prefix = filepath.Dir(p), filepath.Base(p)
	}
	ents, _ := os.ReadDir(parent)
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			if c := filepath.Join(parent, e.Name()); c != p {
				out = append(out, c)
			}
		}
	}
	return out
}
