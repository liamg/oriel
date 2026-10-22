package editor

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui/layout"
	"github.com/liamg/oriel/vim"
)

// editorHost is the IDE as the engine sees it: files from disk, and the IDE's own views for :ls, :copen and
// :messages. An ex command the engine doesn't know runs the registry command of that name.
type editorHost struct {
	x    *Editor
	disk vim.Disk
}

func (h *editorHost) ReadFile(name string) ([]byte, *edit.File, error) {
	b, f, err := h.disk.ReadFile(name)
	return h.opened(name, b, f, err)
}

// opened is a file read from disk as the editor takes it: read-only where the conventions or a BufRead hook say.
func (h *editorHost) opened(name string, b []byte, f *edit.File, err error) ([]byte, *edit.File, error) {
	if err == nil && f.ReadOnly == "" && !f.New {
		f.ReadOnly = h.x.conv.readOnly(f.Path)
	}
	if err == nil {
		ev := &kit.BufRead{Buf: name, File: f.Path}
		h.x.h.Fire(context.Background(), "BufRead", name, ev)
		if f.ReadOnly == "" {
			f.ReadOnly = ev.ReadOnly
		}
	}
	return b, f, err
}

func (h *editorHost) BeforeWrite(name string, buf *text.Buffer, f *edit.File) error {
	h.x.conv.beforeWrite(name, buf, f)
	ev := &kit.BufWritePre{Buf: name, File: f.Path}
	v, _ := h.x.h.Fire(context.Background(), "BufWritePre", name, ev)
	if v.Veto {
		return fmt.Errorf("not written: %s", cmp.Or(v.Reason, "a BufWritePre hook vetoed it"))
	}
	return nil
}

func (h *editorHost) WriteFile(f *edit.File, t []byte, noLines bool) error {
	if err := h.disk.WriteFile(f, t, noLines); err != nil {
		return err
	}
	h.x.h.Fire(context.Background(), "BufWritePost", f.Path, &kit.BufWritePost{Buf: f.Path, File: f.Path})
	return nil
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
	err := h.x.h.Run(ctx, name, registry.Args{"args": args, "bang": bang})
	if registry.IsNoCommand(err, name) {
		return false
	}
	if err != nil {
		h.x.h.Notify(core.Notice{ID: "editor.command", Severity: core.SevError, Source: "editor", Key: true, Text: err.Error()})
	}
	return true
}

// editorLayout is the engine's windows as views in the IDE's layout: :split splits the pane, CTRL-W moves
// between panes, and a pane dragged elsewhere is where the engine finds it.
type editorLayout struct{ x *Editor }

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
	px := from.Min.X + m.TextX + float32(col)*m.CharW
	py := from.Min.Y + m.CodeTop + float32(row)*m.LineH
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
