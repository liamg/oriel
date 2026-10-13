package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/vim"
	"github.com/liamg/oriel/vim/vimreg"
)

// EditorView is the editor pane's view id; each window is an instance of it, named by the window's id.
const EditorView = "editor"

// editorExt is the editor: one vim engine whose windows are views in the layout, whose files are on disk,
// whose notices are the IDE's and whose "+ and "* are the system clipboard (editor.md, "Part of the IDE").
type editorExt struct {
	disposer
	sh     *shell
	h      ext.Host
	ed     *edit.Editor
	e      *vim.Engine
	dir    string
	docs   map[*text.Buffer]*highlighter
	rows   map[vim.WindowID]int
	pane   paneMetrics
	disk   diskWatch
	maps   map[string]registry.Disposable // :map's keymaps, by mode and keys
	inputs map[vim.WindowID]paneInput

	drawing      input.State // the engine of the pane being drawn
	pendingSince time.Time   // when the current window's engine began waiting for more keys
}

// diskWatch is how the editor notices files changing under it.
type diskWatch struct {
	next  time.Time
	asked map[int]time.Time // buffers whose change on disk was asked about, with the change's time
}

// diskInterval is how often the files of open buffers are looked at.
const diskInterval = time.Second

func (x *editorExt) Manifest() ext.Manifest { return ext.Manifest{ID: "editor"} }

