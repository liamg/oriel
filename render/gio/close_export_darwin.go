//go:build darwin && !ios

package gio

// The export is in a file of its own: cgo allows only declarations in the preamble of a file with one.

/*
#include <stdint.h>
*/
import "C"

//export orielWindowClosed
func orielWindowClosed(h C.uintptr_t) {
	if fn, ok := closeWindows.Load(uintptr(h)); ok {
		fn.(func())()
	}
}
