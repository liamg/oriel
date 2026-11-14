package editor

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

var (
	tBlameYou   = theme.Declare("git.blame.you", "fg.ghost")
	tBlameAgent = theme.Declare("git.blame.agent", "alpha(brand, 70%)")
	tBlameOther = theme.Declare("git.blame.other", "fg.ghost")
)

// GitSigns is git in the editor: the gutter's hunks against the branch's base (git.gutter.base), staging,
// unstaging and resetting the hunk under the cursor against the index, ]c and [c between hunks, a preview of what a hunk replaced, and the cursor
// line's blame by author kind.
type GitSigns struct {
	kit.Disposer
	Editor *Editor
	h      plugin.Host
	Clock  clock.Clock // the clock blame's ages are by; the system's if nil

	mu       sync.Mutex
	watched  map[*gitx.Status]func() // the projects' trees whose changes stale their bases and blames
	bases    map[gitSource]*gitBase  // each file as a hunk's source has it
	gens     int                     // the last base read's gen
	blames   map[gitFile]*gitBlame
	hunks    map[bufAgainst]*bufHunks
	previews map[gitFile]int // the hunk previewed in each file, by its new start
	typing   work.Still[any] // a buffer's hunks and blame wait for it to be still (signsQuiet) once it has some
	diffs    int             // diffs begun off the loop, for the tests
	busy     work.Busy
}

type gitBase struct {
	data    []byte
	loaded  bool
	gen     int
	loading bool
}

// signsQuiet is how long a buffer being typed in waits, still, for its hunks to be diffed and its blame read again.
const signsQuiet = 150 * time.Millisecond

// gitSource is where a file's hunks are from: the index of tree when rev is "", else path at rev.
type gitSource struct{ tree, rev, path string }

// bufAgainst is a buffer's hunks for the gutter, or against the index for staging.
type bufAgainst struct {
	buf   *text.Buffer
	index bool
}

type gitBlame struct {
	loaded  bool
	version uint64
	head    string // the commit it was read at
	lines   []gitx.BlameLine
	loading bool
}

// bufHunks are a buffer's last hunks, diffed at version against base gen; diffing is set while a diff runs.
type bufHunks struct {
	version uint64
	gen     int // unique to each base read
	hunks   []gitx.Hunk
	diffed  bool
	diffing bool
}

func (g *GitSigns) Manifest() plugin.Manifest { return plugin.Manifest{ID: "gitsigns"} }

// stale has the bases and blames that changes to paths in tree left behind read again: the index's of each, and the
// blame of each. No paths is another work tree: everything of tree goes. A base at a commit never changes, and a
// blame is read again when the head moves (blameOf).
func (g *GitSigns) stale(tree string, paths []string) {
	g.mu.Lock()
	for src := range g.bases {
		if src.tree == tree && (paths == nil || src.rev == "" && slices.Contains(paths, src.path)) {
			delete(g.bases, src)
		}
	}
	for f, b := range g.blames {
		if f.tree == tree && (paths == nil || slices.Contains(paths, f.rel)) {
			b.loaded = false
		}
	}
	g.mu.Unlock()
	g.h.DecorationsChanged("git.hunks")
}

