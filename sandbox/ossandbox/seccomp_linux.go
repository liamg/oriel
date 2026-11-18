package ossandbox

import (
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/unix"
)

// auditArch is this architecture as seccomp names it; a syscall made as another (32-bit on 64) is refused.
var auditArch = map[string]uint32{"amd64": unix.AUDIT_ARCH_X86_64, "arm64": unix.AUDIT_ARCH_AARCH64}

func stmt(code uint16, k uint32) unix.SockFilter { return unix.SockFilter{Code: code, K: k} }

func jump(code uint16, k uint32, jt, jf uint8) unix.SockFilter {
	return unix.SockFilter{Code: code, Jt: jt, Jf: jf, K: k}
}

// noSockets installs a seccomp filter on this thread: socket() fails with EACCES for every family but AF_UNIX,
// and io_uring (which can open sockets itself) isn't there. Landlock covers TCP; this covers UDP, raw and the rest.
func noSockets() error {
	arch, ok := auditArch[runtime.GOARCH]
	if !ok {
		return fmt.Errorf("no seccomp filter for %s", runtime.GOARCH)
	}
	const (
		ld   = unix.BPF_LD | unix.BPF_W | unix.BPF_ABS
		jeq  = unix.BPF_JMP | unix.BPF_JEQ | unix.BPF_K
		jge  = unix.BPF_JMP | unix.BPF_JGE | unix.BPF_K
		ret  = unix.BPF_RET | unix.BPF_K
		deny = unix.SECCOMP_RET_ERRNO | uint32(unix.EACCES)
	)
	prog := []unix.SockFilter{
		stmt(ld, 4), // seccomp_data.arch
		jump(jeq, arch, 1, 0),
		stmt(ret, deny),
		stmt(ld, 0),                 // seccomp_data.nr
		jump(jge, 0x40000000, 0, 1), // the x32 ABI's syscalls
		stmt(ret, deny),
		jump(jeq, unix.SYS_IO_URING_SETUP, 0, 1),
		stmt(ret, unix.SECCOMP_RET_ERRNO|uint32(unix.ENOSYS)),
		jump(jeq, unix.SYS_SOCKET, 1, 0),
		stmt(ret, unix.SECCOMP_RET_ALLOW),
		stmt(ld, 16), // the low word of args[0], the family
		jump(jeq, unix.AF_UNIX, 0, 1),
		stmt(ret, unix.SECCOMP_RET_ALLOW),
		stmt(ret, deny),
	}
	fp := unix.SockFprog{Len: uint16(len(prog)), Filter: &prog[0]}
	if err := unix.Prctl(unix.PR_SET_SECCOMP, unix.SECCOMP_MODE_FILTER, uintptr(unsafe.Pointer(&fp)), 0, 0); err != nil {
		return fmt.Errorf("seccomp: %v", err)
	}
	return nil
}