func (x *editorExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.dir = x.sh.cfg.WorkDir
	if x.dir == "" {
		x.dir, _ = os.Getwd()
	}
	x.docs, x.rows = map[*text.Buffer]*highlighter{}, map[vim.WindowID]int{}
	x.disk.asked = map[int]time.Time{}
	x.maps = map[string]registry.Disposable{}
	x.inputs = map[vim.WindowID]paneInput{}
	x.builtinInputs()
	x.sh.enginePending = x.pending
	x.ed = edit.New(text.New(nil, text.Author{}), text.Author{Kind: text.User})
	x.ed.SetClipboard(desktopClipboard{x.sh})
	x.ed.OnNotice(func(n edit.Notice) { h.Notify(FromEdit(n, "vim")) })
	x.e = vim.New(x.ed)
	x.e.SetHost(&editorHost{x: x, disk: vim.Disk{Dir: x.dir}})
	x.e.SetLayout(&editorLayout{x: x})
	for _, d := range vimreg.Register(h) {
		x.add(d)
	}
	x.sh.editorAt = func(at registry.At) registry.At {
		at.Buffer, at.Window = x.e.Name(), strconv.Itoa(int(x.e.WindowID()))
		return at
	}
	x.e.SetSettings(vimreg.Settings{Options: h.Options(), At: func(buf, win string) registry.At {
		at := h.Here()
		at.Buffer, at.Window = buf, win
		return at
	}})
	x.add(disposeFn(h.Layout().OnClose(func(v layout.ViewRef) {
		if id, ok := windowOf(v); ok {
			x.e.WindowClosed(id)
		}
	})))

	str := func(a registry.Args, k string) string { s, _ := a[k].(string); return s }
	for _, c := range []registry.Command{
		{ID: "editor.open", Title: "Open a file in the editor, at {line} if given, in a {place}: tab, split or vsplit",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				if err := x.open(ctx, str(a, "path"), str(a, "place")); err != nil {
					return err
				}
				if n, ok := a["line"].(int); ok && n > 0 {
					x.e.Run(fmt.Sprint(n))
				}
				return nil
			}},
		{ID: "editor.keys", Title: "Type keys into the editor, as a mapping's right-hand side", Run: func(_ registry.Ctx, a registry.Args) error {
			noremap, _ := a["noremap"].(bool)
			x.e.TypeMapped(keymap.Parse(str(a, "keys")), noremap)
			return nil
		}},
		{ID: "editor.reload", Title: "Reload a buffer from its file", Run: func(_ registry.Ctx, a registry.Args) error {
			n, _ := a["buffer"].(int)
			if f, ok := a["buffer"].(float64); ok {
				n = int(f)
			}
			delete(x.disk.asked, n)
			return x.e.ReloadBuffer(n)
		}},
		{ID: "editor.keep", Title: "Keep a buffer's changes over its file's", Run: func(registry.Ctx, registry.Args) error { return nil }},
		{ID: "editor.input", Title: "Change the input engine of the current buffer", Run: func(_ registry.Ctx, a registry.Args) error {
			id := str(a, "engine")
			if id == "" {
				id = str(a, "args")
			}
			if _, ok := h.InputByID(id); !ok {
				return fmt.Errorf("no input engine %q: %s", id, strings.Join(h.Inputs(), ", "))
			}
			return h.Options().Set(InputOption, registry.LayerBuffer, registry.At{Buffer: x.e.Name()}, id)
		}},
		{ID: "editor.save", Title: "Write the current buffer", Run: func(registry.Ctx, registry.Args) error {
			x.e.Run("w")
			return nil
		}},
		{ID: "editor.complete", Title: "Complete the command line", Run: func(registry.Ctx, registry.Args) error {
			x.complete()
			return nil
		}},
		{ID: "buffers.switch", Title: "Switch to an open buffer", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": "buffers"})
		}},
		{ID: "quickfix.open", Title: "Show the quickfix list", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": "quickfix"})
		}},
		{ID: "quickfix.set", Title: "Make a new quickfix list: {title}, {items} of {path, line, col, text}", Run: func(_ registry.Ctx, a registry.Args) error {
			var items []vim.QfItem
			raw, _ := a["items"].([]map[string]any)
			for _, it := range raw {
				name, _ := it["path"].(string)
				text, _ := it["text"].(string)
				line, _ := it["line"].(int)
				col, _ := it["col"].(int)
				items = append(items, vim.QfItem{Name: name, Line: line, Col: col, Text: text})
			}
			x.e.SetQuickfix(str(a, "title"), items)
			return nil
		}},
		{ID: "editor.grep-word", Title: "Grep the word under the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": "grep", "query": `\b` + regexp.QuoteMeta(x.e.WordUnderCursor()) + `\b`})
		}},
	} {
		x.add(h.Command(c))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "c", Keys: "<Tab>", Command: "editor.complete", Desc: "Complete the command line"}))
	x.add(h.View(EditorView, editorView{x}))
	x.add(h.Picker(x.filesSource()))
	syms := x.symbolsSource()
	x.add(h.Picker(syms))
	x.add(h.Picker(x.fileSymbolsSource(syms)))
	x.add(h.Picker(x.buffersSource()))
	x.add(h.Picker(x.quickfixSource()))
	x.add(h.Picker(x.grepSource()))
	for _, k := range pickerKeys {
		x.add(h.Keymap(registry.Keymap{Mode: "n", Keys: k.keys, Command: "picker.open", Args: registry.Args{"source": k.source}, Desc: k.desc}))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>*", Command: "editor.grep-word", Desc: "Grep the word under the cursor"}))
	x.add(h.Keymap(registry.Keymap{Mode: "n", Keys: "<Leader>'", Command: "picker.resume", Desc: "Resume the last picker"}))
	x.add(h.View("statusline.left.00-mode", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "mode", Fn: x.modeSegment} })))
	x.add(h.View("statusline.right.80-file", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "file", Fn: x.fileSegment} })))
	x.add(h.View("statusline.right.90-position", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "position", Fn: x.positionSegment}
	})))
	for _, f := range x.sh.cfg.Files {
		if err := x.open(context.Background(), f, ""); err != nil {
			return err
		}
	}
	return nil
}

func (x *editorExt) Deactivate(ctx context.Context) error {
	for _, d := range x.docs {
		d.close()
	}
	return x.disposer.Deactivate(ctx)
}

type disposeFn func()

func (f disposeFn) Dispose() { f() }

func windowRef(id vim.WindowID) layout.ViewRef {
	return layout.ViewRef{ID: EditorView, Instance: strconv.Itoa(int(id))}
}

func windowOf(v layout.ViewRef) (vim.WindowID, bool) {
	if v.ID != EditorView {
		return 0, false
	}
	n, err := strconv.Atoi(v.Instance)
	return vim.WindowID(n), err == nil
}