// watch stales tree's bases and blames as its status changes, once it's read; the window's tree has WatchWindow.
func (g *GitSigns) watch(tree string, st *gitx.Status) {
	if tree == "" || st == nil {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, ok := g.watched[st]; !ok {
		g.watched[st] = st.Watch(func(paths []string) { g.stale(tree, paths) })
	}
}

func (g *GitSigns) Activate(_ context.Context, h plugin.Host) error {
	g.Add(h.Contribute(registry.Busy{Name: "gitsigns", Busy: func() bool { return g.busy.Busy() || g.typing.Held() }}))
	g.h = h
	g.typing = work.Still[any]{Wait: signsQuiet, Wake: func() { h.DecorationsChanged("git.hunks") }, Clock: h.Clock()}
	g.bases, g.blames, g.hunks, g.previews = map[gitSource]*gitBase{}, map[gitFile]*gitBlame{}, map[bufAgainst]*bufHunks{}, map[gitFile]int{}
	g.watched = map[*gitx.Status]func(){}
	g.Add(h.Option(registry.OptionSpec{Name: "git.gutter.base", Type: registry.Enum, Values: []string{"scope", "branch", "head", "index"},
		Default: "scope", Scopes: registry.ScopeUser | registry.ScopeProject,
		Help: "What the gutter marks a file's changes against: as the Explorer's scope has it (scope: Uncommitted's against the last commit, Changed's against the branch's base), where the branch left its base (branch), the last commit (head) or what's staged (index)."}))
	g.Add(h.Option(registry.OptionSpec{Name: "git.blame.inline", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser | registry.ScopeProject,
		Help: "Show who last changed the cursor's line, and when, after it."}))
	g.Add(kit.GitOf(h).WatchWindow(func(_ string, paths []string) { g.stale("", paths) }))
	g.Add(registry.DisposeFunc(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		for _, cancel := range g.watched {
			cancel()
		}
	}))
	for _, c := range []registry.Command{
		{ID: "git.hunk.stage", Title: "Stage the hunk under the cursor", Run: g.cmd(true, g.stage)},
		{ID: "git.hunk.unstage", Title: "Unstage the staged hunk at the cursor", Run: g.cmd(true, g.unstage)},
		{ID: "git.hunk.reset", Title: "Put the hunk under the cursor back as the index has it", Run: g.cmd(true, g.reset)},
		{ID: "git.hunk.preview", Title: "Show what the hunk under the cursor replaced (toggle)", Run: g.cmd(false, g.preview)},
		{ID: "git.hunk.next", Title: "Go to the next hunk", Run: g.cmd(false, func(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
			return g.jump(x, p, hs, 1)
		})},
		{ID: "git.hunk.prev", Title: "Go to the previous hunk", Run: g.cmd(false, func(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
			return g.jump(x, p, hs, -1)
		})},
		{ID: "git.blame.toggle", Title: "Show or hide the cursor line's blame", Run: func(registry.Ctx, registry.Args) error {
			on, _, _ := h.Options().Get("git.blame.inline", h.Here())
			return h.Options().Set("git.blame.inline", registry.LayerUser, registry.At{}, on != true)
		}},
	} {
		g.Add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<Leader>hs", Command: "git.hunk.stage", Desc: "Stage hunk"},
		{Keys: "<Leader>hu", Command: "git.hunk.unstage", Desc: "Unstage hunk"},
		{Keys: "<Leader>hr", Command: "git.hunk.reset", Desc: "Reset hunk"},
		{Keys: "<Leader>hp", Command: "git.hunk.preview", Desc: "Preview hunk"},
		{Keys: "<Leader>hb", Command: "git.blame.toggle", Desc: "Blame on the cursor line"},
		{Keys: "]c", Command: "git.hunk.next", Desc: "Next hunk"},
		{Keys: "[c", Command: "git.hunk.prev", Desc: "Previous hunk"},
	} {
		k.Mode = "n"
		g.Add(h.Keymap(k))
	}
	g.Add(h.Decorations("git.hunks", uireg.KeyedDecorations{Key: g.decorationKey, Make: g.decorations}))
	return nil
}

func (g *GitSigns) Deactivate(ctx context.Context) error {
	g.busy.Wait()
	return g.Disposer.Deactivate(ctx)
}

// gitFile is a buffer's file as git names it, in the work tree of tree: its project's directory, "" for the window's.
type gitFile struct{ tree, rel, abs string }

// treeOf is f's work tree.
func (g *GitSigns) treeOf(f gitFile) services.GitTree { return kit.GitOf(g.h).Tree(f.tree) }

// repo is f's repo; fileOf has found it.
func (g *GitSigns) repo(f gitFile) gitx.Repo { return g.treeOf(f).Status().Repo() }

// located is path made absolute, in its project's work tree.
func (g *GitSigns) located(path string) gitFile {
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.Editor.dir, path)
	}
	f := gitFile{abs: path}
	if p, ok := kit.ProjectsHere(g.h).Of(path); ok {
		f.tree = p.Dir
	}
	return f
}

// fileOf is where path is in its project's repo, else the window's, if it's in one and not ignored.
func (g *GitSigns) fileOf(path string) (gitFile, bool) {
	if path == "" {
		return gitFile{}, false
	}
	f := g.located(path)
	tree := g.treeOf(f)
	if tree.Root() == "" {
		return gitFile{}, false
	}
	g.watch(f.tree, tree.Status())
	f.rel = tree.Rel(path)
	if strings.HasPrefix(f.rel, "../") || filepath.IsAbs(f.rel) || tree.State().Ignored(f.rel) {
		return gitFile{}, false
	}
	return f, true
}

