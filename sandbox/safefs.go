package sandbox

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes data to path, which must lie under root, without following
// a symlink anywhere below root: the agent could have planted one to steer a
// host write onto ~/.bashrc. The write goes through an os.Root, so a link
// swapped in after the check still can't lead out, and the file is replaced by
// rename, which swaps a symlink out rather than writing through it.
func WriteFile(root, path string, data []byte, perm os.FileMode) error {
	if err := CheckPath(root, filepath.Dir(path)); err != nil {
		return err
	}
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	if err != nil {
		return err
	}
	r, err := os.OpenRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	tmp, f, err := createTemp(r, rel)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = r.Remove(tmp)
		return err
	}
	if err := f.Chmod(perm); err != nil {
		_ = f.Close()
		_ = r.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = r.Remove(tmp)
		return err
	}
	if err := r.Rename(tmp, rel); err != nil {
		_ = r.Remove(tmp)
		return err
	}
	return nil
}

// createTemp makes a new file next to rel inside r.
func createTemp(r *os.Root, rel string) (string, *os.File, error) {
	for {
		b := make([]byte, 6)
		_, _ = rand.Read(b)
		name := filepath.Join(filepath.Dir(rel), "."+filepath.Base(rel)+"."+hex.EncodeToString(b))
		f, err := r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		return name, f, err
	}
}

// CheckPath fails when any component of path from root down is a symlink.
func CheckPath(root, path string) error {
	root = filepath.Clean(root)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is outside %s", path, root)
	}
	cur := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		cur = filepath.Join(cur, part)
		fi, err := os.Lstat(cur)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink, and Oriel does not write through links in a sandboxed workspace", cur)
		}
	}
	return nil
}

// WriteIn is WriteFile when root is sandboxed, and a plain write otherwise.
func WriteIn(root, path string, data []byte, perm os.FileMode) error {
	if Managed(root) {
		return WriteFile(root, path, data, perm)
	}
	return os.WriteFile(path, data, perm)
}

// ReadFile is os.ReadFile that, in a sandboxed workspace, refuses a path whose
// symlinks lead out of it: the agent could plant one at a file oriel reads and
// get the host file's contents back in whatever oriel writes from it.
func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Open is os.Open with ReadFile's check. The file is opened through an
// os.Root, so a link swapped in after the check still can't lead out.
func Open(path string) (*os.File, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	root, ok := managedRoot(filepath.Dir(abs))
	if !ok {
		return os.Open(path)
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if !within(real, realRoot) {
		return nil, fmt.Errorf("%s leads out of the workspace, and Oriel does not read through it", path)
	}
	rel, err := filepath.Rel(realRoot, real)
	if err != nil {
		return nil, err
	}
	r, err := os.OpenRoot(realRoot)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return r.Open(rel)
}
