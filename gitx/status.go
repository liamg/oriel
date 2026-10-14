package gitx

import (
	"bytes"
	"cmp"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Scope is which changes a list of files is about (ui.md 01c).
type Scope int

const (
	Changed     Scope = iota // the branch against its base, uncommitted work included
	Uncommitted              // the work tree against HEAD: staged, unstaged and untracked
	All                      // every file, marked as Changed marks it
)

func (s Scope) String() string { return [...]string{"changed", "uncommitted", "all"}[s] }

// Area is where a file's change is: for Changed and All, committed on the branch, still uncommitted, or both; for
// Uncommitted, staged, unstaged or untracked.
type Area int

const (
	NoArea Area = iota
	Committed
	Working
	Both
	Staged
	Unstaged
	Untracked
)

func (a Area) String() string {
	return [...]string{"", "committed", "uncommitted", "both", "staged", "unstaged", "untracked"}[a]
}

// FileState is a file's change in a scope. Code is git's status letter (M A D R ? U), or 0 for a clean file.
type FileState struct {
	Path     string
	Old      string // a rename's old path
	Code     byte
	Add, Del int // lines
	Binary   bool
	Area     Area
	Ignored  bool // an ignored directory or file, listed in All without its contents
}

// emptyTree is git's hash of the empty tree: the base of a repo with no commits.
const emptyTree = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// State is one reading of a work tree's status.
type State struct {
	Branch   string
	Base     string // the branch Changed compares with: origin's default, main or master; "" for none
	BaseHash string // the merge-base with it, abbreviated
	Upstream string
	Ahead    int
	Behind   int
	files    [3][]FileState
}

// Files are the files of a scope, by path. Uncommitted lists a file once per area it has changes in.
func (s *State) Files(sc Scope) []FileState {
	if s == nil {
		return nil
	}
	return s.files[sc]
}

// Count is how many files a scope lists, for its segment: All counts every file.
func (s *State) Count(sc Scope) int {
	n := 0
	for _, f := range s.Files(sc) {
		if !f.Ignored {
			n++
		}
	}
	return n
}

// File is a path's state in Changed, and whether it has one.
func (s *State) File(path string) (FileState, bool) {
	for _, f := range s.Files(Changed) {
		if f.Path == path {
			return f, true
		}
	}
	for _, f := range s.Files(Uncommitted) {
		if f.Path == path && f.Area == Untracked {
			return f, true
		}
	}
	return FileState{}, false
}

// BaseRef is the branch Changed compares with: origin's default branch, else main or master if they're not
// the branch checked out.
func (r Repo) BaseRef(ctx context.Context, branch string) string {
	if out, err := r.git(ctx, "rev-parse", "--abbrev-ref", "origin/HEAD"); err == nil {
		if ref := strings.TrimSpace(string(out)); ref != "" && ref != "origin/HEAD" {
			return ref
		}
	}
	for _, b := range []string{"main", "master"} {
		if b == branch {
			continue
		}
		if _, err := r.git(ctx, "rev-parse", "--verify", "-q", "refs/heads/"+b); err == nil {
			return b
		}
	}
	return ""
}

// Status reads the work tree's status in every scope.
func (r Repo) Status(ctx context.Context) (*State, error) {
	st := &State{}
	st.Branch, _ = r.Branch(ctx)
	_, headErr := r.git(ctx, "rev-parse", "--verify", "-q", "HEAD")
	hasHead := headErr == nil
	if t, err := r.AheadBehind(ctx); err == nil {
		st.Upstream, st.Ahead, st.Behind = t.Upstream, t.Ahead, t.Behind
	}
	base := emptyTree
	if hasHead {
		base = "HEAD"
		if st.Base = r.BaseRef(ctx, st.Branch); st.Base != "" {
			if out, err := r.git(ctx, "merge-base", "HEAD", st.Base); err == nil {
				base = strings.TrimSpace(string(out))
			} else {
				st.Base = ""
			}
		}
		if out, err := r.git(ctx, "rev-parse", "--short", base); err == nil {
			st.BaseHash = strings.TrimSpace(string(out))
		}
	}

	// Uncommitted, from git status.
	out, err := r.git(ctx, "status", "--porcelain=v2", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	entries := parseStatusV2(out)
	staged, _ := r.numstat(ctx, "diff", "--cached", "-M", "--numstat", "-z")
	unstaged, _ := r.numstat(ctx, "diff", "--numstat", "-z")
	var unc []FileState
	working := map[string]bool{}
	for _, e := range entries {
		switch {
		case e.untracked:
			n, bin := countLines(filepath.Join(r.Dir, e.path))
			unc = append(unc, FileState{Path: e.path, Code: '?', Add: n, Binary: bin, Area: Untracked})
		case e.unmerged:
			unc = append(unc, FileState{Path: e.path, Code: 'U', Area: Unstaged})
			working[e.path] = true
		default:
			if e.x != '.' {
				f := FileState{Path: e.path, Old: e.old, Code: e.x, Area: Staged}
				staged.fill(&f)
				unc = append(unc, f)
				working[e.path] = true
			}
			if e.y != '.' {
				f := FileState{Path: e.path, Code: e.y, Area: Unstaged}
				unstaged.fill(&f)
				unc = append(unc, f)
				working[e.path] = true
			}
		}
		if e.old != "" {
			working[e.old] = true
		}
	}
	slices.SortStableFunc(unc, func(a, b FileState) int { return cmp.Or(cmp.Compare(a.Area, b.Area), strings.Compare(a.Path, b.Path)) })
	st.files[Uncommitted] = unc

	// Changed: the work tree against the merge-base.
	var changed []FileState
	if hasHead {
		names, err := r.git(ctx, "diff", "-M", "--name-status", "-z", base)
		if err != nil {
			return nil, err
		}
		counts, _ := r.numstat(ctx, "diff", "-M", "--numstat", "-z", base)
		committed := map[string]bool{}
		if base != "HEAD" {
			if out, err := r.git(ctx, "diff", "-M", "--name-only", "-z", base, "HEAD"); err == nil {
				for _, p := range strings.Split(string(out), "\x00") {
					committed[p] = p != ""
				}
			}
		}
		for _, f := range parseNameStatus(names) {
			counts.fill(&f)
			c, w := committed[f.Path] || f.Old != "" && committed[f.Old], working[f.Path] || f.Old != "" && working[f.Old]
			switch {
			case c && w:
				f.Area = Both
			case c:
				f.Area = Committed
			default:
				f.Area = Working
			}
			changed = append(changed, f)
		}
		for _, f := range unc {
			if f.Code == 'U' {
				changed = append(changed, f)
			}
		}
	}
	slices.SortFunc(changed, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	changed = slices.CompactFunc(changed, func(a, b FileState) bool { return a.Path == b.Path })
	st.files[Changed] = changed

	// All: every file, with Changed's marks, the untracked ones, and ignored directories.
	all := map[string]FileState{}
	if tracked, err := r.git(ctx, "ls-files", "-z"); err == nil {
		for _, p := range strings.Split(string(tracked), "\x00") {
			if p != "" {
				all[p] = FileState{Path: p}
			}
		}
	}
	for _, f := range changed {
		if f.Old != "" {
			delete(all, f.Old)
		}
		all[f.Path] = f
	}
	for _, f := range unc {
		if f.Area == Untracked {
			all[f.Path] = f
		} else if f.Code == 'U' {
			all[f.Path] = f
		}
	}
	if ign, err := r.git(ctx, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory"); err == nil {
		for _, p := range strings.Split(string(ign), "\x00") {
			if p != "" {
				all[p] = FileState{Path: p, Ignored: true}
			}
		}
	}
	list := make([]FileState, 0, len(all))
	for _, f := range all {
		list = append(list, f)
	}
	slices.SortFunc(list, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	st.files[All] = list
	return st, nil
}

type statusEntry struct {
	x, y      byte
	path, old string
	untracked bool
	unmerged  bool
}

// parseStatusV2 reads git status --porcelain=v2 -z.
func parseStatusV2(out []byte) []statusEntry {
	var es []statusEntry
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		l := fields[i]
		if len(l) < 2 {
			continue
		}
		switch l[0] {
		case '1':
			if f := strings.SplitN(l, " ", 9); len(f) == 9 {
				es = append(es, statusEntry{x: f[1][0], y: f[1][1], path: f[8]})
			}
		case '2':
			if f := strings.SplitN(l, " ", 10); len(f) == 10 && i+1 < len(fields) {
				es = append(es, statusEntry{x: f[1][0], y: f[1][1], path: f[9], old: fields[i+1]})
				i++
			}
		case 'u':
			if f := strings.SplitN(l, " ", 11); len(f) == 11 {
				es = append(es, statusEntry{path: f[10], unmerged: true})
			}
		case '?':
			es = append(es, statusEntry{path: l[2:], untracked: true})
		}
	}
	return es
}

// parseNameStatus reads git diff --name-status -z: renames as R with their old path.
func parseNameStatus(out []byte) []FileState {
	var fs []FileState
	fields := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(fields); i++ {
		code := fields[i]
		if code == "" {
			continue
		}
		f := FileState{Code: code[0], Path: fields[i+1]}
		i++
		if (code[0] == 'R' || code[0] == 'C') && i+1 < len(fields) {
			f.Old, f.Path = f.Path, fields[i+1]
			i++
		}
		if f.Code == 'T' {
			f.Code = 'M'
		}
		fs = append(fs, f)
	}
	return fs
}

type lineCount struct {
	add, del int
	binary   bool
}

type numstats map[string]lineCount

func (n numstats) fill(f *FileState) {
	if c, ok := n[f.Path]; ok {
		f.Add, f.Del, f.Binary = c.add, c.del, c.binary
	}
}

// numstat runs a git diff --numstat -z and reads it by path; a rename is under its new path.
func (r Repo) numstat(ctx context.Context, args ...string) (numstats, error) {
	out, err := r.git(ctx, args...)
	if err != nil {
		return nil, err
	}
	n := numstats{}
	fields := strings.Split(string(out), "\x00")
	for i := 0; i < len(fields); i++ {
		f := strings.SplitN(fields[i], "\t", 3)
		if len(f) != 3 {
			continue
		}
		path := f[2]
		if path == "" && i+2 < len(fields) {
			path = fields[i+2] // a rename: "add\tdel\t", then old, then new
			i += 2
		}
		c := lineCount{binary: f[0] == "-"}
		c.add, _ = strconv.Atoi(f[0])
		c.del, _ = strconv.Atoi(f[1])
		n[path] = c
	}
	return n, nil
}

// countLines is how many lines a file has, as git diff would count them added, and whether it's binary.
func countLines(path string) (int, bool) {
	st, err := os.Stat(path)
	if err != nil || st.Size() > 8<<20 {
		return 0, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	if bytes.IndexByte(b[:min(len(b), 8000)], 0) >= 0 {
		return 0, true
	}
	n := bytes.Count(b, []byte{'\n'})
	if len(b) > 0 && b[len(b)-1] != '\n' {
		n++
	}
	return n, false
}

// Status is a work tree's status kept up to date: what explorer scopes and gutters read (ide.md). Refresh reads
// it again; watchers hear which paths' states changed.
type Status struct {
	repo Repo
	mu   sync.Mutex
	st   *State
	seen bool
	subs map[int]func(paths []string)
	next int
}

func NewStatus(r Repo) *Status { return &Status{repo: r, subs: map[int]func([]string){}} }

func (s *Status) Repo() Repo { return s.repo }

// State is the last reading, nil before the first.
func (s *Status) State() *State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

func (s *Status) Files(sc Scope) []FileState { return s.State().Files(sc) }

// Watch calls fn after each refresh that changed something, with the paths whose states changed (every path, the
// first time).
func (s *Status) Watch(fn func(paths []string)) (cancel func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := s.next
	s.next++
	s.subs[id] = fn
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.subs, id)
	}
}

// Refresh reads the status again.
func (s *Status) Refresh(ctx context.Context) error {
	st, err := s.repo.Status(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	changed := diffStates(s.st, st)
	first := !s.seen
	s.st, s.seen = st, true
	var fns []func([]string)
	if first || len(changed) > 0 || s.st.Branch != st.Branch {
		for _, fn := range s.subs {
			fns = append(fns, fn)
		}
	}
	s.mu.Unlock()
	for _, fn := range fns {
		fn(changed)
	}
	return nil
}

// diffStates are the paths whose state differs between two readings, in any scope.
func diffStates(a, b *State) []string {
	var out []string
	for sc := Changed; sc <= All; sc++ {
		was := map[string][]FileState{}
		for _, f := range a.Files(sc) {
			was[f.Path] = append(was[f.Path], f)
		}
		now := map[string][]FileState{}
		for _, f := range b.Files(sc) {
			now[f.Path] = append(now[f.Path], f)
		}
		for p, fs := range now {
			if !slices.Equal(fs, was[p]) {
				out = append(out, p)
			}
		}
		for p := range was {
			if _, ok := now[p]; !ok {
				out = append(out, p)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// Stage adds paths' work-tree state to the index, deletions included (git add -A).
func (r Repo) Stage(ctx context.Context, paths ...string) error {
	_, err := r.git(ctx, append([]string{"add", "-A", "--"}, paths...)...)
	return err
}

// Unstage puts paths' index entries back to HEAD's, leaving the work tree (git restore --staged).
func (r Repo) Unstage(ctx context.Context, paths ...string) error {
	if _, err := r.git(ctx, "rev-parse", "--verify", "-q", "HEAD"); err != nil {
		_, err = r.git(ctx, append([]string{"rm", "-q", "--cached", "--"}, paths...)...)
		return err
	}
	_, err := r.git(ctx, append([]string{"restore", "--staged", "--"}, paths...)...)
	return err
}

// Discard throws away paths' unstaged changes (git restore), and deletes the ones git doesn't track.
func (r Repo) Discard(ctx context.Context, paths ...string) error {
	var tracked []string
	for _, p := range paths {
		if _, err := r.git(ctx, "ls-files", "--error-unmatch", "--", p); err != nil {
			if err := os.Remove(filepath.Join(r.Dir, p)); err != nil {
				return err
			}
			continue
		}
		tracked = append(tracked, p)
	}
	if len(tracked) == 0 {
		return nil
	}
	_, err := r.git(ctx, append([]string{"restore", "--worktree", "--"}, tracked...)...)
	return err
}