// source is where the gutter's hunks of f are from, as git.gutter.base has it, or the index's for staging; not
// ok until the tree's status is read.
func (g *GitSigns) source(f gitFile, index bool) (gitSource, bool) {
	base := g.base()
	if index || base == "index" {
		return gitSource{tree: f.tree, path: f.rel}, true
	}
	st := g.treeOf(f).State()
	if st == nil {
		return gitSource{}, false
	}
	if base == "head" {
		return gitSource{tree: f.tree, rev: cmp.Or(st.Head, emptyRev), path: f.rel}, true
	}
	path := f.rel
	if fs, ok := st.File(f.rel); ok && fs.Old != "" {
		path = fs.Old
	}
	return gitSource{tree: f.tree, rev: cmp.Or(st.BaseHash, emptyRev), path: path}, true
}

// base is what the gutter marks changes against, git.gutter.base resolved: scope follows the Explorer's, the last
// commit while it shows Uncommitted, else the branch's base.
func (g *GitSigns) base() string {
	v, _, _ := g.h.Options().Get("git.gutter.base", g.h.Here())
	base, _ := v.(string)
	if base == "scope" || base == "" {
		if kit.GitOf(g.h).Scope() == gitx.Uncommitted {
			return "head"
		}
		return "branch"
	}
	return base
}

// emptyRev stands for the base of a repo with no commits, which has no files.
const emptyRev = "-"

// hunksOf are the hunks to buf's text as it would be written from f as the gutter's source has it (the index's
// with index), or none while that's read. With now, they're diffed here when out of date; otherwise the diff runs
// off the loop, one at a time per buffer, and the last hunks show until it lands.
func (g *GitSigns) hunksOf(f gitFile, buf *text.Buffer, fileBytes func() []byte, now, index bool) []gitx.Hunk {
	src, ok := g.source(f, index)
	if !ok {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	b := g.baseOf(f, src)
	if !b.loaded {
		return nil
	}
	version := buf.Snapshot().Version()
	key := bufAgainst{buf: buf, index: index}
	bh := g.hunks[key]
	if bh == nil {
		bh = &bufHunks{}
		g.hunks[key] = bh
	}
	switch {
	case bh.diffed && bh.version == version && bh.gen == b.gen:
	case now:
		bh.version, bh.gen, bh.hunks, bh.diffed = version, b.gen, gitx.DiffLines(b.data, fileBytes()), true
	case bh.diffed && bh.gen == b.gen && !g.typing.Ready(key, version): // typed in: the last hunks stand a moment
	case !bh.diffing:
		bh.diffing = true
		g.diffs++
		base, gen, contents := b.data, b.gen, fileBytes()
		g.busy.Go(func() {
			hs := gitx.DiffLines(base, contents)
			g.h.Do(func() {
				g.mu.Lock()
				bh.diffing = false
				if g.hunks[key] == bh && !(bh.diffed && bh.version > version) {
					bh.version, bh.gen, bh.hunks, bh.diffed = version, gen, hs, true
				}
				g.mu.Unlock()
				g.h.DecorationsChanged("git.hunks")
			})
		})
	}
	return bh.hunks
}

// baseOf is f as src has it, read off the loop the first time it's asked for. g.mu is held.
func (g *GitSigns) baseOf(f gitFile, src gitSource) *gitBase {
	b, ok := g.bases[src]
	if !ok {
		b = &gitBase{}
		g.bases[src] = b
	}
	if b.loaded || b.loading {
		return b
	}
	b.loading = true
	repo := g.repo(f)
	g.busy.Go(func() {
		var data []byte
		switch src.rev {
		case "":
			data, _ = repo.IndexFile(context.Background(), src.path)
		case emptyRev:
		default:
			data, _ = repo.FileAt(context.Background(), src.rev, src.path)
		}
		g.h.Do(func() {
			g.mu.Lock()
			g.gens++
			b.data, b.loaded, b.loading, b.gen = data, true, false, g.gens
			g.mu.Unlock()
			g.h.DecorationsChanged("git.hunks")
		})
	})
	return b
}

// signLine is the 0-based line a hunk's sign starts on: a deletion's is the line before it.
func signLine(h gitx.Hunk) int { return max(h.NewStart-1, 0) }

func covers(h gitx.Hunk, line int) bool {
	if h.NewLines == 0 {
		return line == signLine(h)
	}
	return line >= h.NewStart-1 && line < h.NewStart-1+h.NewLines
}

func (g *GitSigns) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	f, ok := g.fileOf(ctx.Path)
	if !ok {
		return nil
	}
	var out []uireg.Decoration
	hs := g.hunksOf(f, ctx.Buffer, ctx.FileBytes, false, false)
	g.mu.Lock()
	g.baseOf(f, gitSource{tree: f.tree, path: f.rel}) // the index's, ready for staging and resetting hunks
	previewed, previewing := g.previews[f]
	g.mu.Unlock()
	for _, h := range hs {
		style := "git." + h.Kind().String()
		if h.Kind() == gitx.Deleted {
			if l := signLine(h); l >= ctx.Top && l < ctx.Bottom {
				out = append(out, uireg.Decoration{Kind: uireg.Sign, Line: l, Style: style, Text: "▁", Priority: 5})
			}
		}
		for l := h.NewStart - 1; l < h.NewStart-1+h.NewLines; l++ {
			if l >= ctx.Top && l < ctx.Bottom {
				out = append(out, uireg.Decoration{Kind: uireg.Sign, Line: l, Style: style, Text: "▎", Priority: 5})
			}
		}
		if previewing && previewed == h.NewStart && len(h.Old) > 0 {
			end := signLine(h)
			if h.NewLines > 0 {
				end = h.NewStart - 2 + h.NewLines
			}
			out = append(out, uireg.Decoration{Kind: uireg.Block, Line: end, Node: &ui.Func{Key: "hunk", Fn: func(c *ui.Ctx) {
				diffBlock(c, "diff", h, false)
			}}, Priority: 5})
		}
	}
	if line, ok := g.blameLine(ctx, f); ok {
		out = append(out, line)
	}
	return out
}