// open opens path in a new editor tab beside the focused editor, or shows the tab that has it. place "split" or
// "vsplit" opens a new window on it beside the focused editor instead.
func (x *editorExt) open(ctx registry.Ctx, path, place string) error {
	name := path
	if rel, err := filepath.Rel(x.dir, path); err == nil && filepath.IsAbs(path) && !strings.HasPrefix(rel, "..") {
		name = rel
	}
	root := x.h.Layout()
	for _, id := range x.e.WindowIDs() {
		if p, ok := x.e.Pane(id); ok && p.Name() == name && root.Find(windowRef(id)) != nil && place != "split" && place != "vsplit" {
			root.Show(windowRef(id))
			x.e.EnterWindow(id)
			return nil
		}
	}
	var id vim.WindowID
	if ids := x.e.WindowIDs(); len(ids) == 1 && root.Find(windowRef(ids[0])) == nil {
		x.e.Edit(name) // the engine's first window, not yet on screen
		id = ids[0]
	} else {
		var err error
		if id, err = x.e.NewWindow(name); err != nil {
			return err
		}
	}
	args := registry.Args{"id": EditorView, "instance": strconv.Itoa(int(id)), "place": "tab"}
	switch place {
	case "split":
		args["place"], args["side"] = "split", "down"
	case "vsplit":
		args["place"], args["side"] = "split", "right"
	}
	if f := root.Focus; f != nil && f.ID == EditorView {
		args["beside"] = f.ID + "#" + f.Instance
	}
	return x.h.Run(ctx, "view.open", args)
}

// desktopClipboard is the system clipboard as the editor's registers see it. Platforms without a primary
// selection use the clipboard for "* too.
type desktopClipboard struct{ sh *shell }

func (d desktopClipboard) Read(bool) (string, bool) {
	if d.sh.desktop == nil {
		return "", false
	}
	return d.sh.desktop.ReadClipboard()
}

func (d desktopClipboard) Write(_ bool, s string) {
	if d.sh.desktop != nil {
		d.sh.desktop.SetClipboard(s)
	}
}

// editorHost is the IDE as the engine sees it: files from disk, and the IDE's own views for :ls, :copen and
// :messages. An ex command the engine doesn't know runs the registry command of that name.
type editorHost struct {
	x    *editorExt
	disk vim.Disk
}

func (h *editorHost) ReadFile(name string) ([]byte, *edit.File, error) { return h.disk.ReadFile(name) }
func (h *editorHost) WriteFile(f *edit.File, t []byte, noLines bool) error {
	return h.disk.WriteFile(f, t, noLines)
}
func (h *editorHost) Exists(name string) bool { return h.disk.Exists(name) }

// mapModes are the keymap modes the editor's keys go through for each of vim's mapping modes.
var mapModes = map[byte]string{'n': "n", 'v': "x", 'o': "o", 'i': "i", 'c': "c"}

// Map puts a :map in the registry keymap: its keys run editor.keys, which types the right-hand side.
func (h *editorHost) Map(mode byte, lhs, rhs []string, noremap bool) {
	h.Unmap(mode, lhs)
	k := registry.Keymap{Mode: mapModes[mode], Keys: keymap.Format(lhs), Command: "editor.keys", Source: ":map",
		Args: registry.Args{"keys": keymap.Format(rhs), "noremap": noremap}, Desc: keymap.Format(rhs)}
	h.x.maps[mapID(mode, lhs)] = h.x.h.Keymap(k)
}

func (h *editorHost) Unmap(mode byte, lhs []string) bool {
	d, ok := h.x.maps[mapID(mode, lhs)]
	if ok {
		d.Dispose()
		delete(h.x.maps, mapID(mode, lhs))
	}
	return ok
}

func (h *editorHost) MapClear(mode byte) {
	for id, d := range h.x.maps {
		if id[0] == mode {
			d.Dispose()
			delete(h.x.maps, id)
		}
	}
}

func mapID(mode byte, lhs []string) string { return string(mode) + keymap.Format(lhs) }

// Quit closes the editor's windows: ending the editing closes its panes, not Oriel.
func (h *editorHost) Quit() {
	root := h.x.h.Layout()
	for _, id := range h.x.e.WindowIDs() {
		root.Close(windowRef(id))
	}
}

