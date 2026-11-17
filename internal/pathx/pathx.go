// Package pathx answers where one path is in relation to another.
package pathx

import "path/filepath"

// Inside reports whether path is dir or under it, as written; never for an empty path or dir.
func Inside(path, dir string) bool {
	if path == "" || dir == "" {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(path))
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// StrictlyInside reports whether path is under dir and not dir itself.
func StrictlyInside(path, dir string) bool {
	return Inside(path, dir) && filepath.Clean(path) != filepath.Clean(dir)
}

// InsideResolved is Inside, as written or with symbolic links resolved.
func InsideResolved(path, dir string) bool {
	if Inside(path, dir) {
		return true
	}
	rp, err1 := filepath.EvalSymlinks(path)
	rd, err2 := filepath.EvalSymlinks(dir)
	return err1 == nil && err2 == nil && Inside(rp, rd)
}
