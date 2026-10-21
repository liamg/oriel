package gitx

import (
	"bytes"
	"cmp"
	"context"
	"errors"
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
	Head     string // HEAD's hash; "" before the first commit
	Upstream string
	Ahead    int
	Behind   int
	files    [3][]FileState
	byPath   map[string]FileState // what File answers
	ignored  map[string]bool      // All's ignored paths, directories with their "/"
}

// sameRefs reports whether two readings agree on everything but their files.
func (s *State) sameRefs(o *State) bool {
	return s.Branch == o.Branch && s.Base == o.Base && s.BaseHash == o.BaseHash && s.Head == o.Head && s.Upstream == o.Upstream &&
		s.Ahead == o.Ahead && s.Behind == o.Behind
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
	if s == nil {
		return FileState{}, false
	}
	f, ok := s.byPath[path]
	return f, ok
}

// index fills byPath: a path's first state in Changed, else its untracked one.
func (s *State) index() {
	s.byPath = map[string]FileState{}
	add := func(f FileState) {
		if _, ok := s.byPath[f.Path]; !ok {
			s.byPath[f.Path] = f
		}
	}
	for _, f := range s.files[Changed] {
		add(f)
	}
	for _, f := range s.files[Uncommitted] {
		if f.Area == Untracked {
			add(f)
		}
	}
	s.ignored = map[string]bool{}
	for _, f := range s.files[All] {
		if f.Ignored {
			s.ignored[f.Path] = true
		}
	}
}

// Ignored says git ignores path: it, or a directory it's in, is listed in All as ignored.
func (s *State) Ignored(path string) bool {
	if s == nil || len(s.ignored) == 0 {
		return false
	}
	if s.ignored[path] {
		return true
	}
	for i := range len(path) {
		if path[i] == '/' && s.ignored[path[:i+1]] {
			return true
		}
	}
	return false
}