func (h *editorHost) Command(name, args string, bang bool) bool {
	ctx := context.Background()
	run := func(id string, a registry.Args) bool {
		if err := h.x.h.Run(ctx, id, a); err != nil {
			h.x.h.Notify(core.Notice{ID: "editor.command", Severity: core.SevError, Source: "editor", Key: true, Text: err.Error()})
		}
		return true
	}
	switch name {
	case "ls", "buffers", "files":
		return run("buffers.switch", nil)
	case "copen", "cwindow", "lopen", "lwindow":
		return run("quickfix.open", nil)
	case "messages":
		return run("messages", nil)
	}
	for _, c := range h.x.h.Commands() {
		if c.ID == name {
			return run(name, registry.Args{"args": args, "bang": bang})
		}
	}
	return false
}

// editorLayout is the engine's windows as views in the IDE's layout: :split splits the pane, CTRL-W moves
// between panes, and a pane dragged elsewhere is where the engine finds it.
type editorLayout struct{ x *editorExt }

func (l *editorLayout) root() *layout.Root { return l.x.h.Layout() }

func (l *editorLayout) Split(at, w vim.WindowID, vertical bool) {
	side := layout.SideUp
	if vertical {
		side = layout.SideLeft
	}
	at0 := windowRef(at)
	if l.root().Find(at0) == nil {
		l.root().Open(at0, layout.Tab, layout.Opts{})
	}
	l.root().Open(windowRef(w), layout.Split, layout.Opts{Beside: &at0, Side: side})
}

func (l *editorLayout) Bottom(w vim.WindowID) {
	l.root().Open(windowRef(w), layout.Docked, layout.Opts{Edge: layout.Bottom})
}

func (l *editorLayout) Close(w vim.WindowID) vim.WindowID {
	l.root().Close(windowRef(w))
	if f := l.root().Focus; f != nil {
		if id, ok := windowOf(*f); ok {
			return id
		}
	}
	if o := l.Order(); len(o) > 0 {
		return o[0]
	}
	return w
}

func (l *editorLayout) Only(w vim.WindowID) {
	for _, v := range l.root().Shown() {
		if id, ok := windowOf(v); ok && id != w {
			l.root().Close(v)
		}
	}
}

