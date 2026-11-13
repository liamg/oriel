// Package search is project search and replace: ripgrep when it's installed, streamed as it finds, and the same
// PCRE2 matching in Go when it isn't. It runs where the files are, in the app's process.
package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/internal/fsx"
	"github.com/liamg/oriel/regex"
)

// Query is a search: a PCRE2 pattern over the files under Dir that git (or .gitignore) doesn't ignore.
type Query struct {
	Pattern    string
	Dir        string
	IgnoreCase bool
	Globs      []string // only files matching one of these, by base name or path: "*.go"
	Exclude    []string // paths relative to Dir to leave out: generated and vendored files
	Max        int      // stop after this many matches; 10000 if 0
}

// Match is a line with one or more matches.
type Match struct {
	Path  string   // relative to the query's Dir, with forward slashes
	Line  int      // 1-based
	Text  string   // the line, without its ending
	Spans [][2]int // the matches' byte ranges in Text
}

// Engine is what searches: ripgrep, Go, or ripgrep when it's there.
type Engine int

const (
	Auto Engine = iota
	Rg
	Go
)

const maxMatches = 10000

// Tools are the search tools on the machine, each found the first time it's asked for.
type Tools struct {
	ripgrep func() (string, bool)
}

// NewTools finds the tools on PATH as it is when each is first asked for.
func NewTools() *Tools {
	return &Tools{ripgrep: sync.OnceValues(func() (string, bool) {
		p, err := exec.LookPath("rg")
		if err != nil {
			return "", false
		}
		out, err := exec.Command(p, "--pcre2-version").Output()
		return p, err == nil && bytes.Contains(out, []byte("PCRE2"))
	})}
}

// Ripgrep is rg's path, if it's installed with PCRE2.
func (t *Tools) Ripgrep() (string, bool) { return t.ripgrep() }

// Run streams q's matches to emit, in batches, until it's done or ctx is.
func (t *Tools) Run(ctx context.Context, q Query, emit func([]Match)) error {
	return t.run(ctx, q, Auto, emit)
}

func (t *Tools) run(ctx context.Context, q Query, e Engine, emit func([]Match)) error {
	re, err := regex.Compile(q.Pattern, regex.Flags{Caseless: q.IgnoreCase})
	if err != nil {
		return err
	}
	if q.Max == 0 {
		q.Max = maxMatches
	}
	if _, ok := t.Ripgrep(); e == Rg || e == Auto && ok {
		return t.runRg(ctx, q, emit)
	}
	return runGo(ctx, q, re, emit)
}

