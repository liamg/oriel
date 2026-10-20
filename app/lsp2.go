package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/search"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
)

// PeekKeyMode is the references peek's key layer.
const PeekKeyMode = "lsp.peek"

var tPeekBg = theme.Declare("lsp.peek.bg", "bg.surface")

// refPeek is grr's inline peek at the references of the symbol on a line.
type refPeek struct {
	path   string
	line   int
	name   string
	refs   []lsp.Location
	texts  []string       // each ref's line
	counts map[string]int // the refs in each file
	srcs   fileSrcs       // the refs' files
	sel    int
	shown  int // the selection the list last scrolled to
}

// renamePrompt is grn's prompt for the new name.
type renamePrompt struct {
	path  string
	at    text.Pos
	old   string
	value string
	c     *lsp.Client
}

// navigation registers LS-2: go to definition and its kin, the references peek, rename, symbols and calls.
// slowAnswer is how long a request the user waits on goes before they're told it's waiting.
const slowAnswer = 400 * time.Millisecond

func (x *lspPlugin) navigation(h plugin.Host) {
	goTo := func(what string, find func(c *lsp.Client, ctx context.Context, at lsp.Loc) ([]lsp.Location, error), vimKeys string) func(registry.Ctx, registry.Args) error {
		var run func(ctx registry.Ctx, _ registry.Args) error
		run = func(ctx registry.Ctx, args registry.Args) error {
			p, path, d := x.current()
			if x.whenStarted(d, what, func() error { return run(ctx, args) }) {
				return nil
			}
			if d == nil || d.client == nil {
				if vimKeys != "" {
					return h.Run(ctx, "editor.keys", registry.Args{"keys": vimKeys, "noremap": true})
				}
				return fmt.Errorf("no language server for this file")
			}
			at, c := lsp.Loc{Path: path, Pos: text.Pos(p.Cursor())}, d.client
			answered := x.waiting(c)
			async(x, func(ctx context.Context) ([]lsp.Location, error) { return find(c, ctx, at) }, func(ls []lsp.Location, err error) {
				answered()
				switch {
				case err != nil:
					h.Notify(core.Notice{ID: "lsp." + what, Severity: core.SevWarning, Key: true, Text: err.Error()})
				case len(ls) == 0:
					h.Notify(core.Notice{ID: "lsp." + what, Severity: core.SevInfo, Key: true, Text: "No " + what + " found"})
				case len(ls) == 1:
					x.jump(ls[0])
				default:
					x.toQuickfix(what, ls)
				}
			})
			return nil
		}
		return run
	}
	for _, c := range []registry.Command{
		{ID: "lsp.definition", Title: "Go to the definition", Run: goTo("definition", (*lsp.Client).Definition, "gd")},
		{ID: "lsp.declaration", Title: "Go to the declaration", Run: goTo("declaration", (*lsp.Client).Declaration, "gD")},
		{ID: "lsp.type_definition", Title: "Go to the type's definition", Run: goTo("type definition", (*lsp.Client).TypeDefinition, "")},
		{ID: "lsp.implementation", Title: "Go to the implementations", Run: goTo("implementation", (*lsp.Client).Implementation, "")},
		{ID: "lsp.references", Title: "Peek at the references to the symbol under the cursor", Run: func(registry.Ctx, registry.Args) error {
			return x.references()
		}},
		{ID: "peek.next", Title: "Highlight the next reference", Run: func(registry.Ctx, registry.Args) error { x.peekMove(1); return nil }},
		{ID: "peek.prev", Title: "Highlight the previous reference", Run: func(registry.Ctx, registry.Args) error { x.peekMove(-1); return nil }},
		{ID: "peek.open", Title: "Go to the highlighted reference", Run: func(registry.Ctx, registry.Args) error {
			if x.peek != nil {
				l := x.peek.refs[x.peek.sel]
				x.peek = nil
				x.jump(l)
			}
			return nil
		}},
		{ID: "peek.close", Title: "Close the references peek", Run: func(registry.Ctx, registry.Args) error { x.peek = nil; return nil }},
		{ID: "lsp.rename", Title: "Rename the symbol under the cursor everywhere, after a preview", Run: func(registry.Ctx, registry.Args) error {
			return x.startRename()
		}},
		{ID: "lsp.calls.incoming", Title: "List what calls the function under the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.calls(ctx, true)
		}},
		{ID: "lsp.calls.outgoing", Title: "List what the function under the cursor calls", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return x.calls(ctx, false)
		}},
	} {
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Mode: "n", Keys: "gd", Command: "lsp.definition", Desc: "Definition"},
		{Mode: "n", Keys: "gD", Command: "lsp.declaration", Desc: "Declaration"},
		{Mode: "n", Keys: "gy", Command: "lsp.type_definition", Desc: "Type definition"},
		{Mode: "n", Keys: "gri", Command: "lsp.implementation", Desc: "Implementations"},
		{Mode: "n", Keys: "grr", Command: "lsp.references", Desc: "References"},
		{Mode: "n", Keys: "grn", Command: "lsp.rename", Desc: "Rename"},
		{Mode: "n", Keys: "gra", Command: "lsp.code_actions", Desc: "Code actions"},
		{Mode: "n", Keys: "gO", Command: "picker.open", Args: registry.Args{"source": "symbols.file"}, Desc: "Symbols here"},
		{Mode: "n", Keys: "<Leader>ci", Command: "lsp.calls.incoming", Desc: "Incoming calls"},
		{Mode: "n", Keys: "<Leader>co", Command: "lsp.calls.outgoing", Desc: "Outgoing calls"},
		{Mode: PeekKeyMode, Keys: "j", Command: "peek.next", Desc: "Next"},
		{Mode: PeekKeyMode, Keys: "<Down>", Command: "peek.next", Desc: "Next"},
		{Mode: PeekKeyMode, Keys: "k", Command: "peek.prev", Desc: "Previous"},
		{Mode: PeekKeyMode, Keys: "<Up>", Command: "peek.prev", Desc: "Previous"},
		{Mode: PeekKeyMode, Keys: "<CR>", Command: "peek.open", Desc: "Go there"},
		{Mode: PeekKeyMode, Keys: "q", Command: "peek.close", Desc: "Close"},
		{Mode: PeekKeyMode, Keys: "<Esc>", Command: "peek.close", Desc: "Close"},
	} {
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(registry.KeyLayer{Name: "lsp.peek", Mode: PeekKeyMode, Active: func() bool { return x.peek != nil }}))
	x.symbolSources(h)
	x.substituteHints(h)
}

