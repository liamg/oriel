// Package editor is the text editor: buffers in panes, its vim or plain input, git signs, markdown and its preview,
// and the pickers and search it brings.
package editor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/kit/vimreg"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/edit/input"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/search"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/vim"
)

// Editor is the editor: one vim engine whose windows are views in the layout, whose files are on disk,
// whose notices are the IDE's and whose "+ and "* are the system clipboard.
type Editor struct {
	kit.Disposer
	Files     []string       // opened at start
	Clipboard edit.Clipboard // the system clipboard, for "+ and "*
	Tools     *search.Tools  // the machine's search tools, for grep

	h      plugin.Host
	ed     *edit.Editor
	e      *vim.Engine
	conv   *conventions
	dir    string    // relative names are under it: the window's directory, or the process's on the start view
	listed *fileList // the files picker's

	unsearched struct { // what search leaves out of the work dir (unsearchable)
		mu      sync.Mutex
		dir     string
		files   []string
		at      time.Time
		ok      bool
		walking bool
		walks   int // for the tests
	}
	docs    map[*text.Buffer]*highlighter
	busy    work.Busy // parsers being made and files read, off the loop
	madeMu  sync.Mutex
	made    map[*syntax.Document]bool // made, not yet handed to the loop
	stopped bool                      // deactivated: a document made since is closed where it's made
	rows    map[vim.WindowID]int
	// blockPx is how tall the blocks between a window's lines were last frame: rows it doesn't have for text.
	blockPx map[vim.WindowID]float32
	pane    kit.PaneMetrics
	disk    diskWatch
	maps    map[string]registry.Disposable // :map's keymaps, by mode and keys
	inputs  map[vim.WindowID]paneInput
	drags   map[vim.WindowID]*paneDrag      // a press in each window
	present map[vim.WindowID]*windowPresent // the windows a presenter draws

	fileHost  *editorHost
	lineTops  map[vim.WindowID][]lineTop // where a window's lines were drawn last frame, below the blocks between them
	previewed kit.FilePreview            // the file a picker last previewed

	now          time.Time   // the frame's, for decorations
	drawing      input.State // the engine of the pane being drawn
	caret        ui.ID       // the current window's cursor, last frame: where popups at the cursor go
	pendingSince time.Time   // when the current window's engine began waiting for more keys
}

func (x *Editor) Manifest() plugin.Manifest { return plugin.Manifest{ID: "editor"} }

