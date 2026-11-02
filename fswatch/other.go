//go:build !linux && !darwin

package fswatch

import (
	"os"
	"time"
)

func newBackend() (backend, error) { return nil, ErrUnsupported }

// changedAt is when fi's file last changed, as far as this OS says.
func changedAt(fi os.FileInfo) time.Time { return fi.ModTime() }

// madeAt is when fi's file was made, as far as this OS says.
func madeAt(fi os.FileInfo) time.Time { return fi.ModTime() }
