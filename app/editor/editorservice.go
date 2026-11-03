package editor

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/vim"
)

// editors is the editor's Editors service.
type editors struct{ x *Editor }

func (s editors) Dir() string { return s.x.dir }

// focused says an editor window has the focus.
func (s editors) focused() bool {
	f := s.x.h.Layout().Focus
	return f != nil && f.ID == kit.EditorView
}

func (s editors) Focused() (registry.EditorBuffer, bool) {
	if !s.focused() {
		return registry.EditorBuffer{}, false
	}
	return s.Current()
}

func (s editors) Current() (registry.EditorBuffer, bool) {
	p, ok := s.x.e.Pane(s.x.e.WindowID())
	if !ok {
		return registry.EditorBuffer{}, false
	}
	c := p.Cursor()
	return registry.EditorBuffer{Path: s.x.path(p), Text: p.Buffer(), Cursor: text.Pos{Line: c.Line, Col: c.Col}, Modified: p.Modified(),
		Bytes: p.FileBytes}, true
}

func (s editors) Buffer(path string) (registry.EditorBuffer, bool) {
	f, ok := s.x.openBuffer(path)
	if !ok {
		return registry.EditorBuffer{}, false
	}
	return fileBuffer(f), true
}

// fileBuffer is a listed buffer with a file, as the service gives it.
func fileBuffer(f vim.FileState) registry.EditorBuffer {
	return registry.EditorBuffer{Path: f.File.Path, Text: f.Text, Modified: f.Modified, Bytes: f.Bytes,
		Encode: func(b []byte) []byte { return edit.Encode(b, f.File.Format, false) }}
}

func (s editors) Buffers() []registry.EditorBuffer {
	if s.x.e == nil {
		return nil
	}
	var out []registry.EditorBuffer
	for _, f := range s.x.e.Files() {
		if f.File != nil {
			out = append(out, fileBuffer(f))
		}
	}
	return out
}

func (s editors) Window(id string) (registry.EditorWindow, bool) {
	if s.x.e == nil {
		return registry.EditorWindow{}, false
	}
	wid, err := s.x.windowID(id)
	if err != nil {
		return registry.EditorWindow{}, false
	}
	p, ok := s.x.e.Pane(wid)
	if !ok {
		return registry.EditorWindow{}, false
	}
	c := p.Cursor()
	w := registry.EditorWindow{ID: strconv.Itoa(int(wid)), Path: s.x.path(p), Cursor: text.Pos{Line: c.Line, Col: c.Col}, Top: p.Top(), Text: p.Buffer()}
	if p.Current() {
		w.Caret = uint64(s.x.caret)
	}
	if wp, _, ok := s.x.presenting(p); ok {
		w.Presenter = wp.name
	}
	return w, true
}

func (s editors) Mode() string { return s.x.e.Mode() }

func (s editors) Recording() (rune, bool) { return s.x.recording() }

func (s editors) At(at registry.At) registry.At {
	e := s.x.e
	at.Buffer, at.Window = e.Name(), strconv.Itoa(int(e.WindowID()))
	if at.Buffer != "" {
		at.File = e.Name()
		if !filepath.IsAbs(at.File) {
			at.File = filepath.Join(s.x.dir, at.File)
		}
		if p, ok := e.Pane(e.WindowID()); ok {
			at.Line = p.Cursor().Line + 1
		}
	}
	return at
}

func (s editors) Pending() registry.Pending { return s.x.pending() }

func (s editors) Hints(keys []string) []registry.Hint {
	if !s.focused() || !isVim(s.x.inputFor(s.x.current())) || s.x.e.Mode() != "normal" {
		return nil
	}
	var out []registry.Hint
	for _, h := range vim.HintsFor(keys) {
		out = append(out, registry.Hint{Keys: h.Keys, Desc: h.Desc})
	}
	return out
}

func (s editors) Shown() bool { return s.x.e != nil && s.x.shown() }

func (s editors) Syntax(buf *text.Buffer) (*syntax.Document, bool) {
	if hl := s.x.docs[buf]; hl != nil && hl.doc != nil {
		return hl.doc, true
	}
	return nil, false
}

func (s editors) SetCursor(pos text.Pos) { s.x.e.SetCursor(vim.Pos(pos)) }

func (s editors) JumpTo(path string, pos text.Pos) error {
	name := path
	if rel, err := filepath.Rel(s.x.dir, path); err == nil && filepath.IsAbs(path) && !strings.HasPrefix(rel, "..") {
		name = rel
	}
	if err := s.x.e.JumpTo(name, vim.Pos(pos)); err != nil {
		return err
	}
	s.x.conv.refresh()
	return nil
}

func (s editors) Edit(rs []text.Replace) error {
	_, err := s.x.current().Editor().Do(rs, 0)
	return err
}

func (s editors) Cmdline() (string, bool) { return s.x.e.Cmdline() }

func (s editors) Selection() (text.Range, bool) {
	if s.x.e == nil {
		return text.Range{}, false
	}
	return visualRange(s.x.e, s.x.current())
}

// visualRange is the Visual selection in the current window, as a range of its text.
func visualRange(e *vim.Engine, p vim.Pane) (text.Range, bool) {
	start, ok := e.Visual()
	if !ok {
		return text.Range{}, false
	}
	end := p.Cursor()
	if end.Line < start.Line || end.Line == start.Line && end.Col < start.Col {
		start, end = end, start
	}
	snap := p.Buf()
	if e.Mode() != "visual" {
		return text.Range{Start: snap.LineStart(start.Line), End: snap.LineEnd(end.Line)}, true
	}
	a := snap.LineStart(start.Line) + min(start.Col, len(snap.Line(start.Line)))
	z := snap.LineStart(end.Line) + min(end.Col+1, len(snap.Line(end.Line)))
	return text.Range{Start: a, End: max(a, z)}, true
}

func (s editors) Quickfix() (string, []registry.QuickfixItem) {
	if s.x.e == nil {
		return "", nil
	}
	items, title, _ := s.x.e.Quickfix()
	var out []registry.QuickfixItem
	for _, q := range items {
		out = append(out, registry.QuickfixItem{Path: q.Name, Line: q.Line, Col: q.Col, Text: q.Text})
	}
	return title, out
}
