package plugin

import (
	"bytes"
	"context"
	"io/fs"
	"os"
)

// File is a file's contents as a Files cache holds them.
type File struct {
	Data  []byte
	Lines []string // Data split at newlines, without them
}

// NewFiles is a cache of files read by path, each watched for changes and bounded by bytes (64 MB when 0).
func NewFiles(h Loop, o CacheOpts[string, File]) *Cache[string, File] {
	if o.MaxBytes == 0 {
		o.MaxBytes = 64 << 20
	}
	o.Size = func(f File) int64 { return int64(2 * len(f.Data)) }
	o.Path = func(p string) string { return p }
	return NewCache(h, func(ctx context.Context, p string) (File, error) {
		b, err := os.ReadFile(p)
		if err != nil {
			return File{}, err
		}
		return File{Data: b, Lines: splitLines(b)}, nil
	}, o)
}

// NewDirs is a cache of folders' entries by path, each watched for changes.
func NewDirs(h Loop, o CacheOpts[string, []fs.DirEntry]) *Cache[string, []fs.DirEntry] {
	o.Path = func(p string) string { return p }
	return NewCache(h, func(ctx context.Context, p string) ([]fs.DirEntry, error) { return os.ReadDir(p) }, o)
}

func splitLines(b []byte) []string {
	b = bytes.TrimSuffix(b, []byte("\n"))
	if len(b) == 0 {
		return nil
	}
	out := make([]string, 0, bytes.Count(b, []byte("\n"))+1)
	for line := range bytes.SplitSeq(b, []byte("\n")) {
		out = append(out, string(bytes.TrimSuffix(line, []byte("\r"))))
	}
	return out
}
