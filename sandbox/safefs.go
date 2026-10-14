package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// WriteFile writes data to path, which must lie under root, without following
// a symlink anywhere below root: the agent could have planted one to steer a
// host write onto ~/.bashrc. The file is replaced by rename, which swaps a
// symlink out rather than writing through it.
func WriteFile(root, path string, data []byte, perm os.FileMode) error {
	if err := CheckPath(root, filepath.Dir(path)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	if err := os.Chmod(tmp.Name(), perm); err != nil {
		_ = os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
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
	if err := checkRead(path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

// Open is os.Open with ReadFile's check.
func Open(path string) (*os.File, error) {
	if err := checkRead(path); err != nil {
		return nil, err
	}
	return os.Open(path)
}

func checkRead(path string) error {
	root, ok := managedRoot(filepath.Dir(path))
	if !ok {
		return nil
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if !within(real, realRoot) {
		return fmt.Errorf("%s leads out of the workspace, and Oriel does not read through it", path)
	}
	return nil
}
