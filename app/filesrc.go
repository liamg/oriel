package app

import (
	"os"
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/text"
)

// fileSrc is a file's text as it is now: an open buffer's, else what's on disk, read again when its size or mtime
// moves. What's made from the text (its lines, its highlighting) is kept until the text changes.
type fileSrc struct {
	src     []byte
	gen     int // moves whenever src does
	buf     *text.Buffer
	version uint64
	size    int64 // -1 for a file that isn't there
	mod     time.Time
	ls      []string
	hl      *sourceFile
	hlAt    [3]int // the line, before and after hl was made for
}

// bufferOn is the buffer open on path, nil for none.
func bufferOn(ed *editorPlugin, path string) *text.Buffer {
	if ed == nil || ed.e == nil {
		return nil
	}
	if f, ok := ed.openBuffer(path); ok {
		return f.Text
	}
	return nil
}

// load brings f up to date with path: buf is the buffer open on it, or nil; stat says to look at the file on disk
// again.
func (f *fileSrc) load(buf *text.Buffer, path string, stat bool) *fileSrc {
	if buf != nil {
		if snap := buf.Snapshot(); f.buf != buf || f.version != snap.Version() || f.src == nil {
			f.set(snap.Bytes())
			f.buf, f.version = buf, snap.Version()
		}
		return f
	}
	if f.buf == nil && f.src != nil && !stat {
		return f
	}
	fi, err := os.Stat(path)
	switch {
	case err != nil:
		if f.buf != nil || f.src == nil || f.size != -1 {
			f.set([]byte{})
			f.buf, f.size = nil, -1
		}
	case f.buf != nil || f.src == nil || fi.Size() != f.size || !fi.ModTime().Equal(f.mod):
		b, _ := os.ReadFile(path)
		f.set(b)
		f.buf, f.size, f.mod = nil, fi.Size(), fi.ModTime()
	}
	return f
}

func (f *fileSrc) set(src []byte) {
	if src == nil {
		src = []byte{}
	}
	f.src, f.ls, f.hl = src, nil, nil
	f.gen++
}

// missing is whether there's neither a buffer nor a file.
func (f *fileSrc) missing() bool { return f.buf == nil && f.size == -1 }

// lines are the text's lines, without their endings.
func (f *fileSrc) lines() []string {
	if f.ls == nil {
		f.ls = fileLines(f.src)
	}
	return f.ls
}

// around is highlightAround of the text.
func (f *fileSrc) around(r registry.Registry, path string, line, before, after int) *sourceFile {
	if at := [3]int{line, before, after}; f.hl == nil || f.hlAt != at {
		f.hl, f.hlAt = highlightAround(r, path, f.src, line, before, after), at
	}
	return f.hl
}

// fileSrcs are files' texts by path.
type fileSrcs map[string]*fileSrc

// of is path's text, as load has it.
func (fs fileSrcs) of(ed *editorPlugin, path string, stat bool) *fileSrc {
	f := fs[path]
	if f == nil {
		f = &fileSrc{}
		fs[path] = f
	}
	return f.load(bufferOn(ed, path), path, stat)
}