func (t *Tools) runRg(ctx context.Context, q Query, emit func([]Match)) error {
	rg, ok := t.Ripgrep()
	if !ok {
		return errors.New("rg isn't installed")
	}
	args := []string{"--json", "--no-config", "-P", "--max-columns", "2000", "--no-messages"}
	if q.IgnoreCase {
		args = append(args, "-i")
	}
	for _, g := range q.Globs {
		args = append(args, "--glob", g)
	}
	if len(q.Exclude) > 0 {
		f, err := os.CreateTemp("", "oriel-search-*.ignore")
		if err != nil {
			return err
		}
		defer os.Remove(f.Name())
		for _, x := range q.Exclude {
			fmt.Fprintf(f, "/%s\n", gitignoreEscape(x))
		}
		f.Close()
		args = append(args, "--ignore-file", f.Name())
	}
	args = append(args, "-e", q.Pattern, "--", ".")
	cmd := exec.CommandContext(ctx, rg, args...)
	cmd.Dir = q.Dir
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	sc.Buffer(make([]byte, 64<<10), 16<<20)
	var batch []Match
	n := 0
	for sc.Scan() {
		var ev struct {
			Type string `json:"type"`
			Data struct {
				Path       struct{ Text string }  `json:"path"`
				Lines      struct{ Text *string } `json:"lines"`
				LineNumber int                    `json:"line_number"`
				Submatches []struct {
					Start, End int
				} `json:"submatches"`
			} `json:"data"`
		}
		if json.Unmarshal(sc.Bytes(), &ev) != nil || ev.Type != "match" || ev.Data.Lines.Text == nil {
			continue
		}
		m := Match{Path: filepath.ToSlash(strings.TrimPrefix(ev.Data.Path.Text, "./")), Line: ev.Data.LineNumber,
			Text: strings.TrimRight(*ev.Data.Lines.Text, "\r\n")}
		for _, s := range ev.Data.Submatches {
			m.Spans = append(m.Spans, [2]int{s.Start, s.End})
		}
		batch = append(batch, m)
		n++
		if len(batch) >= 64 {
			emit(batch)
			batch = nil
		}
		if n >= q.Max {
			cmd.Process.Kill()
			break
		}
	}
	if len(batch) > 0 {
		emit(batch)
	}
	err = cmd.Wait()
	if ctx.Err() != nil || n >= q.Max {
		return ctx.Err()
	}
	if ee, ok := err.(*exec.ExitError); ok && ee.ExitCode() == 1 {
		return nil // no matches
	}
	if err != nil {
		return fmt.Errorf("rg: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// gitignoreEscape writes a path as a .gitignore pattern that matches only it.
func gitignoreEscape(p string) string {
	var b strings.Builder
	for i, r := range p {
		if strings.ContainsRune(`*?[\!#`, r) || r == ' ' && i == len(p)-1 {
			b.WriteByte('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// files are the files under dir git doesn't ignore, or every file outside dot-directories when it isn't a repo.
func files(ctx context.Context, dir string) []string {
	if root, ok := gitx.Root(ctx, dir); ok && filepath.Clean(root) == filepath.Clean(dir) {
		if fs, err := (gitx.Repo{Dir: dir}).Files(ctx); err == nil {
			return fs
		}
	}
	var out []string
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if rel, err := filepath.Rel(dir, p); err == nil {
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	})
	return out
}

func globbed(path string, globs []string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := filepath.Match(g, filepath.Base(path)); ok {
			return true
		}
		if ok, _ := filepath.Match(g, path); ok {
			return true
		}
	}
	return false
}

func runGo(ctx context.Context, q Query, re *regex.Regex, emit func([]Match)) error {
	n := 0
	for _, f := range files(ctx, q.Dir) {
		if ctx.Err() != nil || n >= q.Max {
			return ctx.Err()
		}
		if slices.Contains(q.Exclude, f) || !globbed(f, q.Globs) {
			continue
		}
		b, err := os.ReadFile(filepath.Join(q.Dir, f))
		if err != nil || bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
			continue
		}
		var batch []Match
		for i, line := range gitx.SplitLines(b) {
			line = strings.TrimRight(line, "\r\n")
			locs, err := re.FindAllIndex(line, -1)
			if err != nil || len(locs) == 0 {
				continue
			}
			m := Match{Path: f, Line: i + 1, Text: line}
			for _, l := range locs {
				if l[1] > l[0] {
					m.Spans = append(m.Spans, [2]int{l[0], l[1]})
				}
			}
			if len(m.Spans) == 0 {
				continue
			}
			batch = append(batch, m)
			if n++; n >= q.Max {
				break
			}
		}
		if len(batch) > 0 {
			emit(batch)
		}
	}
	return nil
}

// FileEdit is a planned replace in one file: its text before and after, and the hunks between them.
type FileEdit struct {
	Path     string
	Old, New []byte
	Hunks    []gitx.Hunk
	Count    int // matches replaced
}

// Plan works out a replace of q's matches with repl, read as PCRE2's substitute reads it ($1, ${name}, $$), in each
// file a match is in. read gives a file's current text (an open buffer's), or nil to read it from disk.
func Plan(q Query, repl string, matches []Match, read func(path string) ([]byte, bool)) ([]FileEdit, error) {
	re, err := regex.Compile(q.Pattern, regex.Flags{Caseless: q.IgnoreCase})
	if err != nil {
		return nil, err
	}
	byFile := map[string]map[int]bool{}
	var order []string
	for _, m := range matches {
		if byFile[m.Path] == nil {
			byFile[m.Path] = map[int]bool{}
			order = append(order, m.Path)
		}
		byFile[m.Path][m.Line] = true
	}
	var out []FileEdit
	for _, path := range order {
		var old []byte
		ok := false
		if read != nil {
			old, ok = read(path)
		}
		if !ok {
			if old, err = fsx.ReadFile(filepath.Join(q.Dir, path)); err != nil {
				return nil, err
			}
		}
		lines := gitx.SplitLines(old)
		count := 0
		for i, l := range lines {
			if !byFile[path][i+1] {
				continue
			}
			body := strings.TrimRight(l, "\r\n")
			r, n, err := re.ReplaceAll(body, repl)
			if err != nil {
				return nil, err
			}
			lines[i], count = r+l[len(body):], count+n
		}
		nw := []byte(strings.Join(lines, ""))
		if count == 0 || bytes.Equal(nw, old) {
			continue
		}
		out = append(out, FileEdit{Path: path, Old: old, New: nw, Hunks: gitx.DiffLines(old, nw), Count: count})
	}
	return out, nil
}

// Write puts the edit's new text in its file under dir, if the file still holds the text it was planned on.
func (f FileEdit) Write(dir string) error {
	p := filepath.Join(dir, f.Path)
	cur, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if !bytes.Equal(cur, f.Old) {
		return fmt.Errorf("%s changed since the search; search again", f.Path)
	}
	return atomicfile.Write(p, f.New, 0o644) // a file already there keeps its mode
}
