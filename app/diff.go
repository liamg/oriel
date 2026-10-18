package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/worker"
)

// DiffView is the review diff (ui.md 06): a repo's branch against its base, every changed file, hunk by hunk.
const DiffView = "diff"

// DiffKeyMode is the focused diff's keys: ]h [h between hunks, r marks one reviewed, gc comments, ? explains.
const DiffKeyMode = "diff"

// The diff's slots for plugins, each a view drawn in the order of its name, built with the diff's repo root as
// ViewCtx.Instance: DiffHeader views above the files (the review ledger), DiffSide views in the left column under
// the changed files (authorship), and DiffRight views in a column at the right (an AI review's findings).
const (
	DiffHeader = "diff.header."
	DiffSide   = "diff.side."
	DiffRight  = "diff.right."
)

// diffPresenter is DecorationCtx.Presenter while the diff asks for a file's decorations: their Blocks go between
// its lines.
const diffPresenter = "diff"

// ExplainUnit is the command the diff's ? runs, {file, line}: explain the code unit there (RV-5).
const ExplainUnit = "explain.unit"

// diffPlugin is the diff view, its commands and keys, and the review marks it keeps.
type diffPlugin struct {
	disposer
	sh *shell
	h  plugin.Host

	by    map[string]*diffState // by instance: the repo's root
	focus *diffState            // the diff with focus, or the last to have it

	mu       sync.Mutex
	files    map[diffKey]*diffFile
	loading  map[diffKey]bool
	started  map[diffKey]time.Time // when each file's last diff was started, so one changing fast isn't remade each time
	waking   map[diffKey]bool      // a redraw is set for when a file's diff can be made again
	gen      uint64                // bumped when a diff lands or a local review changes: what the ledger is made from
	reviews  uint64                // bumped when a local review changes
	ledgers  map[string]cachedLedger
	parts    map[diffKey]ledgerPart  // each file's share of its ledger
	stats    map[diffKey]fileStat    // each changed file's size and time on disk, read off the loop
	touched  map[string]touchedFiles // by root: the files the branch's agent commits touched
	statting bool
	styled   map[diffKey]bool      // files the view has shown, whose diffs are made styled from then on
	local    core.State            // reviews made outside a session, as session "local" with each repo's root its project; they last as long as the window
	texts    map[string]agentTexts // by session and root
	busy     sync.WaitGroup
	limit    chan struct{} // diffs made at once
}

type diffKey struct{ repo, path string }

// diffState is one diff view's place: the files open, the selection, and what to do next frame.
type diffState struct {
	root   string
	open   map[string]bool
	sel    diffSel
	reveal bool
	focus  bool
	goLine int // a line of the selected file's new side to select once it's drawn
	scroll scrollState
	order  []diffSel                  // every hunk drawn, in order
	lines  map[diffSel]int            // lines each hunk has, as drawn
	bufs   map[string]diffBuf         // the new side of files not open in the editor
	ids    map[diffSel]ui.ID          // hunks and lines as last drawn
	hunks  map[string][]diffHunk      // each file's hunks as last drawn
	drawn  map[string]*diffFile       // each file's diff as last drawn
	files  []gitx.FileState           // the files listed, as last drawn
	marks  map[string]map[string]bool // each file's marks as last drawn
	ledger registry.Ledger            // as last drawn
	seen   map[string]time.Time       // agent-written lines on screen, by file and key, since when
	sent   map[string]core.LineState  // what's been recorded of them
}

// diffSel is a place in the diff: a file, a hunk of it, a line of the hunk (-1 for the hunk as a whole).
type diffSel struct {
	file       string
	hunk, line int
}

func (x *diffPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "diff"} }

