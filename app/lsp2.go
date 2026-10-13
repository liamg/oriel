package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/worker/search"
)

// PeekKeyMode is the references peek's key layer.
const PeekKeyMode = "lsp.peek"

var tPeekBg = theme.Declare("lsp.peek.bg", "bg.surface")

// refPeek is grr's inline peek at the references of the symbol on a line.
type refPeek struct {
	path string
	line int
	name string
	refs []lsp.Location
	sel  int
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
func (x *lspExt) navigation(h ext.Host) {
	goTo := func(what string, find func(c *lsp.Client, ctx context.Context, at lsp.Loc) ([]lsp.Location, error), vimKeys string) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, _ registry.Args) error {
			p, path, d := x.current()
			if d == nil || d.client == nil {
				if vimKeys != "" {
					return h.Run(ctx, "editor.keys", registry.Args{"keys": vimKeys, "noremap": true})
				}
				return fmt.Errorf("no language server for this file")
			}
			at, c := lsp.Loc{Path: path, Pos: text.Pos(p.Cursor())}, d.client
			async(x, func(ctx context.Context) ([]lsp.Location, error) { return find(c, ctx, at) }, func(ls []lsp.Location, err error) {
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
		{Mode: "n", Keys: "<Leader>rn", Command: "lsp.rename", Desc: "Rename"},
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
}

// jump opens a location's file at its start.
func (x *lspExt) jump(l lsp.Location) {
	r := l.Range
	if l.Target != nil {
		r = *l.Target
	}
	if err := x.h.Run(context.Background(), "editor.open", registry.Args{"path": l.Path, "line": r.Start.Line + 1}); err != nil {
		x.h.Notify(core.Notice{ID: "lsp.jump", Severity: core.SevWarning, Key: true, Text: err.Error()})
		return
	}
	x.sh.editor.e.SetCursor(vim.Pos{Line: r.Start.Line, Col: r.Start.Col})
}

// toQuickfix lists locations in the quickfix list, and shows it.
func (x *lspExt) toQuickfix(title string, ls []lsp.Location) {
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
func (x *lspExt) fileText(path string) []byte {
	if f, ok := x.sh.editor.openBuffer(path); ok {
		return f.Text.Snapshot().Bytes()
	}
	b, _ := os.ReadFile(path)
	return b
}

func lineOf(x *lspExt, l lsp.Location) string {
	lines := strings.Split(string(x.fileText(l.Path)), "\n")
	if l.Range.Start.Line < len(lines) {
		return strings.TrimSpace(lines[l.Range.Start.Line])
	}
	return ""
}

func (x *lspExt) references() error {
	p, path, d := x.current()
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
	async(x, func(ctx context.Context) ([]lsp.Location, error) { return c.References(ctx, at, true) }, func(ls []lsp.Location, err error) {
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
		x.peek = &refPeek{path: path, line: pos.Line, name: name, refs: ls}
	})
	return nil
}

func (x *lspExt) peekMove(d int) {
	if x.peek != nil {
		n := len(x.peek.refs)
		x.peek.sel = (x.peek.sel + d + n) % n
	}
}

// peekDecoration is the peek as a block under the line it's on.
func (x *lspExt) peekDecoration(ctx registry.DecorationCtx) []registry.Decoration {
	pk := x.peek
	if pk == nil || pk.path != ctx.Path || pk.line < ctx.Top || pk.line >= ctx.Bottom {
		return nil
	}
	return []registry.Decoration{{Kind: registry.Block, Line: pk.line, Node: &ui.Func{Key: "peek", Fn: func(c *ui.Ctx) { x.peekView(c, pk) }}, Priority: 20}}
}

// peekView is the highlighted reference's code beside every reference, by file (09-lsp).
func (x *lspExt) peekView(c *ui.Ctx, pk *refPeek) {
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
			src := x.fileText(sel.Path)
			f := highlightAround(x.h, sel.Path, src, sel.Range.Start.Line+1, 2, 6)
			f.mark, f.title, f.size = true, rel(sel.Path), 12
			f.build(c, nil)
		})
		c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
		c.Column("list", ui.Style{W: ui.Px(300), H: ui.Flex(1), Pad: ui.PadXY(0, 8), ScrollY: true}, func(c *ui.Ctx) {
			c.Row("count", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 2)}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("%d references to %s", len(pk.refs), pk.name), monoTok(c, 10, theme.FgFaint))
			})
			file := ""
			for i, l := range pk.refs {
				if l.Path != file {
					file = l.Path
					n := 0
					for _, o := range pk.refs {
						if o.Path == file {
							n++
						}
					}
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
					Fill: fill, Clip: true, Label: lineOf(x, l), Help: "editor/pane#references"}, func(c *ui.Ctx) {
					c.Text("l", fmt.Sprint(l.Range.Start.Line+1), monoTok(c, 10.5, theme.FgGhost))
					c.Text("t", lineOf(x, l), ui.TextStyle{Font: codeMono, Color: th.C(theme.FgMuted)})
				})
				if in.Clicked {
					pk.sel = i
				}
			}
		})
	})
}

