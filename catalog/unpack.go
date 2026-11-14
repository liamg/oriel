package catalog

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/liamg/oriel/plugin"
)

// Unpack extracts a plugin archive into dir, and lists the files it held. Only regular files are taken; a name
// leading out of dir, or more than plugin.MaxSize in all, is an error.
func Unpack(archive, dir string) ([]string, error) {
	f, err := os.Open(archive)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	var files []string
	var total int64
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if !fs.ValidPath(h.Name) {
			return nil, fmt.Errorf("%s: %q leads out of the plugin", filepath.Base(archive), h.Name)
		}
		if total += h.Size; total > plugin.MaxSize {
			return nil, fmt.Errorf("%s is over %s unpacked", filepath.Base(archive), mb(plugin.MaxSize))
		}
		to := filepath.Join(dir, filepath.FromSlash(h.Name))
		if err := os.MkdirAll(filepath.Dir(to), 0o755); err != nil {
			return nil, err
		}
		mode := os.FileMode(0o644)
		if h.Mode&0o111 != 0 {
			mode = 0o755
		}
		w, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return nil, err
		}
		_, err = io.Copy(w, io.LimitReader(tr, h.Size))
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, err
		}
		files = append(files, h.Name)
	}
	slices.Sort(files)
	return files, nil
}
