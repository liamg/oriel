// Package atomicfile writes a file so it's never seen empty or half-written: the settings a crash mid-write, or a
// reader at the wrong moment, would otherwise lose. Like os.WriteFile it may run anywhere, the loop included.
package atomicfile

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// Write puts data in name's place at once: written beside it, synced, then renamed over it, so a reader (or the
// next start, after a crash) finds the old file or the new one, never an empty or half-written one. A symlink stays
// one: its target is what's replaced. A file already there keeps its permissions; perm is a new one's.
func Write(name string, data []byte, perm fs.FileMode) error { return write(name, data, perm, true) }

// WriteSecret is Write for what only the user may read (tokens, credentials): the file is 0600 afterwards, whatever
// it was before, and never readable by others while it's written.
func WriteSecret(name string, data []byte) error { return write(name, data, 0o600, false) }

func write(name string, data []byte, perm fs.FileMode, keepPerm bool) error {
	if real, err := filepath.EvalSymlinks(name); err == nil {
		name = real
	}
	if fi, err := os.Stat(name); err == nil && keepPerm {
		perm = fi.Mode().Perm() // as os.WriteFile leaves a file it overwrites
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*")
	if err != nil {
		return err
	}
	done := false
	defer func() {
		if !done {
			os.Remove(tmp.Name())
		}
	}()
	_, err = tmp.Write(data)
	err = errors.Join(err, tmp.Sync(), tmp.Chmod(perm))
	if err = errors.Join(err, tmp.Close()); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), name); err != nil {
		return err
	}
	done = true
	return nil
}