func (x *lspExt) startRename() error {
	p, path, d := x.current()
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
	async(x, func(ctx context.Context) (*lsp.PrepareRename, error) { return c.PrepareRename(ctx, at) }, func(pr *lsp.PrepareRename, err error) {
		old := word
		if err == nil && pr != nil && pr.Placeholder != "" {
			old = pr.Placeholder
		}
		x.rename = &renamePrompt{path: path, at: at.Pos, old: old, value: old, c: c}
	})
	return nil
}

// renameInput is the prompt at the cursor; Enter asks the server, then shows the rename as a diff.
func (x *lspExt) renameInput(c *ui.Ctx, at render.Point) {
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

func (x *lspExt) doRename(rp *renamePrompt) {
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
func (x *lspExt) previewEdit(title, detail string, e lsp.WorkspaceEdit) error {
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
func (x *lspExt) symbolSources(h ext.Host) {
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
		mine := file
		mine.Items = func(ctx registry.Ctx, q string) []registry.PickerItem {
			_, path, d := x.current()
			if d == nil || d.client == nil {
				return file.Items(ctx, q)
			}
			c := d.client
			syms, err := c.DocumentSymbols(lspCtx(ctx), path)
			if err != nil || len(syms) == 0 {
				return file.Items(ctx, q)
			}
			return x.symbolItems(syms)
		}
		mine.Source = "builtin:lsp"
		x.add(h.Picker(mine))
	}
	if all.ID != "" {
		type result struct {
			q     string
			items []registry.PickerItem
		}
		var last result
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
			var syms []lsp.Symbol
			for _, c := range clients {
				if s, err := c.WorkspaceSymbols(lspCtx(ctx), q); err == nil {
					syms = append(syms, s...)
				}
			}
			last = result{q, x.symbolItems(syms)}
			return last.items
		}
		mine.Source = "builtin:lsp"
		x.add(h.Picker(mine))
	}
}

// lspCtx is a picker's context, or a fresh one in tests that call a source without one.
func lspCtx(ctx registry.Ctx) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

func (x *lspExt) symbolItems(syms []lsp.Symbol) []registry.PickerItem {
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
func (x *lspExt) calls(ctx registry.Ctx, incoming bool) error {
	p, path, d := x.current()
	if d == nil || d.client == nil {
		return fmt.Errorf("no language server for this file")
	}
	at, c := lsp.Loc{Path: path, Pos: text.Pos(p.Cursor())}, d.client
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
func (x *lspExt) callsSource() registry.PickerSource {
	return registry.PickerSource{ID: "lsp.calls", Title: "Calls", Order: 45, PickerOnly: true, Unlisted: true,
		Items: func(registry.Ctx, string) []registry.PickerItem { return x.callItems },
		Preview: func(it registry.PickerItem) ui.Node {
			return x.sh.editor.filePreview(it.Path, it.Line-1)
		},
	}
}