// decorationKey is what the decorations of ctx are made from besides the file's hunks: whether its work tree is read,
// and where the cursor's line blame goes.
func (g *GitSigns) decorationKey(ctx uireg.DecorationCtx) any {
	type blameAt struct {
		on, base any
		loaded   bool // the file's work tree is read
		here     bool
		mode     string
		line     int
	}
	k := blameAt{}
	k.on, _, _ = g.h.Options().Get("git.blame.inline", g.h.Here())
	k.base = g.base()
	if ctx.Path != "" {
		k.loaded = g.treeOf(g.located(ctx.Path)).Load().Done
	}
	if x := g.Editor; x.e != nil {
		if p := x.current(); p.Buffer() == ctx.Buffer {
			k.here, k.mode, k.line = true, x.e.Mode(), p.Cursor().Line
		}
	}
	return k
}

// blameLine is the cursor line's blame, after it, while the pane is the current one.
func (g *GitSigns) blameLine(ctx uireg.DecorationCtx, f gitFile) (uireg.Decoration, bool) {
	x := g.Editor
	if x.e == nil {
		return uireg.Decoration{}, false
	}
	if on, _, _ := g.h.Options().Get("git.blame.inline", g.h.Here()); on != true {
		return uireg.Decoration{}, false
	}
	p := x.current()
	if p.Buffer() != ctx.Buffer || x.e.Mode() != "normal" {
		return uireg.Decoration{}, false
	}
	line := p.Cursor().Line
	if line < ctx.Top || line >= ctx.Bottom {
		return uireg.Decoration{}, false
	}
	snap := ctx.Snap
	head := ""
	if st := g.treeOf(f).State(); st != nil {
		head = st.Head
	}
	g.mu.Lock()
	b, ok := g.blames[f]
	if !ok {
		b = &gitBlame{}
		g.blames[f] = b
	}
	if b.loaded && b.head == head && b.version != snap.Version() && !g.typing.Ready(f, snap.Version()) {
		g.mu.Unlock()
		return uireg.Decoration{}, false // a line being typed has no blame worth a git run until it's still
	}
	if !b.loaded || b.version != snap.Version() || b.head != head {
		if !b.loading {
			b.loading = true
			version, contents, repo := snap.Version(), ctx.FileBytes(), g.repo(f)
			g.busy.Go(func() {
				lines, err := repo.Blame(context.Background(), f.rel, contents)
				g.h.Do(func() {
					g.mu.Lock()
					// A file blame can't read (untracked, say) waits for an edit or a git change to try again, not the next frame.
					b.loading, b.version, b.head, b.lines, b.loaded = false, version, head, lines, true
					if err != nil {
						b.lines = nil
					}
					g.mu.Unlock()
					g.h.DecorationsChanged("git.hunks")
				})
			})
		}
		g.mu.Unlock()
		return uireg.Decoration{}, false
	}
	defer g.mu.Unlock()
	if line >= len(b.lines) {
		return uireg.Decoration{}, false
	}
	bl := b.lines[line]
	var s, style string
	switch bl.Kind {
	case gitx.NotCommitted:
		s, style = "Not committed yet", "git.blame.you"
	case gitx.ByYou:
		s, style = "You", "git.blame.you"
	case gitx.ByAgent:
		s, style = bl.Author+" (agent)", "git.blame.agent"
	default:
		s, style = bl.Author, "git.blame.other"
	}
	if bl.Kind != gitx.NotCommitted {
		s += " · " + widget.AgoLabel(clock.Or(g.Clock).Now(), bl.When) + " ago · " + bl.Summary
		ctx.RefreshAt(ctx.Now.Add(time.Minute))
	}
	return uireg.Decoration{Kind: uireg.VirtualText, Line: line, Text: s, Style: style, Priority: -1}, true
}

