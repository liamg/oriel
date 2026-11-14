//go:build !linux && !(darwin && cgo)

package sysload

import "errors"

var errUnsupported = errors.New("not available on this platform")

type tickCarry struct{}

func ticks(*tickCarry) (cpuTimes, error) { return cpuTimes{}, errUnsupported }

func readMem() (used, total uint64, err error) { return 0, 0, errUnsupported }
