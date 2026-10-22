//go:build !linux && !darwin

package fswatch

func newBackend() (backend, error) { return nil, ErrUnsupported }
