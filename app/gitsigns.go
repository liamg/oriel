package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"
)

var (
	tBlameYou   = theme.Declare("git.blame.you", "fg.ghost")
	tBlameAgent = theme.Declare("git.blame.agent", "alpha(brand, 70%)")
	tBlameOther = theme.Declare("git.blame.other", "fg.ghost")
)

// gitSigns is git in the editor (ide.md, "Git"): the gutter's hunks against the index, staging, unstaging and
// resetting the hunk under the cursor, ]c and [c between hunks, a preview of what a hunk replaced, and the cursor
// line's blame by author kind.
type gitSigns struct {
	disposer
	sh  *shell
	h   ext.Host
	now func() time.Time

	mu       sync.Mutex
	bases    map[string]*gitBase // the index's version of each file, by repo path
	blames   map[string]*gitBlame
	hunks    map[*text.Buffer]*bufHunks
	previews map[string]int // the hunk previewed in each file, by its new start
	busy     sync.WaitGroup
}

type gitBase struct {
	data    []byte
	loaded  bool
	gen     int
	loading bool
}

type gitBlame struct {
	loaded  bool
	version uint64
	lines   []gitx.BlameLine
	loading bool
}

type bufHunks struct {
	version uint64
	gen     int
	hunks   []gitx.Hunk
}

func (g *gitSigns) Manifest() ext.Manifest { return ext.Manifest{ID: "gitsigns"} }

func (g *gitSigns) Activate(_ context.Context, h ext.Host) error {
	g.h = h
	if g.now == nil {
		g.now = time.Now
	}
	g.bases, g.blames, g.hunks, g.previews = map[string]*gitBase{}, map[string]*gitBlame{}, map[*text.Buffer]*bufHunks{}, map[string]int{}
	g.add(h.Option(registry.OptionSpec{Name: "git.blame.inline", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser | registry.ScopeProject,
		Help: "Show who last changed the cursor's line, and when, after it."}))
	if st := g.sh.git.status; st != nil {
		g.add(disposeFn(st.Watch(func([]string) {
			g.mu.Lock()
			for _, b := range g.bases {
				b.loaded = false
			}
			for _, b := range g.blames {
				b.loaded = false
			}
			g.mu.Unlock()
			h.Redraw()
		})))
	}
	for _, c := range []registry.Command{
		{ID: "git.hunk.stage", Title: "Stage the hunk under the cursor", Run: g.cmd(g.stage)},
		{ID: "git.hunk.unstage", Title: "Unstage the staged hunk at the cursor", Run: g.cmd(g.unstage)},
		{ID: "git.hunk.reset", Title: "Put the hunk under the cursor back as the index has it", Run: g.cmd(g.reset)},
		{ID: "git.hunk.preview", Title: "Show what the hunk under the cursor replaced (toggle)", Run: g.cmd(g.preview)},
		{ID: "git.hunk.next", Title: "Go to the next hunk", Run: g.cmd(func(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
			return g.jump(x, p, hs, 1)
		})},
		{ID: "git.hunk.prev", Title: "Go to the previous hunk", Run: g.cmd(func(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
			return g.jump(x, p, hs, -1)
		})},
		{ID: "git.blame.toggle", Title: "Show or hide the cursor line's blame", Run: func(registry.Ctx, registry.Args) error {
			on, _, _ := h.Options().Get("git.blame.inline", h.Here())
			return h.Options().Set("git.blame.inline", registry.LayerUser, registry.At{}, on != true)
		}},
	} {
		g.add(h.Command(c))
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
		g.add(h.Keymap(k))
	}
	g.add(h.Decorations("git.hunks", registry.DecorationFunc(g.decorations)))
	return nil
}

func (g *gitSigns) Deactivate(ctx context.Context) error {
	g.busy.Wait()
	return g.disposer.Deactivate(ctx)
}

func (g *gitSigns) wait() { g.busy.Wait() }

// gitFile is a buffer's file as git names it.
type gitFile struct{ rel, abs string }

