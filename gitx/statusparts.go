package gitx

import (
	"cmp"
	"context"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// A status is read in parts, each read again only when what it depends on changes: Refs when HEAD or a ref moves,
// the tracked files when the index changes, Work when any file does, and Lines only while something shows them.
// Compose puts them together into a State.

// Refs are the branch, its upstream and its base: what moves only with HEAD, the refs and the base set.
type Refs struct {
	Branch   string
	Head     string // "" before the first commit
	Upstream string
	Ahead    int
	Behind   int
	Base     string // the branch Changed compares with; "" for none
	BaseHash string // the merge-base with it, abbreviated
	Rev      string // what Changed diffs against: the merge-base, HEAD, or the empty tree before a commit
	// Committed are the paths that differ between Rev and HEAD: changed on the branch.
	Committed map[string]bool
}

// Same says two readings of Refs agree.
func (a Refs) Same(b Refs) bool {
	return a.Branch == b.Branch && a.Head == b.Head && a.Upstream == b.Upstream && a.Ahead == b.Ahead && a.Behind == b.Behind &&
		a.Base == b.Base && a.BaseHash == b.BaseHash && a.Rev == b.Rev && maps.Equal(a.Committed, b.Committed)
}

// ReadRefs reads the branch, its upstream and its base.
func (r Repo) ReadRefs(ctx context.Context) (Refs, error) {
	var rf Refs
	rf.Branch, _ = r.Branch(ctx)
	head, err := r.git(ctx, "rev-parse", "--verify", "-q", "HEAD")
	if err != nil {
		rf.Rev = emptyTree
		return rf, nil
	}
	rf.Head = strings.TrimSpace(string(head))
	var wg sync.WaitGroup
	wg.Go(func() {
		if t, err := r.AheadBehind(ctx); err == nil {
			rf.Upstream, rf.Ahead, rf.Behind = t.Upstream, t.Ahead, t.Behind
		}
	})
	rf.Rev = "HEAD"
	if rf.Base = r.BaseRef(ctx, rf.Branch); rf.Base != "" {
		if mb, ok := r.mergeBase(ctx, rf.Head, rf.Base, rf.Branch); ok {
			rf.Base, rf.Rev, rf.BaseHash = mb.ref, mb.full, mb.short
		} else {
			rf.Base = ""
		}
	}
	if rf.Rev == "HEAD" {
		if out, err := r.git(ctx, "rev-parse", "--short", "HEAD"); err == nil {
			rf.BaseHash = strings.TrimSpace(string(out))
		}
	} else if between, err := r.git(ctx, "diff", "-M", "--name-only", "-z", rf.Rev, "HEAD"); err == nil {
		rf.Committed = map[string]bool{}
		for _, p := range strings.Split(string(between), "\x00") {
			if p != "" {
				rf.Committed[p] = true
			}
		}
	}
	wg.Wait()
	return rf, nil
}

// ReadTracked lists the files git tracks.
func (r Repo) ReadTracked(ctx context.Context) ([]string, error) {
	out, err := r.git(ctx, "ls-files", "-z")
	if err != nil {
		return nil, err
	}
	var ps []string
	for _, p := range strings.Split(string(out), "\x00") {
		if p != "" {
			ps = append(ps, p)
		}
	}
	return ps, nil
}

// Work is the work tree against HEAD and against Refs.Rev: what changes with any file.
type Work struct {
	entries []statusEntry
	ignored []string    // ignored paths, folders with their "/", without what's in them
	names   []FileState // the work tree against Refs.Rev
	rev     string
}

// Same says two readings of Work agree.
func (a Work) Same(b Work) bool {
	return a.rev == b.rev && slices.Equal(a.entries, b.entries) && slices.Equal(a.ignored, b.ignored) && slices.Equal(a.names, b.names)
}

// Ignored says git ignores path, by this reading.
func (a Work) Ignored(path string) bool {
	if len(a.ignored) == 0 {
		return false
	}
	i, found := slices.BinarySearch(a.ignored, path)
	if found || i < len(a.ignored) && a.ignored[i] == path+"/" {
		return true
	}
	for j := range len(path) {
		if path[j] == '/' {
			if _, ok := slices.BinarySearch(a.ignored, path[:j+1]); ok {
				return true
			}
		}
	}
	return false
}

// ReadWork reads git status, with what's ignored, and the work tree against rf.Rev: two gits, side by side.
func (r Repo) ReadWork(ctx context.Context, rf Refs) (Work, error) {
	w := Work{rev: rf.Rev}
	var (
		wg         sync.WaitGroup
		out, names []byte
		err, nErr  error
	)
	wg.Go(func() {
		out, err = r.git(ctx, "status", "--porcelain=v2", "-z", "--untracked-files=all", "--ignored=matching")
	})
	if rf.Head != "" {
		wg.Go(func() { names, nErr = r.git(ctx, "diff", "-M", "--name-status", "-z", rf.Rev) })
	}
	wg.Wait()
	if err != nil {
		return Work{}, err
	}
	if nErr != nil {
		return Work{}, nErr
	}
	w.entries, w.ignored = parseStatusV2(out)
	slices.Sort(w.ignored)
	w.names = parseNameStatus(names)
	return w, nil
}

// Lines are how many lines each changed file adds and deletes, in each scope.
type Lines struct {
	staged, unstaged, base, head numstats
	untracked                    map[string]lineCount
}

// ReadLines counts the lines of what w says changed: four numstats, and the untracked files read.
func (r Repo) ReadLines(ctx context.Context, rf Refs, w Work) (Lines, error) {
	l := Lines{untracked: map[string]lineCount{}}
	var wg sync.WaitGroup
	wg.Go(func() { l.staged, _ = r.numstat(ctx, "diff", "--cached", "-M", "--numstat", "-z") })
	wg.Go(func() { l.unstaged, _ = r.numstat(ctx, "diff", "--numstat", "-z") })
	wg.Go(func() { l.head, _ = r.numstat(ctx, "diff", "-M", "--numstat", "-z", cmp.Or(rf.Head, emptyTree)) })
	if rf.Head != "" {
		wg.Go(func() { l.base, _ = r.numstat(ctx, "diff", "-M", "--numstat", "-z", rf.Rev) })
	}
	for _, e := range w.entries {
		if e.untracked {
			n, bin := countLines(filepath.Join(r.Dir, e.path))
			l.untracked[e.path] = lineCount{add: n, binary: bin}
		}
	}
	wg.Wait()
	return l, nil
}

// Compose puts a status together from its parts. lines may be nil: the files then have no line counts.
func Compose(rf Refs, tracked []string, w Work, lines *Lines) *State {
	st := &State{Branch: rf.Branch, Head: rf.Head, Upstream: rf.Upstream, Ahead: rf.Ahead, Behind: rf.Behind, Base: rf.Base,
		BaseHash: rf.BaseHash}
	var staged, unstaged, base, head numstats
	untracked := map[string]lineCount{}
	if lines != nil {
		staged, unstaged, base, head, untracked = lines.staged, lines.unstaged, lines.base, lines.head, lines.untracked
	}

	// Uncommitted, from git status.
	var unc, whole []FileState
	working := map[string]bool{}
	for _, e := range w.entries {
		switch {
		case e.untracked:
			c := untracked[e.path]
			unc = append(unc, FileState{Path: e.path, Code: '?', Add: c.add, Binary: c.binary, Area: Untracked})
			whole = append(whole, unc[len(unc)-1])
		case e.unmerged:
			unc = append(unc, FileState{Path: e.path, Code: 'U', Area: Unstaged})
			whole = append(whole, FileState{Path: e.path, Code: 'U', Area: Working})
			working[e.path] = true
		default:
			f := FileState{Path: e.path, Old: e.old, Code: againstHead(e.x, e.y), Area: Working}
			head.fill(&f)
			whole = append(whole, f)
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
	slices.SortFunc(whole, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	st.whole = whole

	// Changed: the work tree against the merge-base.
	var changed []FileState
	if rf.Head != "" && w.rev == rf.Rev {
		for _, f := range w.names {
			base.fill(&f)
			c, wk := rf.Committed[f.Path] || f.Old != "" && rf.Committed[f.Old], working[f.Path] || f.Old != "" && working[f.Old]
			switch {
			case c && wk:
				f.Area = Both
			case c:
				f.Area = Committed
			default:
				f.Area = Working
			}
			changed = append(changed, f)
		}
		for _, f := range unc {
			if f.Code == 'U' || f.Area == Untracked {
				changed = append(changed, f)
			}
		}
	}
	slices.SortFunc(changed, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	changed = slices.CompactFunc(changed, func(a, b FileState) bool { return a.Path == b.Path })
	st.files[Changed] = changed

	// All: every file, with Changed's marks, the untracked ones, and ignored folders.
	all := make(map[string]FileState, len(tracked))
	for _, p := range tracked {
		all[p] = FileState{Path: p}
	}
	for _, f := range changed {
		if f.Old != "" {
			delete(all, f.Old)
		}
		all[f.Path] = f
	}
	for _, f := range unc {
		if f.Area == Untracked || f.Code == 'U' {
			all[f.Path] = f
		}
	}
	for _, p := range w.ignored {
		all[p] = FileState{Path: p, Ignored: true}
	}
	list := make([]FileState, 0, len(all))
	for _, f := range all {
		list = append(list, f)
	}
	slices.SortFunc(list, func(a, b FileState) int { return strings.Compare(a.Path, b.Path) })
	st.files[All] = list
	st.index()
	return st
}

// Status reads the work tree's status in every scope, line counts included: every part at once.
func (r Repo) Status(ctx context.Context) (*State, error) {
	rf, err := r.ReadRefs(ctx)
	if err != nil {
		return nil, err
	}
	var (
		wg      sync.WaitGroup
		tracked []string
		w       Work
		wErr    error
	)
	wg.Go(func() { tracked, _ = r.ReadTracked(ctx) })
	wg.Go(func() { w, wErr = r.ReadWork(ctx, rf) })
	wg.Wait()
	if wErr != nil {
		return nil, wErr
	}
	lines, _ := r.ReadLines(ctx, rf, w)
	return Compose(rf, tracked, w, &lines), nil
}

// WithCounts is s with the line counts c has for the same changes: a status read since the counts were.
func (s *State) WithCounts(c *State) *State {
	if s == nil || c == nil {
		return s
	}
	type key struct {
		path string
		area Area
		code byte
	}
	counts := map[key]FileState{}
	for sc := Changed; sc <= Uncommitted; sc++ {
		for _, f := range c.files[sc] {
			counts[key{f.Path, f.Area, f.Code}] = f
		}
	}
	out := *s
	for sc := Changed; sc <= All; sc++ {
		fs := slices.Clone(s.files[sc])
		for i, f := range fs {
			if n, ok := counts[key{f.Path, f.Area, f.Code}]; ok {
				fs[i].Add, fs[i].Del, fs[i].Binary = n.Add, n.Del, n.Binary
			}
		}
		out.files[sc] = fs
	}
	whole := map[key]FileState{}
	for _, f := range c.whole {
		whole[key{f.Path, f.Area, f.Code}] = f
	}
	out.whole = slices.Clone(s.whole)
	for i, f := range out.whole {
		if n, ok := whole[key{f.Path, f.Area, f.Code}]; ok {
			out.whole[i].Add, out.whole[i].Del, out.whole[i].Binary = n.Add, n.Del, n.Binary
		}
	}
	out.index()
	return &out
}

// againstHead is the status letter of a change against HEAD, from its staged (x) and unstaged (y) letters: a file
// added and then edited is still added, and one deleted from the work tree is deleted.
func againstHead(x, y byte) byte {
	switch {
	case y == 'D' || x == 'D':
		return 'D'
	case x == 'A' || x == 'R' || x == 'C':
		return x
	case x == '.':
		return y
	}
	return 'M'
}
