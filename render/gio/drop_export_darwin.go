//go:build darwin && !ios

package gio

// The export is in a file of its own: cgo allows only declarations in the preamble of a file with one.

/*
#include <stdint.h>
*/
import "C"

import "strings"

//export orielDropped
func orielDropped(h C.uintptr_t, paths *C.char, x, y C.double) {
	if fn, ok := dropWindows.Load(uintptr(h)); ok {
		fn.(func([]string, float64, float64))(strings.Split(C.GoString(paths), "\n"), float64(x), float64(y))
	}
}