// whenStarted runs again once d's server has started, saying it's starting meanwhile; it reports whether it waits.
func (x *lspPlugin) whenStarted(d *lspDoc, what string, again func() error) bool {
	if d == nil || !d.opening {
		return false
	}
	const id = "lsp.starting"
	x.h.Notify(core.Notice{ID: id, Severity: core.SevInfo, Key: true, Text: "Starting the " + d.lang + " language server…"})
	d.ready = append(d.ready, func() {
		x.h.Dismiss(id)
		if d.client == nil {
			x.h.Notify(core.Notice{ID: "lsp." + what, Severity: core.SevWarning, Key: true, Text: "The " + d.lang + " language server didn't start: :LspInfo says why"})
			return
		}
		if err := again(); err != nil {
			x.h.Notify(core.Notice{ID: "lsp." + what, Severity: core.SevWarning, Key: true, Text: err.Error()})
		}
	})
	return true
}

// waiting says a request is waiting on c once it has gone slowAnswer unanswered; call what it returns with the answer.
func (x *lspPlugin) waiting(c *lsp.Client) (answered func()) {
	slow := time.AfterFunc(slowAnswer, func() {
		x.h.Notify(core.Notice{ID: "lsp.waiting", Severity: core.SevInfo, Key: true, Text: "Waiting for " + c.Status().Server + " (" + c.Status().State.String() + ")…"})
	})
	return func() {
		if !slow.Stop() {
			x.h.Dismiss("lsp.waiting")
		}
	}
}

// jump goes to a location as a vim jump in the current window, so ⌃O comes back.
func (x *lspPlugin) jump(l lsp.Location) {
	r := l.Range
	if l.Target != nil {
		r = *l.Target
	}
	ed := x.sh.editor
	name := l.Path
	if rel, err := filepath.Rel(ed.dir, l.Path); err == nil && filepath.IsAbs(l.Path) && !strings.HasPrefix(rel, "..") {
		name = rel
	}
	if err := ed.e.JumpTo(name, vim.Pos{Line: r.Start.Line, Col: r.Start.Col}); err != nil {
		x.h.Notify(core.Notice{ID: "lsp.jump", Severity: core.SevWarning, Key: true, Text: err.Error()})
		return
	}
	ed.conv.refresh()
}

