package apptest

import (
	"maps"
	"path/filepath"
	"slices"
	"testing"

	"github.com/liamg/oriel/app/apptest/rig"
)

// FilesChanged tells the app's file watchers the paths changed, as the OS would once they'd settled, and returns
// once every watcher has taken them in. The scene's loop runs meanwhile: a watcher that hands its events to the loop
// takes them in there. The scene needs ScriptedFiles.
func (s *Scene) FilesChanged(paths ...string) {
	s.T.Helper()
	if s.files == nil {
		s.T.Fatal("FilesChanged needs a scene with ScriptedFiles")
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		s.files.Change(paths...)
	}()
	s.Until("the file watchers taking the change in", func() bool {
		s.RunQueued()
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
}

// WriteFiles writes files under dir, by slash path, and tells the app's file watchers (FilesChanged).
func (s *Scene) WriteFiles(dir string, files map[string]string) {
	s.T.Helper()
	t, ok := s.T.(*testing.T)
	if !ok {
		s.T.Fatal("WriteFiles needs a *testing.T")
	}
	rig.WriteFiles(t, dir, files)
	var paths []string
	for _, name := range slices.Sorted(maps.Keys(files)) {
		paths = append(paths, filepath.Join(dir, filepath.FromSlash(name)))
	}
	s.FilesChanged(paths...)
}
