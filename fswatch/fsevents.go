package fswatch

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

// treeBackend watches a whole tree with one OS handle (FSEvents), rather than one per folder and file.
type treeBackend interface {
	// watchTree reports changes under root, as the OS spells their paths, to send until stop is called. exclude are
	// folders the OS may leave out. send is called from its own goroutine, never while stop runs.
	watchTree(root string, exclude []string, send func([]fsevent)) (stop func(), err error)
}

// fsevent is one FSEvents event: a path and its kFSEventStreamEventFlag bits.
type fsevent struct {
	path  string
	flags uint32
}

// FSEventStreamEventFlags, from CoreServices' FSEvents.h.
const (
	evMustScanSubDirs uint32 = 0x00000001
	evUserDropped     uint32 = 0x00000002
	evKernelDropped   uint32 = 0x00000004
	evItemCreated     uint32 = 0x00000100
	evItemRemoved     uint32 = 0x00000200
	evItemRenamed     uint32 = 0x00000800
	evItemModified    uint32 = 0x00001000
	evItemIsFile      uint32 = 0x00010000
	evItemIsDir       uint32 = 0x00020000
)

// maxExclude is how many folders FSEventStreamSetExclusionPaths takes.
const maxExclude = 8

// stream is a tree root watched by a treeBackend.
type stream struct {
	real    string    // the root with symlinks resolved, as the OS reports it
	since   time.Time // when it began: FSEvents still reports changes made just before
	stop    func()
	stopped bool
}

// treePath is the OS's path p under real, spelled under root instead.
func treePath(real, root, p string) (string, bool) {
	if len(p) > 1 {
		p = strings.TrimSuffix(p, "/")
	}
	base := strings.TrimSuffix(real, "/")
	switch {
	case p == real:
		return root, true
	case len(p) > len(base) && p[len(base)] == '/' && p[:len(base)] == base:
	case len(p) > len(base) && p[len(base)] == '/' && strings.EqualFold(p[:len(base)], base):
		// macOS folders are usually case-insensitive.
	case strings.EqualFold(p, real):
		return root, true
	default:
		return "", false
	}
	return filepath.Join(root, filepath.FromSlash(p[len(base):])), true
}

// excludes finds the shallowest folders skip leaves out under root, at most max, spelled under real. It reads at
// most a few thousand folders: the rest are filtered as their events arrive.
func excludes(root, real string, skip func(string) bool, max int) []string {
	var out []string
	queue := []string{""}
	for reads := 0; len(queue) > 0 && len(out) < max && reads < 4096; reads++ {
		dir := queue[0]
		queue = queue[1:]
		ents, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			continue
		}
		for _, e := range ents {
			if !e.IsDir() {
				continue
			}
			r := e.Name()
			if dir != "" {
				r = dir + "/" + r
			}
			if skip(r) {
				out = append(out, filepath.Join(real, filepath.FromSlash(r)))
				if len(out) == max {
					break
				}
				continue
			}
			queue = append(queue, r)
		}
	}
	return out
}
