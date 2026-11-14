package testrun

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
)

// PackageDirer is an Adapter that knows the directory a package of its is in, for locations printed relative to it.
type PackageDirer interface {
	PackageDir(ws Workspace, pkg string) (string, bool)
}

// skipDirs are directories a scan doesn't go into, besides hidden ones.
var skipDirs = map[string]bool{"vendor": true, "node_modules": true, "testdata": true, "target": true, "__pycache__": true}

// Scan finds every test in the workspace: each file an adapter of it takes for a test file (TestFiler), read from
// disk and discovered as a buffer would be, on a worker for every other CPU.
func (s *Set) Scan(ctx context.Context, ws Workspace) []Item {
	as := s.Detect(ws)
	if len(as) == 0 {
		return nil
	}
	langs := s.langs()
	paths := make(chan string)
	var (
		mu  sync.Mutex
		out []Item
		wg  sync.WaitGroup
	)
	for range max(1, runtime.GOMAXPROCS(0)/2) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ps := map[*syntax.Language]*syntax.Parser{}
			defer func() {
				for _, p := range ps {
					p.Release()
				}
			}()
			for path := range paths {
				items := scanFile(path, as, ps, langs)
				mu.Lock()
				out = append(out, items...)
				mu.Unlock()
			}
		}()
	}
	filepath.WalkDir(ws.Root, func(path string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path != ws.Root && (strings.HasPrefix(d.Name(), ".") || skipDirs[d.Name()]) {
				return filepath.SkipDir
			}
			return nil
		}
		paths <- path
		return nil
	})
	close(paths)
	wg.Wait()
	return out
}

// ScanFile is the tests in one file on disk, found by the first of the workspace's adapters that takes it for a
// test file; none when it's gone.
func (s *Set) ScanFile(ws Workspace, path string) []Item {
	return scanFile(path, s.Detect(ws), nil, s.langs())
}

// scanFile discovers path's tests, in its language among langs, parsing it with the parsers in ps, borrowed as
// they're needed, when ps isn't nil.
func scanFile(path string, as []Adapter, ps map[*syntax.Language]*syntax.Parser, langs *syntax.Set) []Item {
	for _, a := range as {
		if f, ok := a.(TestFiler); !ok || !f.TestFile(path) {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := Source{Path: path, Text: text.New(b, text.Author{}).Snapshot(), Languages: langs}
		if l, ok := langs.ForFile(path, firstLine(src.Text)); ok && ps != nil {
			p := ps[l]
			if p == nil {
				if p, err = syntax.Borrow(l); err != nil {
					return nil
				}
				ps[l] = p
			}
			p.ParseSnapshot(src.Text)
			src.Syntax = p
		}
		return a.Discover(src)
	}
	return nil
}
