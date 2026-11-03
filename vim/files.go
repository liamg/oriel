package vim

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/edit"
	"github.com/liamg/oriel/text"
)

// Host is the IDE the engine runs in: it reads and writes files, ends the editing when the last window
// quits, and owns the commands it shows in its own views (:ls opens its switcher, :copen its results panel).
type Host interface {
	ReadFile(name string) ([]byte, *edit.File, error) // a missing file reads as empty and New
	WriteFile(f *edit.File, text []byte, noLines bool) error
	Exists(name string) bool
	Quit()
	// Command runs an ex command the IDE shows itself, or one the engine doesn't know (a palette command).
	// It reports false when the IDE doesn't have it either.
	Command(name, args string, bang bool) bool
}

// BeforeWriter is a Host that changes a buffer or its file's format just before the buffer is written: trims
// trailing white space, sets the line endings (vim's BufWritePre). An error stops the write, and is its message.
type BeforeWriter interface {
	BeforeWrite(name string, buf *text.Buffer, f *edit.File) error
}

// Disk is the Host for files on this machine, with names relative to Dir, and nothing to quit or show.
type Disk struct{ Dir string }

func (d Disk) path(name string) string {
	if filepath.IsAbs(name) || d.Dir == "" {
		return name
	}
	return filepath.Join(d.Dir, name)
}

func (d Disk) ReadFile(name string) ([]byte, *edit.File, error)        { return edit.ReadFile(d.path(name)) }
func (d Disk) WriteFile(f *edit.File, text []byte, noLines bool) error { return f.Write(text, noLines) }
func (d Disk) Quit()                                                   {}
func (d Disk) Command(string, string, bool) bool                       { return false }

func (d Disk) Exists(name string) bool {
	_, err := os.Stat(d.path(name))
	return !errors.Is(err, fs.ErrNotExist)
}

// SetHost puts the engine in an IDE.
func (e *Engine) SetHost(h Host) { e.host = h }

// markSaved makes the buffer's current undo step the one its file holds: undoing back to it clears 'modified'.
func (b *buffer) markSaved() {
	b.savedSeq = b.text.UndoSeq()
	b.modified = false
}

// changed is how a buffer's 'modified' follows its text: set unless the text is back at the saved step.
func (b *buffer) changed() {
	b.modified = b.text.UndoSeq() != b.savedSeq
	b.noLines = false
}

// Modified reports whether the current buffer has changes not written.
func (e *Engine) Modified() bool { return e.buffer.modified }

// Name is the current buffer's name: its file, as given.
func (e *Engine) Name() string { return e.buffer.name }

// ReadOnly says why the current buffer can't be written, or "".
func (e *Engine) ReadOnly() string {
	if e.buffer.file == nil {
		return ""
	}
	return e.buffer.file.ReadOnly
}

// Edit is :e name, as opening a file from the IDE does.
func (e *Engine) Edit(name string) {
	if err := e.exBuffer("edit", name, exRange{}, false); err != nil {
		e.fail(err)
	}
	e.sync()
}

// JumpTo goes to p in the file name, as a tag jump does: a jump in the current window, which edits name when it
// isn't the current buffer's. CTRL-O comes back.
func (e *Engine) JumpTo(name string, p Pos) error {
	if name != e.buffer.name {
		b, err := e.openFile(name, true)
		if err == nil {
			err = e.editBuffer(b, false)
		}
		if err != nil {
			return err
		}
	} else {
		e.setPcmark()
	}
	v := e.view()
	e.cur = p
	e.clamp(v)
	e.setWant(v)
	e.openCursorFold()
	e.updateTop(v)
	e.sync()
	return nil
}

