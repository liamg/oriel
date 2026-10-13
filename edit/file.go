package edit

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"
	"unicode/utf8"
)

// Format is how a file's text is written: with a UTF-8 byte-order mark or not, and lines ending in CRLF or LF
// ('bomb' and 'fileformat').
type Format struct {
	BOM  bool
	CRLF bool
}

// File is a file as it was last read or written, to write it back the same way and to see when it changes on disk.
type File struct {
	Path     string
	Format   Format
	Mode     fs.FileMode
	ModTime  time.Time
	Size     int64
	Sum      [32]byte
	ReadOnly string // why the file can't be written as text, when it can't: "not valid UTF-8", "binary"
	New      bool   // there was no file
}

var bom = []byte{0xef, 0xbb, 0xbf}

// ReadFile reads a file as text: lines joined by "\n", without the byte-order mark, the CRs of CRLF line endings,
// or the last line's newline. A file that isn't valid UTF-8, or holds a NUL, comes back as it is, read-only.
// A file that doesn't exist reads as empty and New.
func ReadFile(path string) ([]byte, *File, error) {
	f := &File{Path: path}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		f.New, f.Mode = true, 0o644
		return nil, f, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if err := f.stat(b); err != nil {
		return nil, nil, err
	}
	switch {
	case bytes.IndexByte(b, 0) >= 0:
		f.ReadOnly = "binary"
		return b, f, nil
	case !utf8.Valid(b):
		f.ReadOnly = "not valid UTF-8"
		return b, f, nil
	}
	text, format := Decode(b)
	f.Format = format
	return text, f, nil
}

// Decode splits a file's bytes into its text and its format. Lines end in CRLF when every one of them does, as
// vim's 'fileformats' "unix,dos" decides it.
func Decode(b []byte) ([]byte, Format) {
	var f Format
	if bytes.HasPrefix(b, bom) {
		f.BOM, b = true, b[len(bom):]
	}
	if len(b) == 0 {
		return nil, f
	}
	noEOL := b[len(b)-1] != '\n'
	if !noEOL {
		b = b[:len(b)-1]
	}
	lines := bytes.Split(b, []byte("\n"))
	crlf := true
	for i, l := range lines {
		if !bytes.HasSuffix(l, []byte("\r")) && !(i == len(lines)-1 && noEOL) {
			crlf = false
			break
		}
	}
	if crlf && (!noEOL || len(lines) > 1) {
		f.CRLF = true
		for i, l := range lines {
			lines[i] = bytes.TrimSuffix(l, []byte("\r"))
		}
		b = bytes.Join(lines, []byte("\n"))
	}
	return b, f
}

// Encode is text as a file in format f. The last line always gets its newline, as vim's 'fixendofline' does.
// A buffer with no lines at all (not one empty line) writes none.
func Encode(text []byte, f Format, noLines bool) []byte {
	var out bytes.Buffer
	if f.BOM {
		out.Write(bom)
	}
	if noLines {
		return out.Bytes()
	}
	nl := []byte("\n")
	if f.CRLF {
		nl = []byte("\r\n")
	}
	out.Write(bytes.ReplaceAll(text, []byte("\n"), nl))
	out.Write(nl)
	return out.Bytes()
}

// Write writes text to f.Path in f's format, atomically: to a temporary file in the same directory, given the
// file's mode, then renamed over it. f then describes what's on disk.
func (f *File) Write(text []byte, noLines bool) error {
	if f.ReadOnly != "" {
		return errors.New("read-only: " + f.ReadOnly)
	}
	b := Encode(text, f.Format, noLines)
	path, err := filepath.EvalSymlinks(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		path, err = f.Path, nil
	}
	if err != nil {
		return err
	}
	mode := f.Mode
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode()
	} else if mode == 0 {
		mode = 0o644
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".oriel-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode.Perm()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	f.New = false
	return f.stat(b)
}

func (f *File) stat(b []byte) error {
	st, err := os.Stat(f.Path)
	if err != nil {
		return err
	}
	f.Mode, f.ModTime, f.Size, f.Sum = st.Mode(), st.ModTime(), st.Size(), sha256.Sum256(b)
	return nil
}

// Empty reports whether the file holds no lines at all: none, or nothing but a byte-order mark.
func (f *File) Empty() bool {
	return f.New || f.Size == 0 || f.Format.BOM && f.Size == int64(len(bom))
}

// ChangedOnDisk reports whether the file on disk is no longer what f last read or wrote: its size or
// time changed and so did its bytes, or it was deleted.
func (f *File) ChangedOnDisk() (bool, error) {
	st, err := os.Stat(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return !f.New, nil
	}
	if err != nil {
		return false, err
	}
	if f.New {
		return true, nil
	}
	if st.Size() == f.Size && st.ModTime().Equal(f.ModTime) {
		return false, nil
	}
	b, err := os.ReadFile(f.Path)
	if err != nil {
		return false, err
	}
	return sha256.Sum256(b) != f.Sum, nil
}
