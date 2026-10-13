package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"time"
)

const maxRecents = 12

// recents are the directories opened with Open Directory (or oriel <dir>), newest first, kept in the state directory.
type recents struct {
	path string
}

type recentDir struct {
	Path string    `json:"path"`
	At   time.Time `json:"at"`
}

type recentFile struct {
	Directories []recentDir `json:"directories"`
}

func (r *recents) list() []recentDir {
	if r == nil {
		return nil
	}
	var f recentFile
	if b, err := os.ReadFile(r.path); err == nil {
		json.Unmarshal(b, &f)
	}
	return f.Directories
}

func (r *recents) add(dir string, at time.Time) error {
	ds := slices.DeleteFunc(r.list(), func(d recentDir) bool { return d.Path == dir })
	ds = append([]recentDir{{Path: dir, At: at}}, ds...)
	return r.save(ds[:min(len(ds), maxRecents)])
}

func (r *recents) forget(dir string) error {
	if r == nil {
		return nil
	}
	return r.save(slices.DeleteFunc(r.list(), func(d recentDir) bool { return d.Path == dir }))
}

func (r *recents) save(ds []recentDir) error {
	b, err := json.MarshalIndent(recentFile{Directories: ds}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(r.path), 0o755); err != nil {
		return err
	}
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, r.path)
}
