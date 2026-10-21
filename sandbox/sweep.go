package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// HomeCaches are the tool caches under a sandbox's home that Clean removes.
var HomeCaches = []string{".cache", ".npm", ".cargo/registry", ".cargo/git", ".gradle/caches", ".m2/repository", "Library/Caches"}

// DefaultGoCacheMax is the Go build cache's cap when sandbox.go_cache_max is empty.
const DefaultGoCacheMax int64 = 20 << 30

// EmptyTmp removes everything in root's temp dir, keeping the dir.
func EmptyTmp(root string) error {
	return emptyDir(TmpDir(root))
}

// Clean empties root's temp dir and removes its home's caches. The record, grants, Claude config and the rest of
// home stay, so the session reopens as before.
func Clean(root string) error {
	errs := []error{EmptyTmp(root)}
	for _, c := range HomeCaches {
		errs = append(errs, os.RemoveAll(filepath.Join(HomeDir(root), c)))
	}
	return errors.Join(errs...)
}

func emptyDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, e := range entries {
		errs = append(errs, os.RemoveAll(filepath.Join(dir, e.Name())))
	}
	return errors.Join(errs...)
}

// Sweep removes the sandbox dirs whose workspace is gone and that keep doesn't claim, then trims the Go build cache
// to cfg's cap. It returns what it removed.
func Sweep(keep func(root string) bool, cfg Config) []string {
	var out []string
	entries, _ := os.ReadDir(Base())
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(Base(), e.Name())
		root, ok := recordRoot(dir)
		if !ok || Key(root) != e.Name() || keep(root) {
			continue
		}
		if _, err := os.Stat(root); !errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if os.RemoveAll(dir) == nil {
			out = append(out, dir)
		}
	}
	if max, err := GoCacheMax(cfg); err == nil && max >= 0 {
		if n, freed := TrimCache(filepath.Join(GoCache(), "build"), max); n > 0 {
			out = append(out, fmt.Sprintf("%d Go build cache files (%d MB)", n, freed>>20))
		}
	}
	return out
}

// recordRoot is the workspace root dir's record names.
func recordRoot(dir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "record.json"))
	if err != nil {
		return "", false
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.Root == "" {
		return "", false
	}
	return r.Root, true
}

// GoCacheMax is cfg's cap on the Go build cache in bytes; -1 for no cap.
func GoCacheMax(cfg Config) (int64, error) {
	switch cfg.GoCacheMax {
	case "":
		return DefaultGoCacheMax, nil
	case "none":
		return -1, nil
	}
	return ParseSize(cfg.GoCacheMax)
}

// ParseSize reads a size in docker's units: "20g", "512m", "1024".
func ParseSize(s string) (int64, error) {
	num, mult := s, int64(1)
	if n := len(s); n > 0 {
		switch strings.ToLower(s[n-1:]) {
		case "b":
			num = s[:n-1]
		case "k":
			num, mult = s[:n-1], 1<<10
		case "m":
			num, mult = s[:n-1], 1<<20
		case "g":
			num, mult = s[:n-1], 1<<30
		}
	}
	v, err := strconv.ParseInt(num, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%q isn't a size", s)
	}
	return v * mult, nil
}

// TrimCache removes the least recently used entries of the Go build cache at dir until it holds at most max bytes.
// Go marks an entry used by its mtime. It returns how many files it removed and their size.
func TrimCache(dir string, max int64) (int, int64) {
	type entry struct {
		path string
		size int64
		mod  time.Time
	}
	var entries []entry
	var total int64
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !(strings.HasSuffix(p, "-a") || strings.HasSuffix(p, "-d")) {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		entries = append(entries, entry{p, info.Size(), info.ModTime()})
		total += info.Size()
		return nil
	})
	if total <= max {
		return 0, 0
	}
	slices.SortFunc(entries, func(a, b entry) int { return a.mod.Compare(b.mod) })
	var n int
	var freed int64
	for _, e := range entries {
		if total <= max {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
			freed += e.size
			n++
		}
	}
	return n, freed
}