// toQuickfix lists locations in the quickfix list, and shows it.
func (x *lspPlugin) toQuickfix(title string, ls []lsp.Location) {
	var items []map[string]any
	for _, l := range ls {
		items = append(items, map[string]any{"path": l.Path, "line": l.Range.Start.Line + 1, "col": l.Range.Start.Col + 1, "text": lineOf(x, l)})
	}
	ctx := context.Background()
	if err := x.h.Run(ctx, "quickfix.set", registry.Args{"title": title, "items": items}); err == nil {
		x.h.Run(ctx, "quickfix.open", nil)
	}
}

// fileText is a file's text as the server sees it: an open buffer's, or what's on disk.
func (x *lspPlugin) fileText(path string) []byte {
	if f, ok := x.sh.editor.openBuffer(path); ok {
		return f.Text.Snapshot().Bytes()
	}
	b, _ := os.ReadFile(path)
	return b
}

func lineOf(x *lspPlugin, l lsp.Location) string { return lineIn(x, fileSrcs{}, l) }

// lineIn is l's line, its file's text kept in srcs.
func lineIn(x *lspPlugin, srcs fileSrcs, l lsp.Location) string {
	lines := srcs.of(x.sh.editor, l.Path, false).lines()
	if n := l.Range.Start.Line; n >= 0 && n < len(lines) {
		return strings.TrimSpace(lines[n])
	}
	return ""
}

func (x *lspPlugin) references() error {
	p, path, d := x.current()
	if x.whenStarted(d, "references", x.references) {
		return nil
	}
	if d == nil || d.client == nil {
		return fmt.Errorf("no language server for this file")
	}
	pos := p.Cursor()
	line := p.Buf().Line(pos.Line)
	name := string(line[wordStart(line, min(pos.Col, len(line))):])
	if i := strings.IndexFunc(name, func(r rune) bool { return !isWordRune(r) }); i >= 0 {
		name = name[:i]
	}
	at, c := lsp.Loc{Path: path, Pos: text.Pos(pos)}, d.client
	answered := x.waiting(c)
	async(x, func(ctx context.Context) ([]lsp.Location, error) { return c.References(ctx, at, true) }, func(ls []lsp.Location, err error) {
		answered()
		if err != nil || len(ls) == 0 {
			text := "No references found"
			if err != nil {
				text = err.Error()
			}
			x.h.Notify(core.Notice{ID: "lsp.references", Severity: core.SevInfo, Key: true, Text: text})
			return
		}
		slices.SortStableFunc(ls, func(a, b lsp.Location) int {
			if a.Path != b.Path {
				return strings.Compare(a.Path, b.Path)
			}
			return a.Range.Start.Line - b.Range.Start.Line
		})
		pk := &refPeek{path: path, line: pos.Line, name: name, refs: ls, texts: make([]string, len(ls)), counts: map[string]int{}, srcs: fileSrcs{},
			shown: -1}
		for i, l := range ls {
			pk.texts[i] = lineIn(x, pk.srcs, l)
			pk.counts[l.Path]++
		}
		x.peek = pk
	})
	return nil
}

func (x *lspPlugin) peekMove(d int) {
	if x.peek != nil {
		n := len(x.peek.refs)
		x.peek.sel = (x.peek.sel + d + n) % n
	}
}

// peekDecoration is the peek as a block under the line it's on.
func (x *lspPlugin) peekDecoration(ctx registry.DecorationCtx) []registry.Decoration {
	pk := x.peek
	if pk == nil || pk.path != ctx.Path || pk.line < ctx.Top || pk.line >= ctx.Bottom {
		return nil
	}
	return []registry.Decoration{{Kind: registry.Block, Line: pk.line, Node: &ui.Func{Key: "peek", Fn: func(c *ui.Ctx) { x.peekView(c, pk) }},
		Priority: 20, Reveal: true}}
}

