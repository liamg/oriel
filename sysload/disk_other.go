//go:build !unix

package sysload

import "errors"

func Disk(string) (free, total uint64, err error) {
	return 0, 0, errors.New("not available on this platform")
}