// fileOf is where path is in the repo, if it's in one and not ignored.
func (g *gitSigns) fileOf(path string) (gitFile, bool) {
	root := g.sh.git.root()
	if root == "" || path == "" {
		return gitFile{}, false
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(g.sh.cfg.WorkDir, path)
	}
	rel := g.sh.git.rel(path)
	if strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return gitFile{}, false
	}
	for _, f := range g.sh.git.state().Files(gitx.All) {
		if f.Ignored && (f.Path == rel || strings.HasSuffix(f.Path, "/") && strings.HasPrefix(rel, f.Path)) {
			return gitFile{}, false
		}
	}
	return gitFile{rel: rel, abs: path}, true
}

// hunksOf are the hunks from the index's version of f to buf's text as it would be written, or none while the
// index's is read.
func (g *gitSigns) hunksOf(f gitFile, buf *text.Buffer, fileBytes func() []byte) []gitx.Hunk {
	g.mu.Lock()
	b, ok := g.bases[f.rel]
	if !ok {
		b = &gitBase{}
		g.bases[f.rel] = b
	}
	if !b.loaded {
		if !b.loading {
			b.loading = true
			g.busy.Add(1)
			go func() {
				defer g.busy.Done()
				data, _ := g.sh.git.status.Repo().IndexFile(context.Background(), f.rel)
				g.h.Do(func() {
					g.mu.Lock()
					b.data, b.loaded, b.loading = data, true, false
					b.gen++
					g.mu.Unlock()
				})
			}()
		}
		g.mu.Unlock()
		return nil
	}
	snap := buf.Snapshot()
	bh := g.hunks[buf]
	if bh == nil || bh.version != snap.Version() || bh.gen != b.gen {
		bh = &bufHunks{version: snap.Version(), gen: b.gen, hunks: gitx.DiffLines(b.data, fileBytes())}
		g.hunks[buf] = bh
	}
	g.mu.Unlock()
	return bh.hunks
}

// signLine is the 0-based line a hunk's sign starts on: a deletion's is the line before it.
func signLine(h gitx.Hunk) int { return max(h.NewStart-1, 0) }

func covers(h gitx.Hunk, line int) bool {
	if h.NewLines == 0 {
		return line == signLine(h)
	}
	return line >= h.NewStart-1 && line < h.NewStart-1+h.NewLines
}

func (g *gitSigns) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	f, ok := g.fileOf(ctx.Path)
	if !ok {
		return nil
	}
	var out []registry.Decoration
	hs := g.hunksOf(f, ctx.Buffer, ctx.FileBytes)
	g.mu.Lock()
	previewed, previewing := g.previews[f.rel]
	g.mu.Unlock()
	for _, h := range hs {
		style := "git." + h.Kind().String()
		if h.Kind() == gitx.Deleted {
			if l := signLine(h); l >= ctx.Top && l < ctx.Bottom {
				out = append(out, registry.Decoration{Kind: registry.Sign, Line: l, Style: style, Text: "▁", Priority: 5})
			}
		}
		for l := h.NewStart - 1; l < h.NewStart-1+h.NewLines; l++ {
			if l >= ctx.Top && l < ctx.Bottom {
				out = append(out, registry.Decoration{Kind: registry.Sign, Line: l, Style: style, Text: "▎", Priority: 5})
			}
		}
		if previewing && previewed == h.NewStart && len(h.Old) > 0 {
			end := signLine(h)
			if h.NewLines > 0 {
				end = h.NewStart - 2 + h.NewLines
			}
			out = append(out, registry.Decoration{Kind: registry.Block, Line: end, Node: &ui.Func{Key: "hunk", Fn: func(c *ui.Ctx) {
				diffBlock(c, "diff", h, false)
			}}, Priority: 5})
		}
	}
	if line, ok := g.blameLine(ctx, f); ok {
		out = append(out, line)
	}
	return out
}