func (x *Editor) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "editor", Busy: func() bool { return x.busy.Busy() || x.listed != nil && x.listed.busy.Busy() }, Source: "builtin:editor"}))
	x.h = h
	x.dir = editorDir(h)
	x.docs, x.rows, x.blockPx = map[*text.Buffer]*highlighter{}, map[vim.WindowID]int{}, map[vim.WindowID]float32{}
	x.lineTops = map[vim.WindowID][]lineTop{}
	x.disk.asked = map[int]time.Time{}
	x.maps = map[string]registry.Disposable{}
	x.inputs = map[vim.WindowID]paneInput{}
	x.present = map[vim.WindowID]*windowPresent{}
	x.builtinInputs()
	x.ed = edit.New(text.New(nil, text.Author{}), text.Author{Kind: text.User})
	if x.Clipboard != nil {
		x.ed.SetClipboard(x.Clipboard)
	}
	x.ed.OnNotice(func(n edit.Notice) { h.Notify(kit.FromEdit(n, "vim")) })
	x.e = vim.New(x.ed)
	x.fileHost = &editorHost{x: x, disk: vim.Disk{Dir: x.dir}}
	x.e.SetHost(x.fileHost)
	x.e.SetLayout(&editorLayout{x: x})
	for _, d := range vimreg.Register(h) {
		x.Add(d)
	}
	for _, s := range conventionOptions() {
		x.Add(h.Option(s))
	}
	x.Add(h.Option(registry.OptionSpec{Name: "editor.font_size", Type: registry.Float, Default: float64(kit.CodeSize), Min: 6, Max: 48, Step: 0.5,
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The code's text size, inside the UI's zoom (ui.font_size)."}))
	x.Add(h.Option(registry.OptionSpec{Name: "editor.cursor_glide", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
		Help: "Glide the cursor between places. Typing, single steps and held keys still move it at once."}))
	x.Add(h.Option(registry.OptionSpec{Name: "editor.line_height", Type: registry.Float, Default: 1.55, Min: 1, Max: 3, Step: 0.05,
		Scopes: registry.ScopeUser | registry.ScopeProject, Help: "The space between lines of code, as a multiple of editor.font_size."}))
	for _, s := range x.inspectSections() {
		x.Add(h.Contribute(s))
	}
	for _, e := range []registry.EventInfo{
		{Name: "BufRead", Doc: "A file read into a buffer; the subject is its name. A rewrite setting read_only opens it read-only.", Payload: kit.BufRead{}, Fired: true},
		{Name: "BufWritePre", Doc: "A buffer about to be written; a veto stops the write.", Payload: kit.BufWritePre{}, Fired: true},
		{Name: "BufWritePost", Doc: "A buffer just written.", Payload: kit.BufWritePost{}, Fired: true},
	} {
		x.Add(h.DeclareEvent(e))
	}
	x.conv = newConventions(h, x.dir, func() []string {
		var out []string
		for _, b := range x.e.BufferList() {
			if b.File {
				out = append(out, b.Name)
			}
		}
		return out
	})
	x.Add(h.Contribute(registry.Service{Name: services.EditorsService, Impl: editors{x}, Source: "builtin:editor"}))
	x.e.SetSettings(vimreg.Settings{Options: h.Options(), At: func(buf, win string) registry.At {
		at := h.Here()
		at.Buffer, at.Window = buf, win
		return at
	}})
	x.Add(registry.DisposeFunc(h.Layout().OnClose(func(v layout.ViewRef) {
		if id, ok := windowOf(v); ok {
			x.e.WindowClosed(id)
			delete(x.present, id)
		}
	})))

	for _, c := range x.commands(h) {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	for id, v := range x.views() {
		x.Add(h.View(id, v))
	}
	syms := x.symbolsSource()
	for _, p := range []uireg.PickerSource{x.filesSource(), syms, x.fileSymbolsSource(syms), x.buffersSource(), x.quickfixSource(), x.grepSource()} {
		x.Add(h.Picker(p))
	}
	x.Add(kit.OnDirChanged(h, "the editor's directory", func(kit.DirChanged) { x.follow(editorDir(h)) }))
	for _, f := range x.Files {
		if _, err := x.open(context.Background(), f, ""); err != nil {
			return err
		}
	}
	return nil
}

// editorDir is the directory the editor's relative names are under: the window's, or the process's on the start view.
func editorDir(h plugin.Host) string {
	if d := kit.WindowDir(h); d != "" {
		return d
	}
	d, _ := os.Getwd()
	return d
}

// follow puts the editor's relative names under dir, as the window moves onto it. Buffers stay open; the windows
// on them are the layout's, which the directory's own layout replaces.
func (x *Editor) follow(dir string) {
	x.dir = dir
	x.fileHost.disk.Dir = dir
	x.conv.setRoot(dir)
	if x.listed != nil {
		x.listed.setDir(dir)
	}
}

// commands are the editor's commands.
func (x *Editor) commands(h plugin.Host) []registry.Command {
	return []registry.Command{
		{ID: "editor.open", Title: "Open a file in the editor, at {line} if given, in a {place}: tab, split or vsplit",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				viewed, err := x.open(ctx, a.String("path"), a.String("place"))
				if err != nil {
					return err
				}
				if n := a.Int("line"); n > 0 && !viewed {
					x.e.Run(fmt.Sprint(n))
				}
				return nil
			}},
		{ID: "editor.keys", Title: "Type keys into the editor, as a mapping's right-hand side", Run: func(_ registry.Ctx, a registry.Args) error {
			noremap, _ := a["noremap"].(bool)
			x.e.TypeMapped(keymap.Parse(a.String("keys")), noremap)
			return nil
		}},
		{ID: "editor.copy", Title: "Copy the selection to the system clipboard", Run: func(registry.Ctx, registry.Args) error { return x.clip("copy") }},
		{ID: "editor.cut", Title: "Cut the selection to the system clipboard", Run: func(registry.Ctx, registry.Args) error { return x.clip("cut") }},
		{ID: "editor.select-all", Title: "Select the whole buffer", Run: func(registry.Ctx, registry.Args) error {
			b := x.e.Buf()
			last := max(b.Lines()-1, 0)
			x.e.Select(vim.Pos{}, vim.Pos{Line: last, Col: max(len(b.Line(last))-1, 0)}, 'v')
			return nil
		}},
		{ID: "editor.paste", Title: "Paste the system clipboard: at the cursor, or over the selection", Run: func(registry.Ctx, registry.Args) error {
			return x.clip("paste")
		}},
		{ID: "editor.back", Title: "Back in the jumplist (CTRL-O), or to the session or page before once it has nothing older",
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.jumpOr(ctx, -1) }},
		{ID: "editor.forward", Title: "Forward in the jumplist (CTRL-I), or to the session or page after once it has nothing newer",
			Run: func(ctx registry.Ctx, _ registry.Args) error { return x.jumpOr(ctx, 1) }},
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
			id := a.String("engine")
			if id == "" {
				id = a.String("args")
			}
			if _, ok := h.InputByID(id); !ok {
				return fmt.Errorf("no input engine %q: %s", id, strings.Join(h.Inputs(), ", "))
			}
			return h.Options().Set(InputOption, registry.LayerBuffer, registry.At{Buffer: x.e.Name()}, id)
		}},
		{ID: "editor.wrap.toggle", Title: "Wrap long lines, or stop wrapping them", Run: func(registry.Ctx, registry.Args) error {
			x.e.Run("set wrap!")
			return nil
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
			for _, it := range a.Maps("items") {
				items = append(items, vim.QfItem{Name: it.String("path"), Line: it.Int("line"), Col: it.Int("col"), Text: it.String("text")})
			}
			x.e.SetQuickfix(a.String("title"), items)
			return nil
		}},
		{ID: "editor.multi.next", Title: "Add a cursor at the next match of the word under the cursor", Run: func(registry.Ctx, registry.Args) error {
			x.e.MultiNext()
			return nil
		}},
		{ID: "editor.multi.down", Title: "Add a cursor on the line below", Run: func(registry.Ctx, registry.Args) error {
			x.e.MultiVertical(1)
			return nil
		}},
		{ID: "editor.multi.up", Title: "Add a cursor on the line above", Run: func(registry.Ctx, registry.Args) error {
			x.e.MultiVertical(-1)
			return nil
		}},
		{ID: "editor.present", Title: "Show a {window} (the focused one by default) with the presenter {name} (the first that takes its file by default), or as text again when it shows it already",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.presentIn(a.String("window"), a.String("name")) }},
		{ID: "editor.grep-word", Title: "Grep the word under the cursor", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "picker.open", registry.Args{"source": "grep", "query": `\b` + regexp.QuoteMeta(x.e.WordUnderCursor()) + `\b`})
		}},
	}
}

