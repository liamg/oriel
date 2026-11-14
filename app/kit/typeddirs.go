package kit

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// TypedDirs are the directory a typed path names, and the directories in it that start with its last part.
func TypedDirs(q, home string) []string {
	p := typedPath(q, home)
	p, _ = filepath.Abs(p)
	return typedDirs(q, p, func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.IsDir()
	}, func(p string) []fs.DirEntry {
		ents, _ := os.ReadDir(p)
		return ents
	})
}

// TypedDirs is TypedDirs read through the cache, for a frame: none until the folders are read, and none for a
// relative path.
func (r *Reads) TypedDirs(q, home string) []string {
	p := typedPath(q, home)
	if !filepath.IsAbs(p) {
		return nil
	}
	return typedDirs(q, filepath.Clean(p), func(p string) bool {
		st, _ := r.Stat(p)
		return st != nil && st.IsDir()
	}, func(p string) []fs.DirEntry {
		ents, _ := r.Dir(p)
		return ents
	})
}

func typedPath(q, home string) string {
	if strings.HasPrefix(q, "~") {
		return filepath.Join(home, q[1:])
	}
	return q
}

func typedDirs(q, p string, isDir func(string) bool, readDir func(string) []fs.DirEntry) []string {
	var out []string
	if isDir(p) {
		out = append(out, p)
	}
	parent, prefix := p, ""
	if !strings.HasSuffix(q, "/") {
		parent, prefix = filepath.Dir(p), filepath.Base(p)
	}
	for _, e := range readDir(parent) {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) && !strings.HasPrefix(e.Name(), ".") {
			if c := filepath.Join(parent, e.Name()); c != p {
				out = append(out, c)
			}
		}
	}
	return out
}
