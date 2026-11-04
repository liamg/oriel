package filecache

import (
	"bufio"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
)

// New returns a new Cache implemented by fileCache.
func New(dir string) Cache {
	return newFileCache(dir)
}

func newFileCache(dir string) *fileCache {
	return &fileCache{dirPath: dir}
}

// fileCache persists compiled functions into dirPath.
//
// Note: this can be expanded to do binary signing/verification, set TTL on each entry, etc.
type fileCache struct {
	dirPath string
}

func (fc *fileCache) path(key Key) string {
	return path.Join(fc.dirPath, hex.EncodeToString(key[:]))
}

func (fc *fileCache) Get(key Key) (content io.ReadCloser, ok bool, err error) {
	f, err := os.Open(fc.path(key))
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	} else {
		// Oriel's patch: the engine reads the entry eight bytes at a time; buffered, that's a few reads, not one
		// syscall per field.
		return bufferedFile{bufio.NewReaderSize(f, 64<<10), f}, true, nil
	}
}

// bufferedFile reads its file through a buffer, each read filled as a file's is (the engine takes a short read for
// the end), and closes the file.
type bufferedFile struct {
	r *bufio.Reader
	f *os.File
}

func (b bufferedFile) Read(p []byte) (int, error) {
	n, err := io.ReadFull(b.r, p)
	if err == io.ErrUnexpectedEOF {
		err = nil // the rest of the file, as a file's last read gives it
	}
	return n, err
}

func (b bufferedFile) Close() error { return b.f.Close() }

func (fc *fileCache) Add(key Key, content io.Reader) (err error) {
	path := fc.path(key)
	dirPath, fileName := filepath.Split(path)

	// Oriel's patch: the folder made as the cache was set up may be gone since (a cache cleared while the program
	// runs): it's made again, rather than the compile failing for want of somewhere to keep its result.
	if err = os.MkdirAll(dirPath, 0o700); err != nil {
		return
	}
	file, err := os.CreateTemp(dirPath, fileName+".*.tmp")
	if err != nil {
		return
	}
	defer func() {
		file.Close()
		if err != nil {
			_ = os.Remove(file.Name())
		}
	}()
	if _, err = io.Copy(file, content); err != nil {
		return
	}
	if err = file.Sync(); err != nil {
		return
	}
	if err = file.Close(); err != nil {
		return
	}
	err = os.Rename(file.Name(), path)
	return
}

func (fc *fileCache) Delete(key Key) (err error) {
	err = os.Remove(fc.path(key))
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	return
}