func (x *diffPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.by, x.files, x.loading = map[string]*diffState{}, map[diffKey]*diffFile{}, map[diffKey]bool{}
	x.started, x.waking, x.styled = map[diffKey]time.Time{}, map[diffKey]bool{}, map[diffKey]bool{}
	x.local = core.State{Sessions: []core.SessionState{{ID: localReviews}}}
	x.limit = make(chan struct{}, 4)
	x.add(h.Option(registry.OptionSpec{Name: "diff.layout", Section: "git", Type: registry.Enum, Values: []string{"split", "unified"}, Default: "split",
		Scopes: registry.ScopeUser, Help: "How the diff shows a change: old and new side by side, or one above the other."}))
	has := func(registry.Args) bool { return x.focus != nil }
	on := func(fn func(st *diffState, a registry.Args) error) func(registry.Ctx, registry.Args) error {
		return func(_ registry.Ctx, a registry.Args) error {
			if x.focus == nil {
				return errors.New("no diff is open")
			}
			return fn(x.focus, a)
		}
	}
	move := func(fn func(*diffState, int), d int) func(registry.Ctx, registry.Args) error {
		return on(func(st *diffState, _ registry.Args) error { fn(st, d); return nil })
	}
	for _, c := range []registry.Command{
		{ID: "diff.open", Title: "Open the branch diff of {path}: a file's, or a repo's whole change (the work dir's if none), against the merge-base with its base branch; at the file's {line}",
			Run: func(ctx registry.Ctx, a registry.Args) error { return x.open(ctx, a.String("path"), a.Int("line")) }},
		{ID: "diff.layout", Title: "Show the diff split or unified: {layout}, or the other one", Run: func(_ registry.Ctx, a registry.Args) error {
			l := a.String("layout")
			if l == "" {
				l = map[string]string{"split": "unified", "unified": "split"}[x.layout()]
			}
			return h.Options().Set("diff.layout", registry.LayerUser, registry.At{}, l)
		}},
		{ID: "diff.hunk.next", Title: "Diff: the next hunk", Available: has, Run: move(x.moveHunk, 1)},
		{ID: "diff.hunk.prev", Title: "Diff: the hunk before", Available: has, Run: move(x.moveHunk, -1)},
		{ID: "diff.line.next", Title: "Diff: the next line", Available: has, Run: move(x.moveLine, 1)},
		{ID: "diff.line.prev", Title: "Diff: the line before", Available: has, Run: move(x.moveLine, -1)},
		{ID: "diff.review", Title: "Mark the selected hunk reviewed, or not again; or {path}'s hunk {hunk} (0-based)", Available: has,
			Run: on(func(st *diffState, a registry.Args) error { return x.reviewHunk(st, hunkArg(st, a)) })},
		{ID: "diff.review.file", Title: "Mark every hunk of {path} (the selected file if none) reviewed, or {reviewed}", Available: has,
			Run: on(func(st *diffState, a registry.Args) error {
				var want *bool
				if v, ok := a["reviewed"].(bool); ok {
					want = &v
				}
				return x.reviewFile(st, cmpOr(a.String("path"), st.sel.file), want)
			})},
		{ID: "diff.comment", Title: "Open a thread on the selected line of the diff, or its hunk's lines", Available: has, Run: on(x.comment)},
		{ID: "diff.explain", Title: "Explain the code unit of the selected line (" + ExplainUnit + ")", Available: has, Run: on(x.explain)},
		{ID: "diff.source", Title: "Open the selected line in the editor", Available: has, Run: on(x.source)},
		{ID: "diff.expand", Title: "Show or fold a file of the diff: {path}, the selected one if none", Available: has,
			Run: on(func(st *diffState, a registry.Args) error {
				p := cmpOr(a.String("path"), st.sel.file)
				st.open[p] = !st.open[p]
				return nil
			})},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "]h", Command: "diff.hunk.next", Desc: "Next hunk"},
		{Keys: "[h", Command: "diff.hunk.prev", Desc: "Hunk before"},
		{Keys: "j", Command: "diff.line.next", Desc: "Next line"},
		{Keys: "<Down>", Command: "diff.line.next", Desc: "Next line"},
		{Keys: "k", Command: "diff.line.prev", Desc: "Line before"},
		{Keys: "<Up>", Command: "diff.line.prev", Desc: "Line before"},
		{Keys: "r", Command: "diff.review", Desc: "Mark reviewed"},
		{Keys: "gc", Command: "diff.comment", Desc: "Comment"},
		{Keys: "?", Command: "diff.explain", Desc: "Explain"},
		{Keys: "o", Command: "diff.source", Desc: "Open in the editor"},
		{Keys: "<CR>", Command: "diff.expand", Desc: "Show or fold the file"},
		{Keys: "q", Command: "view.close", Desc: "Close"},
	} {
		k.Mode = DiffKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.View(DiffView, diffView{x}))
	x.activateLedger()
	x.add(h.View(DiffSide+"50-authorship", registry.ViewFunc(func(vc registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "authorship", Fn: func(c *ui.Ctx) { x.authorshipView(c, vc.Instance) }}
	})))
	return nil
}

