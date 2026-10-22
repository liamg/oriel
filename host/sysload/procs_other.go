//go:build !linux && !(darwin && cgo)

package sysload

func procs() ([]Proc, error) { return nil, errUnsupported }

func details([]Proc) {}
