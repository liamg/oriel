//go:build !unix

package lsptest

// Alive can't tell where there are no unix signals, so it says the process is gone.
func Alive(pid int) bool { return false }