func (x *diffPlugin) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

// wait is until the diffs being made are done.
func (x *diffPlugin) wait() { x.busy.Wait() }

// hunkArg is the hunk {path} {hunk} names, or the selected one.
func hunkArg(st *diffState, a registry.Args) diffSel {
	if a.String("path") == "" {
		return diffSel{file: st.sel.file, hunk: st.sel.hunk, line: -1}
	}
	return diffSel{file: a.String("path"), hunk: a.Int("hunk"), line: -1}
}

func (x *diffPlugin) layout() string {
	if v, _, _ := x.h.Options().Get("diff.layout", registry.At{}); v == "unified" {
		return "unified"
	}
	return "split"
}

func (x *diffPlugin) state(root string) *diffState {
	st := x.by[root]
	if st == nil {
		st = &diffState{root: root, open: map[string]bool{}, sel: diffSel{hunk: -1, line: -1}, bufs: map[string]diffBuf{}}
		x.by[root] = st
	}
	return st
}

// target is the repo a path is in and the file it names there ("" for the whole change): a file or directory in a
// work tree, or a session's workspace (its primary project).
func (x *diffPlugin) target(path string) (root, rel string, err error) {
	return repoTarget(x.sh, x.h, path)
}

// repoTarget is the repo a path is in and the file it names there ("" for the whole repo): a file or directory in
// a work tree, or a session's workspace (its primary project); the work dir's with no path.
func repoTarget(sh *shell, h plugin.Host, path string) (root, rel string, err error) {
	if path == "" {
		path = sh.cfg.WorkDir
	}
	if path == "" {
		return "", "", errors.New("no directory is open")
	}
	path = filepath.Clean(path)
	for _, s := range h.State().Sessions {
		if filepath.Clean(s.Dir) == path && !gitx.IsRepo(path) {
			if p, ok := s.Primary(); ok {
				path = p.Dir
			}
		}
	}
	dir := path
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		dir = filepath.Dir(path)
	}
	for dir != filepath.Dir(dir) {
		if _, err := os.Stat(dir); err == nil {
			break
		}
		dir = filepath.Dir(dir)
	}
	if t := sh.git.holding(path); t.root() != "" && within(path, t.root()) {
		root = t.root()
	} else if r, ok := gitx.Root(context.Background(), dir); ok {
		root = r
	} else {
		return "", "", fmt.Errorf("%s isn't in a git repository", path)
	}
	if path != root {
		if r, err := filepath.Rel(root, path); err == nil {
			rel = filepath.ToSlash(r)
		}
	}
	return root, rel, nil
}

// open shows the diff of path's repo, with path's file open in it and selected, at its line when line isn't 0.
func (x *diffPlugin) open(ctx context.Context, path string, line int) error {
	root, rel, err := x.target(path)
	if err != nil {
		return err
	}
	st := x.state(root)
	if rel != "" {
		st.open[rel] = true
		st.sel = diffSel{file: rel, hunk: -1, line: -1}
		st.reveal, st.goLine = true, line
	}
	st.focus = true
	x.sh.git.tree(root)
	return x.h.Run(ctx, "view.open", registry.Args{"id": DiffView, "instance": root, "place": "tab"})
}

// localReviews is the session x.local keeps reviews made outside one in.
const localReviews = "local"

// session is the session and project whose work tree root is, for marks that last; ok is false outside one.
func (x *diffPlugin) session(root string) (core.SessionState, string, bool) {
	return projectSession(x.h, root)
}

// projectSession is the session and project whose work tree is root, the session in front first; ok is false
// outside one.
func projectSession(h plugin.Host, root string) (core.SessionState, string, bool) {
	return sessionAt(h.State(), root)
}

// sessionAt is the session and project whose work tree root is, the session in front first.
func sessionAt(st core.State, root string) (core.SessionState, string, bool) {
	var out []core.SessionState
	if se, ok := st.Session(st.Focus); ok {
		out = append(out, se.Snapshot())
	}
	out = append(out, st.Sessions...)
	for _, se := range out {
		for _, p := range se.Projects {
			if p.Dir != "" && filepath.Clean(p.Dir) == filepath.Clean(root) {
				return se, p.Name, true
			}
		}
	}
	return core.SessionState{}, "", false
}