// keymaps are the editor's keys.
func (x *Editor) keymaps() []registry.Keymap {
	out := []registry.Keymap{{Mode: "c", Keys: "<Tab>", Command: "editor.complete", Desc: "Complete the command line"}}
	for _, k := range pickerKeys {
		out = append(out, registry.Keymap{Mode: "n", Keys: k.keys, Command: "picker.open", Args: registry.Args{"source": k.source}, Desc: k.desc})
	}
	out = append(out, registry.Keymap{Mode: "n", Keys: "<Leader>*", Command: "editor.grep-word", Desc: "Grep the word under the cursor"})
	for _, mode := range []string{"n", kit.PresentKeyMode} {
		out = append(out, registry.Keymap{Mode: mode, Keys: "<Leader>cp", Command: "editor.present", Desc: "Preview the file, or edit it again"})
	}
	out = append(out, []registry.Keymap{
		{Mode: "n", Keys: "<D-a>", Command: "editor.select-all", Desc: "Select all"},
		{Mode: "x", Keys: "<D-a>", Command: "editor.select-all", Desc: "Select all"},
		{Mode: "i", Keys: "<D-a>", Command: "editor.select-all", Desc: "Select all"},
		{Mode: "x", Keys: "<D-c>", Command: "editor.copy", Desc: "Copy"},
		{Mode: "x", Keys: "<C-S-c>", Command: "editor.copy", Desc: "Copy"},
		{Mode: "x", Keys: "<D-x>", Command: "editor.cut", Desc: "Cut"},
		{Mode: "x", Keys: "<C-S-x>", Command: "editor.cut", Desc: "Cut"},
		{Mode: "x", Keys: "<D-v>", Command: "editor.paste", Desc: "Paste over the selection"},
		{Mode: "x", Keys: "<C-S-v>", Command: "editor.paste", Desc: "Paste over the selection"},
		{Mode: "n", Keys: "<D-v>", Command: "editor.paste", Desc: "Paste"},
		{Mode: "n", Keys: "<C-S-v>", Command: "editor.paste", Desc: "Paste"},
		{Mode: "i", Keys: "<D-v>", Command: "editor.paste", Desc: "Paste"},
		{Mode: "i", Keys: "<C-S-v>", Command: "editor.paste", Desc: "Paste"},
		{Mode: "c", Keys: "<D-v>", Command: "editor.paste", Desc: "Paste"},
		{Mode: "n", Keys: "<X1Mouse>", Command: "editor.back", Desc: "Back"},
		{Mode: "n", Keys: "<X2Mouse>", Command: "editor.forward", Desc: "Forward"},
		{Mode: "n", Keys: "<C-n>", Command: "editor.multi.next", Desc: "Cursor at the next match"},
		{Mode: "n", Keys: "<C-Down>", Command: "editor.multi.down", Desc: "Cursor below"},
		{Mode: "n", Keys: "<C-Up>", Command: "editor.multi.up", Desc: "Cursor above"},
	}...)
	return append(out, registry.Keymap{Mode: "n", Keys: "<Leader>'", Command: "picker.resume", Desc: "Resume the last picker"})
}