// peekView is the highlighted reference's code beside every reference, by file (09-lsp).
func (x *lspPlugin) peekView(c *ui.Ctx, pk *refPeek) {
	th := c.Theme()
	sel := pk.refs[pk.sel]
	root := x.sh.editor.dir
	rel := func(p string) string {
		if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
			return r
		}
		return p
	}
	c.Row("peek", ui.Style{W: ui.Flex(1), H: ui.Px(220), Fill: th.C(tPeekBg), Stroke: th.C(tPopupLine), Border: 1, Radius: 6, Clip: true,
		Help: "editor/pane#references", Role: ui.RoleRegion, Label: "References"}, func(c *ui.Ctx) {
		c.Column("code", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(10, 8), Gap: 6}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("f", filepath.Base(sel.Path), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5, Weight: typeset.SemiBold},
					Color: th.C(theme.Fg)})
				c.Text("d", filepath.Dir(rel(sel.Path)), monoTok(c, 10.5, theme.FgFaint))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("n", fmt.Sprintf("%d / %d", pk.sel+1, len(pk.refs)), monoTok(c, 10.5, theme.FgFaint))
			})
			f := pk.srcs.of(x.sh.editor, sel.Path, true).around(x.h, sel.Path, sel.Range.Start.Line+1, 2, 6)
			f.mark, f.title, f.size = true, rel(sel.Path), 12
			f.build(c, nil)
		})
		c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
		listID := c.Column("list", ui.Style{W: ui.Px(300), H: ui.Flex(1), Pad: ui.PadXY(0, 8), ScrollY: true}, func(c *ui.Ctx) {
			c.Row("count", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 2)}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("%d references to %s", len(pk.refs), pk.name), monoTok(c, 10, theme.FgFaint))
			})
			file := ""
			for i, l := range pk.refs {
				if l.Path != file {
					file = l.Path
					n := pk.counts[file]
					c.Row(fmt.Sprint("file", i), ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 10, R: 10, T: 6}, Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("f", filepath.Base(file), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: th.C(theme.Fg)})
						c.Text("n", fmt.Sprint(n), monoTok(c, 10, theme.FgFaint))
					})
				}
				fill := render.Color{}
				if i == pk.sel {
					fill = th.C(tCompSel)
				}
				in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(20), Pad: ui.Insets{L: 22, R: 10}, Gap: 8, Cross: ui.Center,
					Fill: fill, Clip: true, Label: pk.texts[i], Help: "editor/pane#references"}, func(c *ui.Ctx) {
					c.Text("l", fmt.Sprint(l.Range.Start.Line+1), monoTok(c, 10.5, theme.FgGhost))
					c.Text("t", pk.texts[i], ui.TextStyle{Font: codeMono, Color: th.C(theme.FgMuted)})
				})
				if in.Clicked {
					pk.sel = i
				}
			}
		})
		if pk.shown != pk.sel {
			c.ScrollIntoView(listID, ui.Child(listID, pk.sel))
			pk.shown = pk.sel
		}
	})
}

func (x *lspPlugin) startRename() error {
	p, path, d := x.current()
	if x.whenStarted(d, "rename", x.startRename) {
		return nil
	}
	if d == nil || d.client == nil {
		return fmt.Errorf("no language server for this file")
	}
	pos := p.Cursor()
	at, c := lsp.Loc{Path: path, Pos: text.Pos(pos)}, d.client
	line := p.Buf().Line(pos.Line)
	word := string(line[wordStart(line, min(pos.Col, len(line))):])
	if i := strings.IndexFunc(word, func(r rune) bool { return !isWordRune(r) }); i >= 0 {
		word = word[:i]
	}
	answered := x.waiting(c)
	async(x, func(ctx context.Context) (*lsp.PrepareRename, error) { return c.PrepareRename(ctx, at) }, func(pr *lsp.PrepareRename, err error) {
		answered()
		switch {
		case errors.Is(err, lsp.ErrNotSupported):
			err = fmt.Errorf("%s can't rename", c.Status().Server)
		case err == nil && pr == nil:
			err = fmt.Errorf("%s can't rename what's under the cursor", c.Status().Server)
		}
		if err != nil {
			x.h.Notify(core.Notice{ID: "lsp.rename", Severity: core.SevWarning, Key: true, Text: err.Error()})
			return
		}
		old := word
		if pr.Placeholder != "" {
			old = pr.Placeholder
		}
		x.rename = &renamePrompt{path: path, at: at.Pos, old: old, value: old, c: c}
	})
	return nil
}