// blameLine is the cursor line's blame, after it, while the pane is the current one.
func (g *gitSigns) blameLine(ctx registry.DecorationCtx, f gitFile) (registry.Decoration, bool) {
	x := g.sh.editor
	if x == nil {
		return registry.Decoration{}, false
	}
	if on, _, _ := g.h.Options().Get("git.blame.inline", g.h.Here()); on != true {
		return registry.Decoration{}, false
	}
	p := x.current()
	if p.Buffer() != ctx.Buffer || x.e.Mode() != "normal" {
		return registry.Decoration{}, false
	}
	line := p.Cursor().Line
	if line < ctx.Top || line >= ctx.Bottom {
		return registry.Decoration{}, false
	}
	snap := ctx.Snap
	g.mu.Lock()
	b, ok := g.blames[f.rel]
	if !ok {
		b = &gitBlame{}
		g.blames[f.rel] = b
	}
	if !b.loaded || b.version != snap.Version() {
		if !b.loading {
			b.loading = true
			g.busy.Add(1)
			version, contents := snap.Version(), ctx.FileBytes()
			go func() {
				defer g.busy.Done()
				lines, err := g.sh.git.status.Repo().Blame(context.Background(), f.rel, contents)
				g.h.Do(func() {
					g.mu.Lock()
					// A file blame can't read (untracked, say) waits for an edit or a git change to try again, not the next frame.
					b.loading, b.version, b.lines, b.loaded = false, version, lines, true
					if err != nil {
						b.lines = nil
					}
					g.mu.Unlock()
					g.h.Redraw()
				})
			}()
		}
		g.mu.Unlock()
		return registry.Decoration{}, false
	}
	defer g.mu.Unlock()
	if line >= len(b.lines) {
		return registry.Decoration{}, false
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
		s += " · " + ago(g.now(), bl.When) + " ago · " + bl.Summary
	}
	return registry.Decoration{Kind: registry.VirtualText, Line: line, Text: s, Style: style, Priority: -1}, true
}

// cmd runs fn on the current editor window's file and hunks; outside a repo it says so.
func (g *gitSigns) cmd(fn func(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error) func(registry.Ctx, registry.Args) error {
	return func(registry.Ctx, registry.Args) error {
		x := g.sh.editor
		if x == nil {
			return fmt.Errorf("no editor")
		}
		p := x.current()
		f, ok := g.fileOf(p.Name())
		if !ok {
			return fmt.Errorf("%s isn't in a git repository", cmpOr(p.Name(), "[No Name]"))
		}
		return fn(x, p, f, g.hunksOf(f, p.Buffer(), p.FileBytes))
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

func (g *gitSigns) stage(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	contents := p.FileBytes()
	g.sh.git.async(func(ctx context.Context) error { return g.sh.git.status.Repo().StageHunk(ctx, f.rel, contents, h) })
	return nil
}

func (g *gitSigns) unstage(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
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
	g.sh.git.async(func(ctx context.Context) error {
		repo := g.sh.git.status.Repo()
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

func (g *gitSigns) reset(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	line := h.NewStart - 1
	if h.NewLines == 0 {
		line = h.NewStart
	}
	_, err := p.Editor().Do([]text.Replace{lineReplace(p.Buf(), line, h.NewLines, h.Old)}, 0)
	return err
}

func (g *gitSigns) preview(x *editorExt, p vim.Pane, f gitFile, hs []gitx.Hunk) error {
	h, ok := hunkAt(hs, p.Cursor().Line)
	g.mu.Lock()
	defer g.mu.Unlock()
	if cur, on := g.previews[f.rel]; on && (!ok || cur == h.NewStart) {
		delete(g.previews, f.rel)
		return nil
	}
	if !ok {
		return fmt.Errorf("no hunk on this line")
	}
	g.previews[f.rel] = h.NewStart
	return nil
}

func (g *gitSigns) jump(x *editorExt, p vim.Pane, hs []gitx.Hunk, dir int) error {
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