// views are the editor's views: its windows, and its segments of the status line.
func (x *Editor) views() map[string]uireg.View {
	out := map[string]uireg.View{
		kit.EditorView:            editorView{x},
		"statusline.left.00-mode": uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "mode", Fn: x.modeSegment} }),
	}
	for name, fn := range map[string]func(*ui.Ctx){"80-file": x.fileSegment, "85-wrap": x.wrapSegment, "90-position": x.positionSegment} {
		out[kit.StatusRight+name] = uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
			if !x.shown() {
				return nil
			}
			return &ui.Func{Key: name, Fn: fn}
		})
	}
	return out
}

func (x *Editor) Deactivate(ctx context.Context) error {
	x.madeMu.Lock()
	x.stopped = true
	x.madeMu.Unlock()
	x.busy.Wait()
	for doc := range x.made {
		doc.Close()
	}
	x.made = nil
	for _, d := range x.docs {
		d.close()
	}
	return x.Disposer.Deactivate(ctx)
}

func windowRef(id vim.WindowID) layout.ViewRef {
	return layout.ViewRef{ID: kit.EditorView, Instance: strconv.Itoa(int(id))}
}

func windowOf(v layout.ViewRef) (vim.WindowID, bool) {
	if v.ID != kit.EditorView {
		return 0, false
	}
	n, err := strconv.Atoi(v.Instance)
	return vim.WindowID(n), err == nil
}

// open opens path in a new editor tab beside the focused editor, or shows the tab that has it. place "split" or
// "vsplit" opens a new window on it beside the focused editor instead. A file a viewer claims (registry.FileViewer)
// opens in its view instead, and viewed says so.
func (x *Editor) open(ctx registry.Ctx, path, place string) (viewed bool, err error) {
	name := x.relName(path)
	root := x.h.Layout()
	abs := path
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(x.dir, abs)
	}
	if v, ok := registry.ViewerFor(x.h, abs); ok {
		return true, x.h.Run(ctx, "view.open", x.placed(registry.Args{"id": v.View, "instance": abs}, place))
	}
	for _, id := range x.e.WindowIDs() {
		if p, ok := x.e.Pane(id); ok && p.Name() == name && root.Find(windowRef(id)) != nil && place != "split" && place != "vsplit" {
			root.Show(windowRef(id))
			x.e.EnterWindow(id)
			x.conv.refresh()
			return false, nil
		}
	}
	id, err := x.window(name)
	if err != nil {
		return false, err
	}
	x.conv.refresh()
	return false, x.h.Run(ctx, "view.open", x.placed(registry.Args{"id": kit.EditorView, "instance": strconv.Itoa(int(id))}, place))
}

// placed is view.open's args for a view opened in place (a tab, a split or a vsplit) beside the focused editor.
func (x *Editor) placed(args registry.Args, place string) registry.Args {
	args["place"] = "tab"
	switch place {
	case "split":
		args["place"], args["side"] = "split", "down"
	case "vsplit":
		args["place"], args["side"] = "split", "right"
	}
	if f := x.h.Layout().Focus; f != nil && f.ID == kit.EditorView {
		args["beside"] = f.ID + "#" + f.Instance
	}
	return args
}

// window is a new window on name: the engine's first window while it isn't on screen yet, or another.
func (x *Editor) window(name string) (vim.WindowID, error) {
	if ids := x.e.WindowIDs(); len(ids) == 1 && x.h.Layout().Find(windowRef(ids[0])) == nil {
		x.e.Edit(name)
		return ids[0], nil
	}
	return x.e.NewWindow(name)
}

// relName is how the editor names path: relative to its directory when inside it.
func (x *Editor) relName(path string) string {
	if rel, err := filepath.Rel(x.dir, path); err == nil && filepath.IsAbs(path) && !strings.HasPrefix(rel, "..") {
		return rel
	}
	return path
}

