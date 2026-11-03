//go:build darwin && !ios

package gio

// The export is in a file of its own: cgo allows only declarations in the preamble of a file with one.

/*
#include <stdint.h>
*/
import "C"

//export orielOcclusion
func orielOcclusion(h C.uintptr_t, occluded C.int) {
	if fn, ok := occlusionWindows.Load(uintptr(h)); ok {
		fn.(func(bool))(occluded != 0)
	}
}