// cmd runs fn on the current editor window's file and its hunks, the gutter's or with index the index's; outside a
// repo it says so.
func (g *GitSigns) cmd(index bool, fn func(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error) func(registry.Ctx, registry.Args) error {
	return func(registry.Ctx, registry.Args) error {
		x := g.Editor
		if x.e == nil {
			return fmt.Errorf("no editor")
		}
		p := x.current()
		f, ok := g.fileOf(p.Name())
		if !ok {
			return fmt.Errorf("%s isn't in a git repository", cmp.Or(p.Name(), "[No Name]"))
		}
		return fn(x, p, f, g.hunksOf(f, p.Buffer(), p.FileBytes, true, index))
	}
}

func hunkAt(hs []gitx.Hunk, line int) (gitx.Hunk, bool) {
	for _, h := range hs {
		if covers(h, line) {
			return h, true
		}
	}
	return gitx.Hunk{}, false
}

func (g *GitSigns) stage(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	contents := p.FileBytes()
	repo := g.repo(f)
	kit.GitOf(g.h).Run(func(ctx context.Context) error { return repo.StageHunk(ctx, f.rel, contents, h) })
	return nil
}

func (g *GitSigns) unstage(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	// The cursor's line in the index's numbering: past every unstaged hunk above it.
	line := p.Cursor().Line
	at := line
	for _, h := range hs {
		switch {
		case covers(h, line):
			at = h.OldStart - 1
		case signLine(h) < line:
			at += h.OldLines - h.NewLines
		}
	}
	repo := g.repo(f)
	kit.GitOf(g.h).Run(func(ctx context.Context) error {
		staged, err := repo.StagedHunks(ctx, f.rel)
		if err != nil {
			return err
		}
		h, ok := hunkAt(staged, at)
		if !ok {
			g.h.Notify(core.Notice{ID: "git.hunk.unstage", Severity: core.SevInfo, Key: true, Text: "No staged hunk on this line"})
			return nil
		}
		return repo.UnstageHunk(ctx, f.rel, h)
	})
	return nil
}

func (g *GitSigns) reset(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	line := h.NewStart - 1
	if h.NewLines == 0 {
		line = h.NewStart
	}
	_, err := p.Editor().Do([]text.Replace{kit.LineReplace(p.Buf(), line, h.NewLines, h.Old)}, 0)
	return err
}

func (g *GitSigns) preview(x *Editor, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	g.mu.Lock()
	defer g.mu.Unlock()
	defer g.h.DecorationsChanged("git.hunks")
	if cur, on := g.previews[f]; on && (!ok || cur == h.NewStart) {
		delete(g.previews, f)
		return nil
	}
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	g.previews[f] = h.NewStart
	return nil
}

func (g *GitSigns) jump(x *Editor, p vim.Pane, hs []gitx.Hunk, dir int) error {
	line := p.Cursor().Line
	starts := make([]int, len(hs))
	for i, h := range hs {
		starts[i] = signLine(h)
	}
	if dir < 0 {
		slices.Reverse(starts)
	}
	for _, s := range starts {
		if dir > 0 && s > line || dir < 0 && s < line {
			x.e.SetCursor(vim.Pos{Line: s})
			return nil
		}
	}
	return nil
}
