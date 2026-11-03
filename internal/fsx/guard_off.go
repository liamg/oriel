//go:build !oriel_framecheck

package fsx

// Checking says calls are checked: this is an oriel_framecheck build.
const Checking = false

// Enter marks the calling goroutine as in a frame until leave; only an oriel_framecheck build checks.
func Enter() (leave func()) { return func() {} }

func guard(string) {}