// openFile finds the buffer named name, or reads its file into a new one. With reuse, a current buffer that's
// empty, unnamed and unchanged takes the file instead, as vim's buflist_new reuses it.
func (e *Engine) openFile(name string, reuse bool) (*buffer, error) {
	var b *buffer
	for _, o := range e.buffers {
		if o.name == name {
			o.unlisted = false
			if o.file != nil || o.modified || o.text.Snapshot().Len() > 0 {
				return o, nil
			}
			b = o // known by name, never read
		}
	}
	data, f, err := e.host.ReadFile(name)
	if err != nil {
		return nil, errReadFailed.with(name, err)
	}
	cur := e.buffer
	if b == nil && reuse && cur != nil && cur.name == "" && !cur.modified && cur.file == nil && cur.text.Snapshot().Len() == 0 && cur.qf == nil {
		b = cur
	}
	if b == nil {
		b = e.newBuffer(text.New(data, text.Author{Kind: text.File}))
	} else {
		e.setText(b, text.New(data, text.Author{Kind: text.File}))
		for _, w := range e.windows {
			if w.buf == b {
				w.ed = e.ed.Open(b.text)
			}
		}
	}
	b.name, b.file = name, f
	b.markSaved()
	b.noLines = f.Empty()
	if f.ReadOnly != "" {
		e.notify(msgReadOnlyFile.with(name, f.ReadOnly))
	}
	return b, nil
}

// reload is :e!: the buffer goes back to what's on disk.
func (e *Engine) reload() error {
	b := e.buffer
	if b.name == "" {
		e.unloadCurrent()
		return nil
	}
	data, f, err := e.host.ReadFile(b.name)
	if err != nil {
		return errReadFailed.with(b.name, err)
	}
	v := e.view()
	e.do(e.newGroup(), text.Replace{Range: text.Range{Start: 0, End: v.s.Len()}, Text: data})
	b.file = f
	b.markSaved()
	b.noLines = f.Empty()
	e.clamp(e.view())
	return nil
}

// Reload is the IDE reloading a buffer whose file changed on disk.
func (e *Engine) Reload() error {
	err := e.reload()
	e.sync()
	return err
}

// write writes b to name ("" for its own file). A write to another name doesn't change which file the buffer is,
// except for a buffer with no name yet, which takes it.
func (e *Engine) write(b *buffer, name string, force bool, then func()) error {
	own := name == "" || name == b.name
	if name == "" {
		name = b.name
	}
	if name == "" {
		return errWriteNoName.with()
	}
	f := b.file
	if !own || f == nil {
		if !force && e.host.Exists(name) && (!own || f == nil && b.name != "") {
			return errFileExists.with()
		}
		_, nf, err := e.host.ReadFile(name)
		if err != nil {
			nf = &edit.File{Path: name, New: true}
		}
		nf.ReadOnly = ""
		if f != nil {
			nf.Format = f.Format
		}
		f = nf
	}
	if f.ReadOnly != "" {
		return errReadOnlyFile.with(name, f.ReadOnly)
	}
	do := func() error {
		if h, ok := e.host.(BeforeWriter); ok {
			if err := h.BeforeWrite(b.name, b.text, f); err != nil {
				return err
			}
		}
		data := b.text.Snapshot().Bytes()
		isNew := f.New
		if err := e.host.WriteFile(f, data, b.noLines); err != nil {
			return errWriteFailed.with(name, err)
		}
		lines := strings.Count(string(data), "\n") + 1
		if len(data) == 0 {
			lines = 0
		}
		if b.noLines {
			lines = 0
		}
		e.notify(msgWritten.with(name, flag(isNew), lines, len(edit.Encode(data, f.Format, b.noLines))))
		if own || b.name == "" {
			b.name, b.file = name, f
			b.notEdited = false
			b.markSaved()
		}
		if then != nil {
			then()
		}
		return nil
	}
	if own && !force && f != nil && !f.New {
		if changed, _ := f.ChangedOnDisk(); changed {
			e.confirm = &confirmation{yes: func() {
				if err := do(); err != nil {
					e.fail(err)
				}
			}}
			e.notify(askWriteChanged.with(name))
			return nil
		}
	}
	return do()
}

// quit closes the current window: the last one ends the editing, unless a buffer has unsaved changes.
func (e *Engine) quit(force bool) error {
	if len(e.order()) > 1 {
		if !force && !e.opts.Hidden && e.buffer.modified && !e.shownElsewhere(e.buffer) {
			return errUnsaved.with()
		}
		e.closeWindow(e.window)
		return nil
	}
	if !force {
		if e.buffer.modified {
			return errUnsaved.with()
		}
		for _, b := range e.buffers {
			if b.modified && !b.unlisted {
				return errUnsavedFile.with(b.displayName())
			}
		}
	}
	e.host.Quit()
	return nil
}

// quitAll is :qa: everything ends, unless a buffer has unsaved changes.
func (e *Engine) quitAll(force bool) error {
	if !force {
		for _, b := range e.buffers {
			if b.modified && !b.unlisted {
				return errUnsavedFile.with(b.displayName())
			}
		}
	}
	e.host.Quit()
	return nil
}