// renameInput is the prompt at the cursor; Enter asks the server, then shows the rename as a diff.
func (x *lspPlugin) renameInput(c *ui.Ctx, at render.Point) {
	rp := x.rename
	id := ui.Child(ui.Child(c.Next("rename"), "card"), "in")
	if !c.FocusWithin(id) && c.Interaction(id).Keys == nil {
		c.Focus(id)
	}
	c.Box("rename", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
		popup(c, "card", ui.Style{W: ui.Px(320), Pad: ui.Pad(8), Gap: 6, Help: "editor/pane#rename"}, func(c *ui.Ctx) {
			c.Text("t", "RENAME "+rp.old, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .12},
				Color: c.Theme().C(theme.FgFaint)})
			res := widget.Input(c, "in", &rp.value, widget.InputOpts{Label: "New name", SelectOnFocus: true, Help: "editor/pane#rename"})
			switch {
			case res.Cancelled:
				x.rename = nil
			case res.Submitted:
				x.rename = nil
				if rp.value != "" && rp.value != rp.old {
					x.doRename(rp)
				}
			}
		})
	})
}

func (x *lspPlugin) doRename(rp *renamePrompt) {
	async(x, func(ctx context.Context) (*lsp.WorkspaceEdit, error) {
		return rp.c.Rename(ctx, lsp.Loc{Path: rp.path, Pos: rp.at}, rp.value)
	}, func(e *lsp.WorkspaceEdit, err error) {
		if err == nil && e == nil {
			err = fmt.Errorf("the server can't rename this")
		}
		if err == nil {
			err = x.previewEdit(fmt.Sprintf("Rename %s → %s", rp.old, rp.value), rp.c.Spec().Name, *e)
		}
		if err != nil {
			x.h.Notify(core.Notice{ID: "lsp.rename", Severity: core.SevError, Key: true, Text: err.Error()})
		}
	})
}

// previewEdit shows a workspace edit as a diff of each file, which Apply makes. Edits that create, rename or delete
// files are made as they are, without one.
func (x *lspPlugin) previewEdit(title, detail string, e lsp.WorkspaceEdit) error {
	var plan []search.FileEdit
	count := 0
	for _, ch := range e.Changes {
		if ch.Op != "" {
			return x.applyEdit(e)
		}
		var old, nw []byte
		if f, ok := x.sh.editor.openBuffer(ch.Path); ok {
			snap := f.Text.Snapshot()
			ed, err := lsp.EditFor(snap, ch.Edits, text.Author{Kind: text.LSP})
			if err != nil {
				return err
			}
			tmp := text.New(snap.Bytes(), text.Author{})
			if _, err := tmp.Apply(ed); err != nil {
				return err
			}
			var format edit.Format
			if f.File != nil {
				format = f.File.Format
			}
			old, nw = f.Bytes(), edit.Encode(tmp.Snapshot().Bytes(), format, false)
		} else {
			b, err := os.ReadFile(ch.Path)
			if err != nil {
				return err
			}
			tmp := text.New(b, text.Author{})
			ed, err := lsp.EditFor(tmp.Snapshot(), ch.Edits, text.Author{Kind: text.LSP})
			if err != nil {
				return err
			}
			if _, err := tmp.Apply(ed); err != nil {
				return err
			}
			old, nw = b, tmp.Snapshot().Bytes()
		}
		count += len(ch.Edits)
		plan = append(plan, search.FileEdit{Path: ch.Path, Old: old, New: nw, Hunks: gitx.DiffLines(old, nw), Count: len(ch.Edits)})
	}
	if len(plan) == 0 {
		return fmt.Errorf("nothing to change")
	}
	return x.sh.search.show(context.Background(), &editPlan{title: title,
		detail: fmt.Sprintf("%d %s in %d %s · %s", count, plural(count, "change", "changes"), len(plan), plural(len(plan), "file", "files"), detail),
		edits:  plan})
}

