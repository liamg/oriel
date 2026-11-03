//go:build unix

package typeset

import (
	"os"

	"golang.org/x/sys/unix"
)

// mapFile maps file read-only. Its pages are the page cache's, not the heap's.
func mapFile(file string) (data []byte, unmap func() error, err error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, nil, err
	}
	if fi.Size() == 0 {
		return nil, func() error { return nil }, nil
	}
	data, err = unix.Mmap(int(f.Fd()), 0, int(fi.Size()), unix.PROT_READ, unix.MAP_SHARED)
	if err != nil {
		return nil, nil, err
	}
	return data, func() error { return unix.Munmap(data) }, nil
}
