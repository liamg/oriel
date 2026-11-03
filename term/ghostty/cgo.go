//go:build ghostty_native && cgo

package ghostty

/*
#cgo CFLAGS: -I${SRCDIR}/native/include -O2
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/native/lib/linux_amd64/libghostty-vt.a
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/native/lib/linux_arm64/libghostty-vt.a
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/native/lib/darwin_amd64/libghostty-vt.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/native/lib/darwin_arm64/libghostty-vt.a
#cgo LDFLAGS: -lm
#include <string.h>
#include "shim_native.h"
*/
import "C"

import (
	"errors"
	"unsafe"
)

const nativeBuilt = true

// nativeMachine is the shim, and Ghostty, in this process.
type nativeMachine struct{ t unsafe.Pointer }

func newNative(cols, rows, scrollback int) (machine, error) {
	t := C.oriel_new(C.u32(cols), C.u32(rows), C.u32(scrollback))
	if t == nil {
		return nil, errors.New("out of memory")
	}
	return &nativeMachine{t: t}, nil
}

func (n *nativeMachine) head() *C.TermHead { return (*C.TermHead)(n.t) }

func u(a uint64) C.u32                     { return C.u32(a) }
func i(a uint64) C.int                     { return C.int(int32(uint32(a))) }
func r(v C.u32) uint64                     { return uint64(v) }
func ri(v C.int) uint64                    { return uint64(uint32(int32(v))) }
func ref(a uint64) C.GhosttyTrackedGridRef { return C.oriel_ref(C.uintptr_t(a)) }

func (n *nativeMachine) call(name string, a ...uint64) uint64 {
	t := n.t
	switch name {
	case "oriel_free":
		C.oriel_free(t)
	case "oriel_input":
		return r(C.oriel_input(t, u(a[0])))
	case "oriel_write":
		C.oriel_write(t, u(a[0]))
	case "oriel_take":
		return r(C.oriel_take(t))
	case "oriel_resize":
		return ri(C.oriel_resize(t, u(a[0]), u(a[1])))
	case "oriel_title":
		return r(C.oriel_title(t))
	case "oriel_pwd":
		return r(C.oriel_pwd(t))
	case "oriel_mode":
		return ri(C.oriel_mode(t, u(a[0]), u(a[1])))
	case "oriel_state":
		return r(C.oriel_state(t))
	case "oriel_grid":
		return r(C.oriel_grid(t, u(a[0]), u(a[1])))
	case "oriel_cursor":
		return r(C.oriel_cursor(t))
	case "oriel_scroll":
		C.oriel_scroll(t, u(a[0]), i(a[1]))
	case "oriel_text":
		return r(C.oriel_text(t, u(a[0]), u(a[1]), u(a[2]), u(a[3])))
	case "oriel_mark":
		return uint64(C.oriel_refval(C.oriel_mark(t, u(a[0]), i(a[1]))))
	case "oriel_mark_point":
		return r(C.oriel_mark_point(t, ref(a[0])))
	case "oriel_mark_free":
		C.oriel_mark_free(ref(a[0]))
	case "oriel_key":
		return r(C.oriel_key(t, u(a[0]), u(a[1]), u(a[2]), u(a[3]), u(a[4])))
	case "oriel_paste":
		return r(C.oriel_paste(t, u(a[0])))
	case "oriel_snapshot":
		return r(C.oriel_snapshot(t))
	case "oriel_restore":
		return ri(C.oriel_restore(t, u(a[0]), u(a[1])))
	default:
		panic("ghostty: no " + name)
	}
	return 0
}

func (n *nativeMachine) out(l uint64) []byte {
	if l == 0 {
		return nil
	}
	return unsafe.Slice((*byte)(unsafe.Pointer(n.head().out)), l)
}

func (n *nativeMachine) in(p []byte) {
	if uint32(len(p)) > uint32(n.head().in_cap) && C.oriel_input(n.t, C.u32(len(p))) == 0 {
		panic("ghostty: out of memory")
	}
	if len(p) > 0 {
		C.memcpy(unsafe.Pointer(n.head().in), unsafe.Pointer(&p[0]), C.size_t(len(p)))
	}
}

func (n *nativeMachine) replyLen() uint32 { return uint32(n.head().reply_len) }
func (n *nativeMachine) events() uint32   { return uint32(n.head().events) }
func (n *nativeMachine) held() bool       { return n.head().held != 0 }
func (n *nativeMachine) close() error     { return nil } // oriel_free has run
