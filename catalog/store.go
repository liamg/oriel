package catalog

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
)

// Store is where plugins are installed (catalog.md, Files on disk): <Dir>/<id>/<commit>/, read-only, with
// <Dir>/<id>/current a link to the active commit, so going back is cheap.
type Store struct {
	Dir string // ~/.local/share/oriel/plugins
}

// Path is where id's commit is installed.
func (s Store) Path(id, commit string) string {
	return filepath.Join(s.Dir, filepath.FromSlash(id), commit)
}

// Has says whether id's commit is installed.
func (s Store) Has(id, commit string) bool {
	_, err := os.Stat(filepath.Join(s.Path(id, commit), plugin.ManifestFile))
	return commit != "" && err == nil
}

// FetchGit installs id from a git repository at rev (a tag, branch or commit; the default branch if ""), and says
// which commit that was. A commit already installed isn't fetched again. The checkout has no .git, is within
// plugin.MaxSize, and must be the plugin it's installed as.
func (s Store) FetchGit(ctx context.Context, id, url, rev string) (dir, commit string, err error) {
	if isCommit(rev) && s.Has(id, rev) {
		return s.Path(id, rev), rev, s.activate(id, rev)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, filepath.FromSlash(id)), 0o755); err != nil {
		return "", "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(s.Dir, filepath.FromSlash(id)), ".fetch-")
	if err != nil {
		return "", "", err
	}
	defer os.RemoveAll(tmp)
	work := filepath.Join(tmp, "src")
	if err := gitx.Clone(ctx, url, work); err != nil {
		return "", "", err
	}
	r := gitx.Repo{Dir: work}
	if rev != "" {
		if _, err := r.Run(ctx, "checkout", "--quiet", "--detach", rev); err != nil {
			return "", "", fmt.Errorf("%s has no %s: %w", url, rev, err)
		}
	}
	if commit, err = r.Head(ctx); err != nil {
		return "", "", err
	}
	if err := os.RemoveAll(filepath.Join(work, ".git")); err != nil {
		return "", "", err
	}
	if err := checkPlugin(work, id); err != nil {
		return "", "", err
	}
	dir = s.Path(id, commit)
	if !s.Has(id, commit) {
		os.RemoveAll(dir)
		if err := os.Rename(work, dir); err != nil {
			return "", "", err
		}
		readOnly(dir)
	}
	return dir, commit, s.activate(id, commit)
}

// FetchArchive installs id's commit from a release archive (Unpack), checked against its sha256 first.
func (s Store) FetchArchive(id, commit, archive string) (string, error) {
	if s.Has(id, commit) {
		return s.Path(id, commit), s.activate(id, commit)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, filepath.FromSlash(id)), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.MkdirTemp(filepath.Join(s.Dir, filepath.FromSlash(id)), ".fetch-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	if _, err := Unpack(archive, tmp); err != nil {
		return "", err
	}
	if err := checkPlugin(tmp, id); err != nil {
		return "", err
	}
	dir := s.Path(id, commit)
	os.RemoveAll(dir)
	if err := os.Rename(tmp, dir); err != nil {
		return "", err
	}
	readOnly(dir)
	return dir, s.activate(id, commit)
}

// checkPlugin says why dir isn't plugin id: no manifest, another id, or too big.
func checkPlugin(dir, id string) error {
	m, err := plugin.LoadManifest(dir)
	if err != nil {
		return err
	}
	if m.ID != id {
		return fmt.Errorf("it's %s, not %s", m.ID, id)
	}
	n, err := plugin.Size(dir)
	if err != nil {
		return err
	}
	if n > plugin.MaxSize {
		return fmt.Errorf("%s is %s: a plugin may be at most %s", id, mb(n), mb(plugin.MaxSize))
	}
	return nil
}

// activate points id's current link at commit.
func (s Store) activate(id, commit string) error {
	link := filepath.Join(s.Dir, filepath.FromSlash(id), "current")
	if t, err := os.Readlink(link); err == nil && t == commit {
		return nil
	}
	tmp := link + ".new"
	os.Remove(tmp)
	if err := os.Symlink(commit, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, link)
}

// Remove deletes everything installed of id.
func (s Store) Remove(id string) error {
	dir := filepath.Join(s.Dir, filepath.FromSlash(id))
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			os.Chmod(p, 0o755)
		}
		return nil
	})
	if err := os.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	owner := filepath.Dir(dir)
	if es, err := os.ReadDir(owner); err == nil && len(es) == 0 {
		os.Remove(owner)
	}
	return nil
}

// readOnly takes write permission off everything under dir.
func readOnly(dir string) {
	var dirs []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if i, err := d.Info(); err == nil && i.Mode().IsRegular() {
			os.Chmod(p, i.Mode().Perm()&^0o222)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		os.Chmod(dirs[i], 0o555)
	}
}

func isCommit(rev string) bool {
	if len(rev) != 40 {
		return false
	}
	return strings.Trim(rev, "0123456789abcdef") == ""
}