// symbolSources make the symbols pickers ask the servers: this file's symbols, and the workspace's as typed, with
// the editor's own outline where no server answers.
func (x *lspPlugin) symbolSources(h plugin.Host) {
	var file, all registry.PickerSource
	for _, s := range h.Pickers() {
		switch s.ID {
		case "symbols.file":
			file = s
		case "symbols":
			all = s
		}
	}
	if file.ID != "" {
		type answer struct {
			path    string
			version uint64
			items   []registry.PickerItem
			ok      bool // the server answered with symbols
		}
		var got answer
		asking := false
		mine := file
		mine.Items = func(ctx registry.Ctx, q string) []registry.PickerItem {
			p, path, d := x.current()
			if d == nil || d.client == nil {
				return file.Items(ctx, q)
			}
			version := p.Buf().Version()
			if got.path != path || got.version != version {
				if !asking {
					asking = true
					c := d.client
					async(x, func(ctx context.Context) ([]lsp.Symbol, error) { return c.DocumentSymbols(ctx, path) }, func(syms []lsp.Symbol, err error) {
						asking = false
						got = answer{path: path, version: version, items: x.symbolItems(syms), ok: err == nil && len(syms) > 0}
						x.h.Redraw()
					})
				}
				return file.Items(ctx, q)
			}
			if !got.ok {
				return file.Items(ctx, q)
			}
			return got.items
		}
		mine.Loading = func() bool { return asking }
		mine.Source = "builtin:lsp"
		x.add(h.Picker(mine))
	}
	if all.ID != "" {
		type result struct {
			q     string
			items []registry.PickerItem
		}
		var last result
		asked := ""
		mine := all
		mine.Live = true
		mine.Items = func(ctx registry.Ctx, q string) []registry.PickerItem {
			clients := x.m.Clients()
			if len(clients) == 0 || q == "" {
				return all.Items(ctx, q)
			}
			if last.q == q {
				return last.items
			}
			if asked != q {
				asked = q
				async(x, func(ctx context.Context) ([]lsp.Symbol, error) {
					var syms []lsp.Symbol
					for _, c := range clients {
						if s, err := c.WorkspaceSymbols(ctx, q); err == nil {
							syms = append(syms, s...)
						}
					}
					return syms, nil
				}, func(syms []lsp.Symbol, _ error) {
					if asked == q {
						last, asked = result{q, x.symbolItems(syms)}, ""
					}
					x.h.Redraw()
				})
			}
			return all.Items(ctx, q)
		}
		mine.Loading = func() bool { return asked != "" }
		mine.Source = "builtin:lsp"
		x.add(h.Picker(mine))
	}
}

func (x *lspPlugin) symbolItems(syms []lsp.Symbol) []registry.PickerItem {
	var out []registry.PickerItem
	var walk func(ss []lsp.Symbol, container string)
	walk = func(ss []lsp.Symbol, container string) {
		for _, s := range ss {
			path := s.Path
			if r, err := filepath.Rel(x.sh.editor.dir, path); err == nil && !strings.HasPrefix(r, "..") {
				path = r
			}
			detail := s.Kind.String()
			if container != "" {
				detail += " · " + container
			}
			out = append(out, registry.PickerItem{ID: fmt.Sprintf("%s:%d:%s", path, s.Selection.Start.Line, s.Name), Label: s.Name, Detail: detail,
				Note: filepath.Base(path), IconStyle: "status.working", Path: path, Line: s.Selection.Start.Line + 1, Col: s.Selection.Start.Col + 1})
			walk(s.Children, s.Name)
		}
	}
	walk(syms, "")
	return out
}

// calls lists the incoming or outgoing calls of the function under the cursor in the lsp.calls picker.
func (x *lspPlugin) calls(ctx registry.Ctx, incoming bool) error {
	p, path, d := x.current()
	if x.whenStarted(d, "calls", func() error { return x.calls(ctx, incoming) }) {
		return nil
	}
	if d == nil || d.client == nil {
		return fmt.Errorf("no language server for this file")
	}
	at, c := lsp.Loc{Path: path, Pos: text.Pos(p.Cursor())}, d.client
	answered := x.waiting(c)
	async(x, func(ctx context.Context) ([]registry.PickerItem, error) {
		items, err := c.PrepareCallHierarchy(ctx, at)
		if err != nil || len(items) == 0 {
			return nil, err
		}
		var calls []lsp.Call
		if incoming {
			calls, err = c.IncomingCalls(ctx, items[0])
		} else {
			calls, err = c.OutgoingCalls(ctx, items[0])
		}
		var out []registry.PickerItem
		for _, call := range calls {
			path := call.Item.Path
			if r, err := filepath.Rel(x.sh.editor.dir, path); err == nil && !strings.HasPrefix(r, "..") {
				path = r
			}
			out = append(out, registry.PickerItem{ID: fmt.Sprintf("%s:%d", path, call.Item.Selection.Start.Line), Label: call.Item.Name,
				Detail: call.Item.Kind.String() + " · " + call.Item.Detail, Note: filepath.Base(path), IconStyle: "status.working", Path: path,
				Line: call.Item.Selection.Start.Line + 1, Col: call.Item.Selection.Start.Col + 1})
		}
		return out, err
	}, func(items []registry.PickerItem, err error) {
		answered()
		if err != nil {
			x.h.Notify(core.Notice{ID: "lsp.calls", Severity: core.SevWarning, Key: true, Text: err.Error()})
			return
		}
		x.callItems = items
		x.h.Run(context.Background(), "picker.open", registry.Args{"source": "lsp.calls"})
	})
	return nil
}