// pending is the focused editor's engine waiting for more keys: vim's g, z or a count, emacs's C-x.
func (x *Editor) pending() plugin.Pending {
	f := x.h.Layout().Focus
	if f == nil || f.ID != kit.EditorView || x.pendingSince.IsZero() {
		return plugin.Pending{}
	}
	st := x.inputFor(x.current())
	p := plugin.Pending{Modes: []string{st.Mode().KeyMode, "global"}, Since: x.pendingSince}
	if isVim(st) {
		p.Keys = x.e.Pending()
		for _, h := range x.e.NextKeys() {
			p.Hints = append(p.Hints, plugin.Hint{Keys: h.Keys, Desc: h.Desc})
		}
	} else if s := st.Pending(); s != "" {
		p.Keys = []string{s}
	}
	if len(p.Keys) == 0 {
		return plugin.Pending{}
	}
	return p
}

// complete is <Tab> on the command line: the longest common start of the ex commands, buffers or files that
// match what's typed, then each in turn.
func (x *Editor) complete() {
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
func (x *Editor) files(arg string) []string {
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

// unsearchable are the work dir's files .gitattributes marks generated or vendored, which search leaves out. They're
// worked out once for a directory, then again in the background once fileFresh old, not for every search typed.
func (x *Editor) unsearchable() []string {
	u := &x.unsearched
	dir := x.dir
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.dir != dir || !u.ok {
		u.dir, u.files, u.at, u.ok = dir, x.unsearchableIn(dir), time.Now(), true
		return u.files
	}
	if !u.walking && time.Since(u.at) >= fileFresh {
		u.walking = true
		x.busy.Go(func() {
			files := x.unsearchableIn(dir)
			u.mu.Lock()
			defer u.mu.Unlock()
			u.walking = false
			if u.dir == dir {
				u.files, u.at = files, time.Now()
			}
		})
	}
	return u.files
}

// unsearchableIn walks dir for the files search leaves out.
func (x *Editor) unsearchableIn(dir string) []string {
	x.unsearched.walks++ // u.mu is held, or its walking set
	all := walkFiles(dir, nil)
	keep := map[string]bool{}
	for _, f := range x.conv.searchable(all) {
		keep[f] = true
	}
	var out []string
	for _, f := range all {
		if !keep[f] {
			out = append(out, f)
		}
	}
	return out
}

// openBuffer is the listed buffer holding the file at path, if one does.
func (x *Editor) openBuffer(path string) (vim.FileState, bool) {
	for _, f := range x.e.Files() {
		if f.File != nil && filepath.Clean(f.File.Path) == filepath.Clean(path) {
			return f, true
		}
	}
	return vim.FileState{}, false
}

// openFileBytes is an open buffer's text as it would be written, by its path: absolute, or relative to the work dir.
func (x *Editor) openFileBytes(rel string) ([]byte, bool) {
	path := rel
	if !filepath.IsAbs(path) {
		path = filepath.Join(x.dir, rel)
	}
	f, ok := x.openBuffer(path)
	if !ok {
		return nil, false
	}
	return f.Bytes(), true
}

func layoutRef(id string) layout.ViewRef { return layout.ViewRef{ID: id} }

// clip copies, cuts or pastes with the system clipboard, the "+ register, as the platform's keys do in any mode.
func (x *Editor) clip(op string) error {
	_, selecting := x.e.Visual()
	var keys []string
	switch {
	case op == "copy" && selecting:
		keys = []string{"\"", "+", "y"}
	case op == "cut" && selecting:
		keys = []string{"\"", "+", "d"}
	case op == "paste" && selecting:
		keys = []string{"\"", "+", "p"}
	case op == "paste":
		switch x.e.Mode() {
		case "insert", "replace", "cmdline":
			keys = []string{"<C-r>", "+"}
		default:
			keys = []string{"\"", "+", "P"}
		}
	}
	if keys != nil {
		x.e.TypeMapped(keys, true)
	}
	return nil
}

// jumpOr goes dir along the jumplist, as CTRL-O and CTRL-I do, or along the window's history once the jumplist has
// nowhere to go.
func (x *Editor) jumpOr(ctx registry.Ctx, dir int) error {
	if isVim(x.inputFor(x.current())) && x.e.Mode() == "normal" && x.e.CanJump(dir) {
		keys := "<C-o>"
		if dir > 0 {
			keys = "<C-i>"
		}
		x.e.TypeMapped([]string{keys}, true)
		return nil
	}
	if dir < 0 {
		return x.h.Run(ctx, "nav.back", nil)
	}
	return x.h.Run(ctx, "nav.forward", nil)
}
