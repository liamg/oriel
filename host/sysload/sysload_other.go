//go:build !linux && !darwin

package sysload

import "errors"

var errUnsupported = errors.New("not available on this platform")

func readCPU() (float64, error) { return 0, errUnsupported }

func readMem() (used, total uint64, err error) { return 0, 0, errUnsupported }
