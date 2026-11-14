package kit

import (
	"errors"
	"io/fs"
	"strings"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/ui"
)

// FileSrc is a file's text as it is now: an open buffer's, else what's on disk, read through the window's Reads and
// read again when it changes. What's made from the text (its lines, its highlighting) is kept until the text changes.
type FileSrc struct {
	Src     []byte // nil while the file's first read is under way
	Gen     int    // moves whenever src does
	buf     *text.Buffer
	version uint64
	state   ui.LoadState
	missing bool
	ls      []string
	ref     *srcBytes // this version of Src
	rd      *Reads
	hl      *SourceFile
	hlAt    [3]int // the line, before and after hl was made for
	hlDone  bool   // hl is highlighted, not the plain text shown meanwhile
}

// BufferOn is the editor's buffer open on path, nil for none; r may be nil.
func BufferOn(r registry.Registry, path string) *text.Buffer {
	if r == nil {
		return nil
	}
	if ed, ok := services.EditorsOf(r); ok {
		if b, ok := ed.Buffer(path); ok {
			return b.Text
		}
	}
	return nil
}

// Buffers are the editor's listed buffers that hold files; r may be nil.
func Buffers(r registry.Registry) []services.EditorBuffer {
	if r == nil {
		return nil
	}
	if ed, ok := services.EditorsOf(r); ok {
		return ed.Buffers()
	}
	return nil
}

// Load brings f up to date with path: buf is the buffer open on it, or nil; else the file is read through rd.
func (f *FileSrc) Load(rd *Reads, buf *text.Buffer, path string) *FileSrc {
	if buf != nil {
		if snap := buf.Snapshot(); f.buf != buf || f.version != snap.Version() || f.Src == nil {
			f.set(snap.Bytes())
			f.buf, f.version, f.missing = buf, snap.Version(), false
		}
		f.state = ui.LoadState{Phase: ui.Ready}
		return f
	}
	if rd == nil {
		f.state = ui.LoadState{Phase: ui.Failed, Err: errors.New("nothing reads files here")}
		return f
	}
	f.rd = rd
	file, st := rd.File(path)
	f.state = st
	switch {
	case errors.Is(st.Err, fs.ErrNotExist) || st.Phase == ui.Failed && file.Data == nil:
		if f.buf != nil || f.Src == nil || !f.missing {
			f.set([]byte{})
			f.buf, f.missing = nil, true
		}
	case file.Data != nil || st.Phase == ui.Ready:
		if f.buf != nil || f.Src == nil || f.missing || !SameBytes(f.Src, file.Data) {
			f.set(file.Data)
			f.buf, f.missing = nil, false
		}
	}
	return f
}

// SameBytes is whether a and b are the same slice of memory: a file read again is a new one.
func SameBytes(a, b []byte) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// Loading is whether the file's first read is under way.
func (f *FileSrc) Loading() bool { return f.Src == nil && f.state.Loading() }

// State is the file's read's state.
func (f *FileSrc) State() ui.LoadState { return f.state }

func (f *FileSrc) set(src []byte) {
	if src == nil {
		src = []byte{}
	}
	f.Src, f.ls, f.hl, f.hlDone, f.ref = src, nil, nil, false, &srcBytes{src}
	f.Gen++
}

// Missing is whether there's neither a buffer nor a file.
func (f *FileSrc) Missing() bool { return f.buf == nil && f.missing }

// Lines are the text's lines, without their endings.
func (f *FileSrc) Lines() []string {
	if f.ls == nil {
		f.ls = FileLines(f.Src)
	}
	return f.ls
}

// Around is HighlightAround of the text, highlighted off the loop through the window's reads: plain text until it's
// made.
func (f *FileSrc) Around(r registry.Registry, path string, line, before, after int) *SourceFile {
	at := [3]int{line, before, after}
	if f.hl != nil && f.hlAt == at && f.hlDone {
		return f.hl
	}
	if f.rd == nil {
		f.hl, f.hlAt, f.hlDone = HighlightAround(r, path, f.Src, line, before, after), at, true
		return f.hl
	}
	if f.ref == nil {
		f.ref = &srcBytes{f.Src}
	}
	lang, _ := LanguageFor(r, path, f.Src)
	if hl := f.rd.highlight(highlightKey{path: path, src: f.ref, lang: lang, line: line, before: before, after: after}); hl != nil {
		c := *hl
		f.hl, f.hlAt, f.hlDone = &c, at, true
		return f.hl
	}
	if f.hl == nil || f.hlAt != at {
		f.hl, f.hlAt = highlightIn(nil, path, f.Src, line, before, after), at
	}
	return f.hl
}

// FileSrcs are files' texts by path.
type FileSrcs map[string]*FileSrc

// Of is path's text, as Load has it, read through r's Reads.
func (fs FileSrcs) Of(r registry.Registry, path string) *FileSrc {
	f := fs[path]
	if f == nil {
		f = &FileSrc{}
		fs[path] = f
	}
	rd, _ := ReadsOf(r)
	return f.Load(rd, BufferOn(r, path), path)
}

// SkipDir says a directory named name isn't walked for a project's files: a hidden one, .git, node_modules or vendor.
func SkipDir(name string) bool {
	return strings.HasPrefix(name, ".") || name == "node_modules" || name == "vendor"
}