// review is what of a file's change has been reviewed: in its session, or for the window outside one.
func (x *diffPlugin) review(root, file string) core.Review {
	if se, project, ok := x.session(root); ok {
		return se.ReviewOf(project, file)
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.local.Sessions[0].ReviewOf(root, file)
}

// marks are the changes of a file marked reviewed.
func (x *diffPlugin) marks(root, file string) map[string]bool {
	out := map[string]bool{}
	for _, h := range x.review(root, file).Hunks {
		out[h] = true
	}
	return out
}

// record applies a review event: through the session host, so it lasts, or for as long as the window outside a
// session. ev's session, and its project outside one, are filled in.
func (x *diffPlugin) record(root string, ev core.Event, send func(c *worker.Worker, sid core.SessionID, project string) error) error {
	se, project, ok := x.session(root)
	if !ok {
		x.mu.Lock()
		err := ev.Apply(&x.local)
		x.gen++
		x.reviews++
		x.mu.Unlock()
		x.h.Redraw()
		return err
	}
	var c *worker.Worker
	var err error
	if x.sh.sessions == nil {
		err = errors.New("there are no sessions in this window")
	} else {
		c, err = x.sh.sessions.host()
	}
	if err != nil {
		return err
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		if err := send(c, se.ID, project); err != nil {
			x.h.Notify(core.Notice{ID: "diff.review", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	}()
	return nil
}

// setMarks marks changes of a file reviewed or not.
func (x *diffPlugin) setMarks(root, file string, ids []string, on bool) error {
	if len(ids) == 0 {
		return nil
	}
	local := core.HunksReviewed{Session: localReviews, Project: root, File: file, Hunks: slices.Clone(ids), Reviewed: on}
	return x.record(root, local, func(c *worker.Worker, sid core.SessionID, project string) error {
		ev := local
		ev.Session, ev.Project = sid, project
		return c.SetReviewed(ev)
	})
}

// setSeen records lines of a file seen in the diff, read or skimmed.
func (x *diffPlugin) setSeen(root, file string, read, skimmed []string) error {
	if len(read)+len(skimmed) == 0 {
		return nil
	}
	local := core.LinesSeen{Session: localReviews, Project: root, File: file, Read: read, Skimmed: skimmed}
	return x.record(root, local, func(c *worker.Worker, sid core.SessionID, project string) error {
		ev := local
		ev.Session, ev.Project = sid, project
		return c.SetSeen(ev)
	})
}

// hunkAt is the hunk a place names, as last drawn.
func (st *diffState) hunkAt(s diffSel) (diffHunk, bool) {
	hs := st.hunks[s.file]
	if s.hunk < 0 || s.hunk >= len(hs) {
		return diffHunk{}, false
	}
	return hs[s.hunk], true
}

// reviewed says whether every change of a hunk is marked.
func reviewed(h diffHunk, marks map[string]bool) bool {
	for _, id := range h.ids {
		if !marks[id] {
			return false
		}
	}
	return len(h.ids) > 0
}

func (x *diffPlugin) reviewHunk(st *diffState, s diffSel) error {
	h, ok := st.hunkAt(s)
	if !ok {
		return errors.New("no hunk is selected")
	}
	return x.setMarks(st.root, s.file, h.ids, !reviewed(h, x.marks(st.root, s.file)))
}

// reviewFile marks every hunk of a file, reviewed unless want says otherwise, or every one already is.
func (x *diffPlugin) reviewFile(st *diffState, file string, want *bool) error {
	hs := st.hunks[file]
	if d := st.drawn[file]; d != nil {
		hs = d.hunks
	}
	if len(hs) == 0 {
		return fmt.Errorf("%s has no hunks to review", file)
	}
	marks := x.marks(st.root, file)
	var ids []string
	all := true
	for _, h := range hs {
		ids = append(ids, h.ids...)
		all = all && reviewed(h, marks)
	}
	on := !all
	if want != nil {
		on = *want
	}
	return x.setMarks(st.root, file, ids, on)
}

// moveHunk selects the next or previous hunk of the files open.
func (x *diffPlugin) moveHunk(st *diffState, d int) {
	if len(st.order) == 0 {
		return
	}
	i := slices.IndexFunc(st.order, func(s diffSel) bool { return s.file == st.sel.file && s.hunk == st.sel.hunk })
	switch {
	case i < 0 && d < 0:
		i = len(st.order) - 1
	case i < 0:
		i = 0
	default:
		i = min(max(i+d, 0), len(st.order)-1)
	}
	st.sel = st.order[i]
	st.sel.line = -1
	st.reveal = true
}

// moveLine selects the next or previous line, across hunks.
func (x *diffPlugin) moveLine(st *diffState, d int) {
	if len(st.order) == 0 {
		return
	}
	i := slices.IndexFunc(st.order, func(s diffSel) bool { return s.file == st.sel.file && s.hunk == st.sel.hunk })
	if i < 0 {
		st.sel = st.order[0]
		st.sel.line = 0
		st.reveal = true
		return
	}
	line := st.sel.line + d
	if d < 0 && st.sel.line < 0 {
		line = -1
	}
	n := st.lines[st.order[i]]
	switch {
	case line >= n && i+1 < len(st.order):
		st.sel = st.order[i+1]
		st.sel.line = 0
	case line < 0 && i > 0:
		st.sel = st.order[i-1]
		st.sel.line = st.lines[st.order[i-1]] - 1
	default:
		st.sel.line = min(max(line, 0), n-1)
	}
	st.reveal = true
}

// selLine is the new side's line (1-based) of the selection and the last line of its range: the line selected, or
// the hunk's changed lines.
func (st *diffState) selLine() (path string, line, end int, ok bool) {
	h, found := st.hunkAt(st.sel)
	if !found {
		return "", 0, 0, false
	}
	path = filepath.Join(st.root, filepath.FromSlash(st.sel.file))
	near := func(i int) int {
		for k := i; k >= 0; k-- {
			if n := h.Lines[k].New; n > 0 {
				return n
			}
		}
		return max(h.NewStart, 1)
	}
	if l := st.sel.line; l >= 0 && l < len(h.Lines) {
		return path, near(l), 0, true
	}
	first, last := 0, 0
	for i, l := range h.Lines {
		if l.Op == ' ' {
			continue
		}
		n := near(i)
		if first == 0 {
			first = n
		}
		last = n
	}
	if last == first {
		last = 0
	}
	return path, max(first, 1), last, true
}

// comment opens a thread on the selection's lines, between them in the diff.
func (x *diffPlugin) comment(st *diffState, _ registry.Args) error {
	path, line, end, ok := st.selLine()
	if !ok {
		return errors.New("no hunk is selected")
	}
	a := registry.Args{"file": path, "line": line}
	if end > line {
		a["end_line"] = end
	}
	return x.h.Run(context.Background(), "thread.new", a)
}

// explain opens the selected line in the editor and asks for its code unit to be explained there.
func (x *diffPlugin) explain(st *diffState, _ registry.Args) error {
	path, line, _, ok := st.selLine()
	if !ok {
		return errors.New("no hunk is selected")
	}
	if err := x.h.Run(context.Background(), "editor.open", registry.Args{"path": path, "line": line}); err != nil {
		return err
	}
	return x.h.Run(context.Background(), ExplainUnit, registry.Args{"file": path, "line": line})
}

func (x *diffPlugin) source(st *diffState, _ registry.Args) error {
	path, line, _, ok := st.selLine()
	if !ok {
		return errors.New("no hunk is selected")
	}
	return x.h.Run(context.Background(), "editor.open", registry.Args{"path": path, "line": line})
}

// diffFor is a file's diff as last made, and makes it again (off the loop) when what it's made from has changed, or
// styled when the view asks for one (styled) and it's plain. current is false while the diff is older than the file.
func (x *diffPlugin) diffFor(tree *gitTree, st *gitx.State, f gitx.FileState, styled, blame bool) (d *diffFile, current bool) {
	root := tree.root()
	work := tree.abs(f.Path)
	in := diffInput{rev: st.BaseHash, path: f.Path, old: f.Old, code: f.Code, add: f.Add, del: f.Del, repo: root, work: work}
	if f.Code == 'A' || f.Code == '?' {
		in.rev = ""
	}
	var snap *text.Snapshot
	var buf vim.FileState
	open := false
	if x.sh.editor != nil && x.sh.editor.e != nil {
		buf, open = x.sh.editor.openBuffer(work)
	}
	if open {
		s := buf.Text.Snapshot()
		in.buf, in.version, snap = buf.Text, s.Version(), &s
	}
	k := diffKey{root, f.Path}
	if !open {
		in.size, in.mtime = x.stat(k, work, styled)
	}
	x.mu.Lock()
	if styled {
		x.styled[k] = true
	}
	styled = x.styled[k]
	blame = blame || styled
	d, busy := x.files[k], x.loading[k]
	x.mu.Unlock()
	current = d != nil && d.in == in
	if (d == nil || d.in != in || styled && !d.styled || blame && !d.blamed) && !busy {
		if d != nil && d.in != in && !x.mayRemake(k) {
			return d, false
		}
		// A diff is made in stages, each shown as it lands: plain, then styled, then blamed.
		var styling, blaming bool
		switch {
		case d == nil:
			x.h.Log().Debug("making a diff", "path", f.Path)
		case d.in != in:
			styling, blaming = d.styled, d.blamed
			x.h.Log().Debug("remaking a diff", "path", f.Path, "changed", changedInputs(d.in, in))
		case styled && !d.styled:
			styling = true
			x.h.Log().Debug("styling a diff", "path", f.Path)
		default:
			styling, blaming = d.styled, true
			x.h.Log().Debug("blaming a diff", "path", f.Path)
		}
		var newText []byte
		if open {
			newText = buf.Bytes()
		}
		x.mu.Lock()
		x.loading[k] = true
		if d == nil || d.in != in {
			x.started[k] = time.Now() // a stage on the same text doesn't hold back its remaking
		}
		x.mu.Unlock()
		x.busy.Add(1)
		go func() {
			defer x.busy.Done()
			x.limit <- struct{}{}
			nd := loadDiff(x.h, in, newText, snap, styling, blaming)
			<-x.limit
			x.mu.Lock()
			x.files[k], x.loading[k] = nd, false
			x.gen++
			x.mu.Unlock()
			x.h.Redraw()
		}()
	}
	return d, current
}

// made is the diff made last of a file, nil before one is; it starts none.
func (x *diffPlugin) made(root, path string) *diffFile {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.files[diffKey{root, path}]
}

// statFresh is how long a file's size and time on disk are taken as read before they're read again.
const statFresh = time.Second

type fileStat struct {
	size, mtime int64
	read        time.Time
}

// stat is k's size and time on disk. The first read, and every one for the view (now), is now; after that it's what
// was last read, with the reads older than statFresh done again off the loop, and a frame drawn when one has changed.
func (x *diffPlugin) stat(k diffKey, work string, now bool) (size, mtime int64) {
	x.mu.Lock()
	st, ok := x.stats[k]
	if now {
		ok = false
	}
	stale := ok && time.Since(st.read) > statFresh && !x.statting
	if stale {
		x.statting = true
	}
	x.mu.Unlock()
	if !ok {
		st = statNow(work)
		x.mu.Lock()
		if x.stats == nil {
			x.stats = map[diffKey]fileStat{}
		}
		x.stats[k] = st
		x.mu.Unlock()
	}
	if stale {
		x.busy.Add(1)
		go x.restat()
	}
	return st.size, st.mtime
}

// restat reads again every file whose read is older than statFresh.
func (x *diffPlugin) restat() {
	defer x.busy.Done()
	x.mu.Lock()
	var due []diffKey
	for k, st := range x.stats {
		if time.Since(st.read) > statFresh {
			due = append(due, k)
		}
	}
	x.mu.Unlock()
	changed := false
	read := make(map[diffKey]fileStat, len(due))
	for _, k := range due {
		read[k] = statNow(filepath.Join(k.repo, filepath.FromSlash(k.path)))
	}
	x.mu.Lock()
	for k, st := range read {
		if old := x.stats[k]; old.size != st.size || old.mtime != st.mtime {
			changed = true
		}
		x.stats[k] = st
	}
	x.statting = false
	if changed {
		x.gen++
	}
	x.mu.Unlock()
	if changed {
		x.h.Redraw()
	}
}

func statNow(path string) fileStat {
	st := fileStat{read: time.Now()}
	if fi, err := os.Stat(path); err == nil {
		st.size, st.mtime = fi.Size(), fi.ModTime().UnixNano()
	}
	return st
}

// diffRemakeGap is the least time between starting a file's diffs: one written to over and over is remade at this
// pace at most, not on every write.
const diffRemakeGap = 500 * time.Millisecond

// mayRemake reports whether k's diff can be made again now, and if not, has a frame drawn when it can, with what's
// cached on the old diff (the ledger) made again.
func (x *diffPlugin) mayRemake(k diffKey) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	wait := diffRemakeGap - time.Since(x.started[k])
	if wait <= 0 {
		return true
	}
	if !x.waking[k] {
		x.waking[k] = true
		time.AfterFunc(wait, func() {
			x.mu.Lock()
			delete(x.waking, k)
			x.gen++ // what was made from the old diff asks for the new one
			x.mu.Unlock()
			x.h.Redraw()
		})
	}
	return false
}

// changedInputs names what differs between two diffs' inputs, for the log.
func changedInputs(a, b diffInput) string {
	var out []string
	for _, c := range []struct {
		name string
		same bool
	}{{"rev", a.rev == b.rev}, {"path", a.path == b.path && a.old == b.old}, {"status", a.code == b.code},
		{"counts", a.add == b.add && a.del == b.del}, {"size", a.size == b.size}, {"mtime", a.mtime == b.mtime},
		{"buffer", a.buf == b.buf && a.version == b.version}, {"repo", a.repo == b.repo && a.work == b.work}} {
		if !c.same {
			out = append(out, c.name)
		}
	}
	return strings.Join(out, " ")
}

// newBuffer is the buffer of a file's new side, for its decorations: the editor's when it's open, else one of the
// diff's own, edited to the text each new diff is made from so anchors in it follow the changes.
func (x *diffPlugin) newBuffer(st *diffState, file string, d *diffFile) *text.Buffer {
	if d.in.buf != nil {
		return d.in.buf
	}
	b := st.bufs[file]
	switch {
	case b.buf == nil:
		b = diffBuf{from: d, buf: text.New(d.newText, text.Author{Kind: text.File})}
	case b.from != d:
		snap := b.buf.Snapshot()
		if old := snap.Bytes(); !bytes.Equal(old, d.newText) {
			_, err := b.buf.Apply(text.Edit{Ranges: hunkEdits(snap, gitx.DiffLines(old, d.newText)), Author: text.Author{Kind: text.File}})
			if err != nil || !bytes.Equal(b.buf.Snapshot().Bytes(), d.newText) {
				b.buf = text.New(d.newText, text.Author{Kind: text.File})
			}
		}
		b.from = d
	}
	st.bufs[file] = b
	return b.buf
}

// diffBuf is a file's new side as a buffer, and the diff it was last brought up to.
type diffBuf struct {
	from *diffFile
	buf  *text.Buffer
}

type diffView struct{ x *diffPlugin }

func (v diffView) Title(instance string) string { return "Diff · " + filepath.Base(instance) }
func (v diffView) EdgeToEdge()                  {}
func (v diffView) ModeLabel(string) (string, string) {
	return "REVIEW", ""
}
func (v diffView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "diff", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

// diffPersisted is a diff as the layout keeps it across restarts: its repo and the file selected.
type diffPersisted struct {
	Root string `json:"root"`
	File string `json:"file,omitempty"`
}

func (v diffView) Persist(instance string) (json.RawMessage, bool) {
	p := diffPersisted{Root: instance}
	if st := v.x.by[instance]; st != nil {
		p.File = st.sel.file
	}
	b, _ := json.Marshal(p)
	return b, true
}

func (v diffView) Revive(state json.RawMessage) (string, error) {
	var p diffPersisted
	if err := json.Unmarshal(state, &p); err != nil {
		return "", err
	}
	if !gitx.IsRepo(p.Root) {
		return "", fmt.Errorf("%s isn't a git repository", p.Root)
	}
	st := v.x.state(p.Root)
	if p.File != "" {
		st.open[p.File] = true
		st.sel = diffSel{file: p.File, hunk: -1, line: -1}
		st.reveal = true
	}
	return p.Root, nil
}

// agentName is what the authorship box calls the agent of root's session.
func (x *diffPlugin) agentName(root string) string {
	if se, _, ok := x.session(root); ok && se.Agent.Provider != "" {
		return providerName(se.Agent.Provider)
	}
	return "Agent"
}
