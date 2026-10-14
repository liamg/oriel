//go:build !(darwin && !ios)

package gio

// holdQuit does nothing: only macOS has an app menu whose Quit skips the window's close.
func holdQuit(bool) {}