// callsSource is the call hierarchy's last answer, as a picker.
func (x *lspPlugin) callsSource() registry.PickerSource {
	return registry.PickerSource{ID: "lsp.calls", Title: "Calls", Order: 45, PickerOnly: true, Unlisted: true,
		Items: func(registry.Ctx, string) []registry.PickerItem { return x.callItems },
		Preview: func(it registry.PickerItem) ui.Node {
			return x.sh.editor.filePreview(it.Path, it.Line-1)
		},
	}
}

// substituteRe is a :s of one whole word, which a rename can do better.
var substituteRe = regexp.MustCompile(`^:(?:%|[0-9.,$]*)s/\\<(\w+)\\>/(\w*)/`)

// substitution is the word a :s on the command line replaces and what with, when a server could rename it instead.
func (x *lspPlugin) substitution() (old, new string, ok bool) {
	line, open := x.sh.editor.e.Cmdline()
	if !open {
		return "", "", false
	}
	m := substituteRe.FindStringSubmatch(line)
	if m == nil || m[2] == "" {
		return "", "", false
	}
	if _, _, d := x.current(); d == nil || d.client == nil || d.client.Capabilities().RenameProvider == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// substituteHints offer the rename while a :s of a whole word is typed (09-lsp), with how many matches the :s has.
func (x *lspPlugin) substituteHints(h plugin.Host) {
	x.add(h.Contribute(registry.CmdlineHint{Name: "lsp.rename", Hint: func(string) string {
		old, _, ok := x.substitution()
		if !ok {
			return ""
		}
		n := len(regexp.MustCompile(`\b`+regexp.QuoteMeta(old)+`\b`).FindAllIndex(x.sh.editor.current().Buf().Bytes(), -1))
		return fmt.Sprintf("%d %s · ⇥ use LSP rename", n, plural(n, "match", "matches"))
	}}))
	x.add(h.Contribute(registry.KeyLayer{Name: "lsp.substitute", Mode: "lsp.substitute", Active: func() bool { _, _, ok := x.substitution(); return ok }}))
	x.add(h.Keymap(registry.Keymap{Mode: "lsp.substitute", Keys: "<Tab>", Command: "lsp.rename.substitute", Desc: "Use LSP rename"}))
	x.add(h.Command(registry.Command{ID: "lsp.rename.substitute", Title: "Rename the word the :s on the command line replaces, with the language server",
		Run: func(registry.Ctx, registry.Args) error {
			old, nw, ok := x.substitution()
			if !ok {
				return fmt.Errorf("the command line isn't a :s of a whole word")
			}
			e := x.sh.editor.e
			e.Key("<Esc>")
			p, path, d := x.current()
			b := p.Buf().Bytes()
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(old) + `\b`)
			snap := p.Buf()
			from := snap.Offset(text.Pos(p.Cursor()))
			at := -1
			for _, loc := range re.FindAllIndex(b, -1) {
				if loc[1] > from && at < 0 || loc[0] <= from && from < loc[1] {
					at = loc[0]
					break
				}
			}
			if at < 0 {
				if locs := re.FindAllIndex(b, 1); len(locs) > 0 {
					at = locs[0][0]
				}
			}
			if at < 0 {
				return fmt.Errorf("no %s in this file", old)
			}
			x.doRename(&renamePrompt{path: path, at: snap.PosAt(at), old: old, value: nw, c: d.client})
			return nil
		}}))
}
