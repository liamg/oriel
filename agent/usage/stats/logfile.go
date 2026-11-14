package stats

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/liamg/oriel/zstd"
)

// tail is what the log at path holds past offset, and the size of the file it read; shrunk says it holds less than
// offset. A log compressed with zstd (.zst) reads decompressed, offset counting its plain bytes, as does a path
// compressed since (path.zst in its place) or one gone back to plain.
func tail(path string, offset int64) (data []byte, size int64, shrunk bool, err error) {
	f, compressed, err := openLog(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, 0, false, err
	}
	if !compressed {
		if fi.Size() < offset {
			return nil, fi.Size(), true, nil
		}
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return nil, 0, false, err
		}
		data, err = io.ReadAll(f)
		return data, fi.Size(), false, err
	}
	z, err := zstd.NewReader(f)
	if err != nil {
		return nil, 0, false, err
	}
	defer z.Close()
	if n, err := io.CopyN(io.Discard, z, offset); n < offset {
		if err == io.EOF {
			return nil, fi.Size(), true, nil
		}
		return nil, 0, false, err
	}
	data, err = io.ReadAll(z)
	return data, fi.Size(), false, err
}

// statLog stats the file tail reads for path.
func statLog(path string) (fs.FileInfo, error) {
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		if fi, err2 := os.Stat(otherForm(path)); err2 == nil {
			return fi, nil
		}
	}
	return fi, err
}

// otherForm is a log's compressed path for its plain one, and its plain path for its compressed one.
func otherForm(path string) string {
	if plain, ok := strings.CutSuffix(path, ".zst"); ok {
		return plain
	}
	return path + ".zst"
}

// openLog opens path, or the other of its plain and .zst forms when it's gone, saying whether what it opened is
// compressed.
func openLog(path string) (*os.File, bool, error) {
	f, err := os.Open(path)
	if err == nil {
		return f, strings.HasSuffix(path, ".zst"), nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, false, err
	}
	other := otherForm(path)
	g, err2 := os.Open(other)
	if err2 != nil {
		return nil, false, err
	}
	return g, strings.HasSuffix(other, ".zst"), nil
}