func (l *editorLayout) Order() []vim.WindowID {
	var out []vim.WindowID
	for _, v := range l.root().Shown() {
		if id, ok := windowOf(v); ok {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		out = []vim.WindowID{l.x.e.WindowID()}
	}
	return out
}

func (l *editorLayout) Rows(w vim.WindowID) int {
	if n := l.x.rows[w]; n > 0 {
		return n
	}
	return 40
}

func (l *editorLayout) Neighbour(w vim.WindowID, dir byte, row, col int) (vim.WindowID, bool) {
	from, ok := l.x.h.PaneRect(windowRef(w))
	if !ok {
		return 0, false
	}
	m := l.x.pane
	px := from.Min.X + m.textX + float32(col)*m.charW
	py := from.Min.Y + m.codeTop + float32(row)*m.lineH
	best, found, bestD := vim.WindowID(0), false, float32(0)
	for _, id := range l.Order() {
		if id == w {
			continue
		}
		r, ok := l.x.h.PaneRect(windowRef(id))
		if !ok {
			continue
		}
		var d float32
		var hit bool
		switch dir {
		case 'h':
			hit, d = r.Max.X <= from.Min.X+1 && py >= r.Min.Y && py < r.Max.Y, from.Min.X-r.Max.X
		case 'l':
			hit, d = r.Min.X >= from.Max.X-1 && py >= r.Min.Y && py < r.Max.Y, r.Min.X-from.Max.X
		case 'k':
			hit, d = r.Max.Y <= from.Min.Y+1 && px >= r.Min.X && px < r.Max.X, from.Min.Y-r.Max.Y
		case 'j':
			hit, d = r.Min.Y >= from.Max.Y-1 && px >= r.Min.X && px < r.Max.X, r.Min.Y-from.Max.Y
		}
		if hit && (!found || d < bestD) {
			best, found, bestD = id, true, d
		}
	}
	return best, found
}

func (l *editorLayout) Exchange(w vim.WindowID, count int) (vim.WindowID, bool) {
	row, i, ok := l.root().Row(windowRef(w))
	if !ok {
		return 0, false
	}
	j := i + 1
	if count > 0 {
		j = count - 1
	} else if j >= len(row) {
		j = i - 1
	}
	if j < 0 || j >= len(row) || row[j] == (layout.ViewRef{}) {
		return 0, false
	}
	other, ok := windowOf(row[j])
	if !ok {
		return 0, false
	}
	l.root().Swap(windowRef(w), row[j])
	return other, true
}

func (l *editorLayout) Rotate(w vim.WindowID, back bool, count int) bool {
	row, _, ok := l.root().Row(windowRef(w))
	if !ok {
		return true
	}
	if slices.Contains(row, layout.ViewRef{}) {
		return false
	}
	for range count {
		n := len(row)
		if back {
			for i := 0; i+1 < n; i++ {
				l.root().Swap(row[i], row[i+1])
				row[i], row[i+1] = row[i+1], row[i]
			}
		} else {
			for i := n - 1; i > 0; i-- {
				l.root().Swap(row[i], row[i-1])
				row[i], row[i-1] = row[i-1], row[i]
			}
		}
	}
	return true
}

func (l *editorLayout) Focus(w vim.WindowID) {
	if l.root().Find(windowRef(w)) != nil {
		l.root().Show(windowRef(w))
	}
}

// editorView is the pane view: one instance per window.
type editorView struct{ x *editorExt }

func (v editorView) EdgeToEdge() {}

func (v editorView) Build(vc registry.ViewCtx) ui.Node {
	n, err := strconv.Atoi(vc.Instance)
	return &ui.Func{Key: "editor", Fn: func(c *ui.Ctx) {
		if err != nil {
			c.Text("bad", "no editor window "+vc.Instance, ui.TextStyle{})
			return
		}
		v.x.paneView(c, vim.WindowID(n), vc.Focused)
	}}
}

func (v editorView) Title(instance string) string {
	n, err := strconv.Atoi(instance)
	if err != nil {
		return ""
	}
	p, ok := v.x.e.Pane(vim.WindowID(n))
	if !ok {
		return ""
	}
	name := filepath.Base(p.Name())
	if p.Name() == "" {
		name = "[No Name]"
	}
	if p.Modified() {
		name += " ●"
	}
	return name
}

// highlighter keeps a buffer's syntax highlights up to date, for the panes showing it.
type highlighter struct {
	doc    *syntax.Document
	lang   string
	cancel func()
}

func (h *highlighter) close() {
	if h.cancel != nil {
		h.cancel()
	}
	if h.doc != nil {
		h.doc.Close()
	}
}

// highlights is the buffer's highlighter, started the first time a pane draws it.
func (x *editorExt) highlights(p vim.Pane) *highlighter {
	b := p.Buffer()
	if h, ok := x.docs[b]; ok {
		return h
	}
	h := &highlighter{}
	x.docs[b] = h
	lang, ok := syntax.ForPath(p.Name())
	if !ok {
		return h
	}
	doc, err := syntax.NewDocument(lang, b.Snapshot())
	if err != nil {
		x.h.Log().Warn("syntax", "file", p.Name(), "err", err)
		return h
	}
	doc.OnUpdate = x.h.Redraw
	h.doc, h.lang = doc, lang.Name
	h.cancel = b.Subscribe(doc.Change)
	return h
}

// pending is the focused editor's engine waiting for more keys: vim's g, z or a count, emacs's C-x.
func (x *editorExt) pending() ext.Pending {
	f := x.h.Layout().Focus
	if f == nil || f.ID != EditorView || x.pendingSince.IsZero() {
		return ext.Pending{}
	}
	st := x.inputFor(x.current())
	p := ext.Pending{Modes: []string{st.Mode().KeyMode, "global"}, Since: x.pendingSince}
	if isVim(st) {
		p.Keys = x.e.Pending()
		for _, h := range x.e.NextKeys() {
			p.Hints = append(p.Hints, ext.Hint{Keys: h.Keys, Desc: h.Desc})
		}
	} else if s := st.Pending(); s != "" {
		p.Keys = []string{s}
	}
	if len(p.Keys) == 0 {
		return ext.Pending{}
	}
	return p
}

// checkDisk reloads a buffer whose file changed on disk, or asks when the buffer has changes of its own.
func (x *editorExt) checkDisk(c *ui.Ctx) {
	now := c.Now()
	if now.Before(x.disk.next) {
		c.WakeAt(x.disk.next)
		return
	}
	x.disk.next = now.Add(diskInterval)
	c.WakeAt(x.disk.next)
	for _, f := range x.e.Files() {
		changed, err := f.File.ChangedOnDisk()
		if err != nil || !changed {
			continue
		}
		st, _ := os.Stat(f.File.Path)
		var mod time.Time
		if st != nil {
			mod = st.ModTime()
		}
		if !f.Modified {
			if err := x.e.ReloadBuffer(f.Num); err != nil {
				x.h.Notify(core.Notice{ID: "editor.reload", Severity: core.SevError, Source: "editor", Text: err.Error()})
			}
			continue
		}
		if t, ok := x.disk.asked[f.Num]; ok && t.Equal(mod) {
			continue
		}
		x.disk.asked[f.Num] = mod
		x.h.Notify(core.Notice{ID: "editor.changed-on-disk." + f.Name, Severity: core.SevQuestion, Source: "editor", Title: "Changed on disk",
			Text: f.Name + " changed on disk, and has unsaved changes here.",
			Actions: []core.Action{
				{Title: "Reload", Command: "editor.reload", Args: map[string]any{"buffer": f.Num}},
				{Title: "Keep mine", Command: "editor.keep", Args: map[string]any{"buffer": f.Num}},
			}})
	}
}

// complete is <Tab> on the command line: the longest common start of the ex commands, buffers or files that
// match what's typed, then each in turn.
func (x *editorExt) complete() {
	line, ok := x.e.Cmdline()
	if !ok || !strings.HasPrefix(line, ":") {
		return
	}
	text := line[1:]
	cmd, arg, hasArg := strings.Cut(text, " ")
	var cands []string
	prefix := ""
	if !hasArg {
		cands = append(cands, vim.ExNames()...)
		for _, c := range x.h.Commands() {
			cands = append(cands, c.ID)
		}
		arg = cmd
	} else {
		prefix = cmd + " "
		switch cmd {
		case "b", "buffer", "sb", "sbuffer", "bd", "bdelete":
			for _, b := range x.e.BufferList() {
				cands = append(cands, b.Name)
			}
		default:
			cands = x.files(arg)
		}
	}
	var matches []string
	for _, c := range cands {
		if strings.HasPrefix(c, arg) && c != arg {
			matches = append(matches, c)
		}
	}
	slices.Sort(matches)
	matches = slices.Compact(matches)
	if len(matches) == 0 {
		return
	}
	common := matches[0]
	for _, m := range matches[1:] {
		for !strings.HasPrefix(m, common) {
			common = common[:len(common)-1]
		}
	}
	if common == arg {
		common = matches[0]
	}
	x.e.SetCmdline(prefix + common)
}

// files are the paths under the editor's directory that complete arg.
func (x *editorExt) files(arg string) []string {
	dir, base := filepath.Split(arg)
	ents, err := os.ReadDir(filepath.Join(x.dir, dir))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if !strings.HasPrefix(e.Name(), base) {
			continue
		}
		name := dir + e.Name()
		if e.IsDir() {
			name += "/"
		}
		out = append(out, name)
	}
	return out
}

// modeName is the engine's mode as the status line chip says it.
func modeName(m string) string {
	switch m {
	case "insert":
		return "INSERT"
	case "replace":
		return "REPLACE"
	case "visual":
		return "VISUAL"
	case "visual-line":
		return "V-LINE"
	case "visual-block":
		return "V-BLOCK"
	case "cmdline":
		return "COMMAND"
	case "op-pending":
		return "O-PENDING"
	}
	return "NORMAL"
}

// keyMode is the keymap mode the editor takes keys in: vim's mode letters, so :nmap is mode "n".
func keyMode(m string) string {
	switch m {
	case "insert", "replace":
		return "i"
	case "visual", "visual-line", "visual-block":
		return "x"
	case "cmdline":
		return "c"
	case "op-pending":
		return "o"
	}
	return "n"
}