// writeAll is :wa: every changed buffer with a name is written.
func (e *Engine) writeAll(force bool) error {
	for _, b := range e.buffers {
		if !b.modified || b.unlisted {
			continue
		}
		if b.name == "" {
			return errNoNameForBuffer.with(b.num)
		}
		if err := e.write(b, "", force, nil); err != nil {
			return err
		}
	}
	return nil
}

// fileCmd runs the ex commands that read, write and quit; false when name isn't one.
func (e *Engine) fileCmd(name, args string, bang bool) (bool, error) {
	args = strings.TrimSpace(args)
	switch name {
	case "write":
		return true, e.write(e.buffer, args, bang, nil)
	case "update":
		if !e.buffer.modified {
			return true, nil
		}
		return true, e.write(e.buffer, args, bang, nil)
	case "saveas":
		if args == "" {
			return true, errArgRequired.with()
		}
		if !bang && e.host.Exists(args) {
			return true, errFileExists.with()
		}
		b := e.buffer
		old := b.name
		b.name, b.file = args, nil
		if err := e.write(b, args, true, nil); err != nil {
			b.name = old
			return true, err
		}
		if old != "" {
			alt := e.emptyBuffer()
			alt.name, alt.unlisted = old, true
			e.window.alt = alt
		}
		return true, nil
	case "wall":
		return true, e.writeAll(bang)
	case "wq":
		return true, e.write(e.buffer, args, bang, func() { e.fail(e.quit(bang)) })
	case "xit", "exit":
		if e.buffer.modified {
			return true, e.write(e.buffer, args, bang, func() { e.fail(e.quit(bang)) })
		}
		return true, e.quit(bang)
	case "qall", "quitall":
		return true, e.quitAll(bang)
	case "wqall", "xall":
		if err := e.writeAll(bang); err != nil {
			return true, err
		}
		return true, e.quitAll(bang)
	}
	return false, nil
}

// File is the current buffer's file, or nil when it has none yet.
func (e *Engine) File() *edit.File { return e.buffer.file }

// FileState is a buffer that has a file, for the IDE to watch.
type FileState struct {
	Num      int
	Name     string
	File     *edit.File
	Modified bool
	Text     *text.Buffer
	noLines  bool
}

// Bytes is the buffer as writing it would put it on disk.
func (f FileState) Bytes() []byte { return fileBytes(f.Text, f.File, f.noLines) }

func fileBytes(t *text.Buffer, f *edit.File, noLines bool) []byte {
	var format edit.Format
	if f != nil {
		format = f.Format
	}
	return edit.Encode(t.Snapshot().Bytes(), format, noLines)
}

// Files is every listed buffer with a file.
func (e *Engine) Files() []FileState {
	var out []FileState
	for _, b := range e.buffers {
		if b.file != nil && !b.unlisted {
			out = append(out, FileState{Num: b.num, Name: b.name, File: b.file, Modified: b.modified, Text: b.text, noLines: b.noLines})
		}
	}
	return out
}

// ReloadBuffer reads buffer num's file again, as :e! would in a window showing it.
func (e *Engine) ReloadBuffer(num int) error {
	b := e.bufferByNum(num)
	if b == nil || b.name == "" {
		return errNoBuffer.with(num)
	}
	data, f, err := e.host.ReadFile(b.name)
	if err != nil {
		return errReadFailed.with(b.name, err)
	}
	return e.ReloadBufferFrom(num, data, f)
}

// ReloadBufferFrom is ReloadBuffer with the file already read: data as f is now.
func (e *Engine) ReloadBufferFrom(num int, data []byte, f *edit.File) error {
	b := e.bufferByNum(num)
	if b == nil || b.name == "" {
		return errNoBuffer.with(num)
	}
	g := b.text.NewGroup()
	if _, err := b.text.Apply(text.Edit{Ranges: []text.Replace{{Range: text.Range{End: b.text.Snapshot().Len()}, Text: data}},
		Author: text.Author{Kind: text.File}, Group: g}); err != nil {
		return err
	}
	b.file = f
	b.markSaved()
	b.noLines = f.Empty()
	if e.buffer == b {
		e.clamp(e.view())
	}
	e.sync()
	return nil
}