// BaseRef is the branch Changed compares with: the one SetBase named for this worktree, else origin's default branch,
// else main or master if they're not the branch checked out.
func (r Repo) BaseRef(ctx context.Context, branch string) string {
	if ref := r.setBase(ctx); ref != "" {
		return ref
	}
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

type mergeBaseKey struct{ dir, head, refs string }

type mergeBase struct {
	ref, full, short string
}

var mergeBases = struct {
	sync.Mutex
	by map[mergeBaseKey]mergeBase
}{by: map[mergeBaseKey]mergeBase{}}

// mergeBase is where HEAD left base: of origin's base and the local branch of its name, the later merge-base, so
// a stale origin doesn't put years of others' work in Changed. It's worked out again only when HEAD or either
// branch moves.
func (r Repo) mergeBase(ctx context.Context, head, base, branch string) (mergeBase, bool) {
	refs := []string{base}
	if local, ok := strings.CutPrefix(base, "origin/"); ok && local != branch {
		refs = append(refs, local)
	}
	var full []string
	for _, ref := range refs {
		full = append(full, "refs/remotes/"+ref, "refs/heads/"+ref)
	}
	out, err := r.git(ctx, append([]string{"for-each-ref", "--format=%(refname) %(objectname)"}, full...)...)
	if err != nil {
		return mergeBase{}, false
	}
	k := mergeBaseKey{dir: r.Dir, head: head, refs: string(out)}
	mergeBases.Lock()
	mb, ok := mergeBases.by[k]
	mergeBases.Unlock()
	if ok {
		return mb, mb.full != ""
	}
	for _, ref := range refs {
		b, err := r.git(ctx, "merge-base", "HEAD", ref)
		if err != nil {
			continue
		}
		next := mergeBase{ref: ref, full: strings.TrimSpace(string(b))}
		if mb.full != "" {
			if _, err := r.git(ctx, "merge-base", "--is-ancestor", next.full, mb.full); err == nil {
				continue // no later than the one already found
			}
		}
		mb = next
	}
	if mb.full != "" {
		if b, err := r.git(ctx, "rev-parse", "--short", mb.full); err == nil {
			mb.short = strings.TrimSpace(string(b))
		}
	}
	mergeBases.Lock()
	if len(mergeBases.by) > 256 {
		clear(mergeBases.by)
	}
	mergeBases.by[k] = mb
	mergeBases.Unlock()
	return mb, mb.full != ""
}

// Status reads the work tree's status in every scope.
func (r Repo) Status(ctx context.Context) (*State, error) {
	st := &State{}
	// What doesn't need the base runs alongside working the base out, each its own git.
	var (
		wg                    sync.WaitGroup
		hasHead               bool
		base                  = emptyTree
		out, tracked, ign     []byte
		statusErr, trackedErr error
		ignErr                error
		staged, unstaged      numstats
	)
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	run(func() {
		st.Branch, _ = r.Branch(ctx)
		head, headErr := r.git(ctx, "rev-parse", "--verify", "-q", "HEAD")
		hasHead = headErr == nil
		if !hasHead {
			return
		}
		st.Head = strings.TrimSpace(string(head))
		var inner sync.WaitGroup
		inner.Add(1)
		go func() {
			defer inner.Done()
			if t, err := r.AheadBehind(ctx); err == nil {
				st.Upstream, st.Ahead, st.Behind = t.Upstream, t.Ahead, t.Behind
			}
		}()
		base = "HEAD"
		if st.Base = r.BaseRef(ctx, st.Branch); st.Base != "" {
			if mb, ok := r.mergeBase(ctx, st.Head, st.Base, st.Branch); ok {
				st.Base, base, st.BaseHash = mb.ref, mb.full, mb.short
			} else {
				st.Base = ""
			}
		}
		if base == "HEAD" {
			if out, err := r.git(ctx, "rev-parse", "--short", base); err == nil {
				st.BaseHash = strings.TrimSpace(string(out))
			}
		}
		inner.Wait()
	})
	run(func() { out, statusErr = r.git(ctx, "status", "--porcelain=v2", "-z", "--untracked-files=all") })
	run(func() { staged, _ = r.numstat(ctx, "diff", "--cached", "-M", "--numstat", "-z") })
	run(func() { unstaged, _ = r.numstat(ctx, "diff", "--numstat", "-z") })
	run(func() { tracked, trackedErr = r.git(ctx, "ls-files", "-z") })
	run(func() {
		ign, ignErr = r.git(ctx, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	})
	wg.Wait()
	if statusErr != nil {
		return nil, statusErr
	}

	// Uncommitted, from git status.
	entries := parseStatusV2(out)
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
		var (
			names, between  []byte
			err, betweenErr error
			counts          numstats
		)
		run(func() { names, err = r.git(ctx, "diff", "-M", "--name-status", "-z", base) })
		run(func() { counts, _ = r.numstat(ctx, "diff", "-M", "--numstat", "-z", base) })
		if base != "HEAD" {
			run(func() { between, betweenErr = r.git(ctx, "diff", "-M", "--name-only", "-z", base, "HEAD") })
		}
		wg.Wait()
		if err != nil {
			return nil, err
		}
		committed := map[string]bool{}
		if base != "HEAD" && betweenErr == nil {
			for _, p := range strings.Split(string(between), "\x00") {
				committed[p] = p != ""
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
	if trackedErr == nil {
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
	if ignErr == nil {
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
	st.index()
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
	repo    Repo
	reading sync.Mutex // held through a refresh
	mu      sync.Mutex
	st      *State
	seen    bool
	subs    map[int]func(paths []string)
	next    int
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

// Refresh reads the status again. Refreshes run one at a time, so a reading never replaces a later one.
func (s *Status) Refresh(ctx context.Context) error {
	s.reading.Lock()
	defer s.reading.Unlock()
	st, err := s.repo.Status(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	last, first := s.st, !s.seen
	s.mu.Unlock()
	changed := diffStates(last, st) // refreshes are one at a time, so last is still the one kept
	s.mu.Lock()
	same := !first && len(changed) == 0 && last.sameRefs(st)
	if !same {
		s.st = st // a reading that changed nothing keeps the last, so what's cached on it stays good
	}
	s.seen = true
	var fns []func([]string)
	if !same {
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
		was := make(map[string][]FileState, len(a.Files(sc)))
		for _, f := range a.Files(sc) {
			was[f.Path] = append(was[f.Path], f)
		}
		now := make(map[string][]FileState, len(b.Files(sc)))
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

// SetBase names the branch this worktree's Changed compares with ("" to go back to the default): a pull request's
// target, say. It's kept in the worktree's own git dir.
func (r Repo) SetBase(ctx context.Context, ref string) error {
	p, err := r.basePath(ctx)
	if err != nil {
		return err
	}
	if ref == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	return os.WriteFile(p, []byte(ref+"\n"), 0o644)
}

func (r Repo) setBase(ctx context.Context) string {
	p, err := r.basePath(ctx)
	if err != nil {
		return ""
	}
	b, _ := os.ReadFile(p)
	return strings.TrimSpace(string(b))
}

func (r Repo) basePath(ctx context.Context) (string, error) {
	out, err := r.git(ctx, "rev-parse", "--path-format=absolute", "--git-path", "oriel-base")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
