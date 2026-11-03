// Package leakcheck finds private names in tracked files: work paths, user homes, company and customer names.
// The names themselves come from outside the repo (ORIEL_LEAK_DENYLIST), so the public tree doesn't list them.
package leakcheck

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Builtin terms are always denied.
var Builtin = []string{".crit/work"}

// Hit is one denied term found in a file.
type Hit struct {
	File string
	Line int
	Term string
}

func (h Hit) String() string { return fmt.Sprintf("%s:%d: %q", h.File, h.Line, h.Term) }

// Terms parses a denylist: one term per line, matched case-insensitively; blank lines and # comments are skipped.
func Terms(list string) []string {
	var out []string
	for l := range strings.Lines(list) {
		l = strings.TrimSpace(l)
		if l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, strings.ToLower(l))
		}
	}
	return out
}

// Files lists root's tracked files, without vendor/ and this package, which names the built-in terms.
func Files(root string) ([]string, error) {
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	var files []string
	for f := range strings.SplitSeq(strings.TrimRight(string(out), "\x00"), "\x00") {
		if f != "" && !strings.HasPrefix(f, "vendor/") && !strings.HasPrefix(f, "tools/leakcheck/") {
			files = append(files, f)
		}
	}
	return files, nil
}

// Scan reports every line of files (relative to root) holding one of terms. Binary files are skipped.
func Scan(root string, files, terms []string) ([]Hit, error) {
	var hits []Hit
	for _, f := range files {
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, err
		}
		if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(nil, len(b)+1)
		for n := 1; sc.Scan(); n++ {
			l := strings.ToLower(sc.Text())
			for _, t := range terms {
				if strings.Contains(l, t) {
					hits = append(hits, Hit{File: f, Line: n, Term: t})
				}
			}
		}
	}
	return hits, nil
}
