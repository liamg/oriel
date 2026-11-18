package shell

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/atomicfile"
)

const maxRecents = 12

// recents are the directories opened with Open Directory (or oriel <dir>), newest first, kept in the state directory.
type recents struct {
	path string

	mu   sync.Mutex
	read *recentRead // the file as last read or written
}

// recentRead is the recents file's directories, and the size and time it had then.
type recentRead struct {
	dirs []recentDir
	size int64
	mod  time.Time
}

type recentDir struct {
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

type recentFile struct {
	Directories []recentDir `json:"directories"`
}

// list is the recent directories, none when the file can't be read.
func (r *recents) list() []recentDir {
	ds, _ := r.load()
	return ds
}

// load is the recent directories, read again only when the file has changed; a file that's there but doesn't parse
// is an error, so nothing saves over it.
func (r *recents) load() ([]recentDir, error) {
	if r == nil {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	st, err := os.Stat(r.path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c := r.read; c != nil && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		return slices.Clone(c.dirs), nil
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		return nil, err
	}
	var f recentFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", r.path, err)
	}
	r.read = &recentRead{dirs: f.Directories, size: st.Size(), mod: st.ModTime()}
	return slices.Clone(f.Directories), nil
}

func (r *recents) add(dir string, at time.Time) error {
	ds, err := r.load()
	if err != nil {
		return err
	}
	ds = slices.DeleteFunc(ds, func(d recentDir) bool { return d.Path == dir })
	ds = append([]recentDir{{Path: dir, At: at}}, ds...)
	return r.save(ds[:min(len(ds), maxRecents)])
}

func (r *recents) forget(dir string) error {
	if r == nil {
		return nil
	}
	ds, err := r.load()
	if err != nil {
		return err
	}
	return r.save(slices.DeleteFunc(ds, func(d recentDir) bool { return d.Path == dir }))
}

func (r *recents) save(ds []recentDir) error {
	b, err := json.MarshalIndent(recentFile{Directories: ds}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	if err := atomicfile.Write(r.path, b, 0o644); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.read = nil
	return nil
}
