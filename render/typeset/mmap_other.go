//go:build !unix

package typeset

import "os"

// mapFile reads file where mapping isn't available.
func mapFile(file string) (data []byte, unmap func() error, err error) {
	data, err = os.ReadFile(file)
	return data, func() error { return nil }, err
}
