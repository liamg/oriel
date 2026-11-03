package ossandbox

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/unix"
)

// linuxRead are what any program on Linux reads: its libraries and the loader's cache, certificates, name
// resolution and time zones, the safe devices, and its own /proc entry.
var linuxRead = []string{"/usr", "/lib", "/lib32", "/lib64", "/etc/ld.so.cache", "/etc/ld.so.conf", "/etc/ld.so.conf.d",
	"/etc/ssl", "/etc/ca-certificates", "/etc/pki", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf",
	"/etc/gai.conf", "/etc/host.conf", "/etc/localtime", "/etc/services", "/etc/protocols", "/dev/zero",
	"/dev/random", "/dev/urandom", "/proc/self", "/sys/kernel/mm/transparent_hugepage"}

// linuxWrite are written by programs that mean nothing by it.
var linuxWrite = []string{"/dev/null"}

// Landlock's rights by the ABI version that brought them.
const (
	fsV1 = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM
	fileRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_TRUNCATE | unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	readRights  = unix.LANDLOCK_ACCESS_FS_READ_FILE | unix.LANDLOCK_ACCESS_FS_READ_DIR
	writeRights = readRights | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR |
		unix.LANDLOCK_ACCESS_FS_REMOVE_FILE | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_SYM |
		unix.LANDLOCK_ACCESS_FS_REFER | unix.LANDLOCK_ACCESS_FS_TRUNCATE
	execRights = unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_READ_FILE
	netRights  = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
	scopes     = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
	ruleNet    = 2 // LANDLOCK_RULE_NET_PORT
)

// netPortAttr is struct landlock_net_port_attr.
type netPortAttr struct {
	Allowed uint64
	Port    uint64
}

// ABI is the kernel's Landlock version, 0 without it.
func ABI() int {
	v, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return 0
	}
	return int(v)
}

// Available says whether the kernel has Landlock.
func Available() error {
	if ABI() < 1 {
		return fmt.Errorf("%w: the kernel has no Landlock", ErrUnavailable)
	}
	return nil
}

func command(p Profile, path string, args []string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	return exec.Command(self, append([]string{flagProfile + encode(p), path}, args...)...), nil
}

// Main is the helper: run with the profile as its first argument, it confines itself to it and execs the program
// its other arguments name. Otherwise it returns at once.
func Main() {
	if len(os.Args) < 2 || !strings.HasPrefix(os.Args[1], flagProfile) {
		return
	}
	spec := strings.TrimPrefix(os.Args[1], flagProfile)
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "oriel: sandbox:", err)
		os.Exit(126)
	}
	var p Profile
	if err := json.Unmarshal([]byte(spec), &p); err != nil {
		fail(err)
	}
	if len(os.Args) < 3 {
		fail(fmt.Errorf("no program"))
	}
	runtime.LockOSThread() // the thread confined is the one that execs
	if err := confine(p); err != nil {
		fail(err)
	}
	fail(unix.Exec(os.Args[2], os.Args[2:], os.Environ()))
}

// confine locks this thread down to p: a Landlock ruleset, no new privileges, and with no ports a seccomp filter
// refusing every socket but a Unix one.
func confine(p Profile) error {
	abi := ABI()
	if abi < 1 {
		return fmt.Errorf("the kernel has no Landlock")
	}
	fs := uint64(fsV1)
	if abi >= 2 {
		fs |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		fs |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		fs |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	attr := unix.LandlockRulesetAttr{Handled_access_fs: fs}
	if abi >= 4 {
		attr.Handled_access_net = netRights
	}
	if abi >= 6 {
		attr.Scoped = scopes
	}
	fd, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return fmt.Errorf("landlock_create_ruleset: %v", errno)
	}
	defer unix.Close(int(fd))
	add := func(paths []string, rights uint64) error {
		for _, path := range paths {
			if err := addPath(int(fd), path, rights&fs); err != nil {
				return err
			}
		}
		return nil
	}
	if err := add(append(linuxRead, p.Read...), readRights); err != nil {
		return err
	}
	if err := add(append(linuxWrite, p.Write...), writeRights); err != nil {
		return err
	}
	if err := add(runs(p), execRights); err != nil {
		return err
	}
	if abi >= 4 {
		for _, port := range p.Ports {
			a := netPortAttr{Allowed: unix.LANDLOCK_ACCESS_NET_CONNECT_TCP, Port: uint64(port)}
			if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, fd, ruleNet, uintptr(unsafe.Pointer(&a)), 0, 0, 0); errno != 0 {
				return fmt.Errorf("landlock_add_rule port %d: %v", port, errno)
			}
		}
	}
	if err := unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0); err != nil {
		return fmt.Errorf("no_new_privs: %v", err)
	}
	if len(p.Ports) == 0 {
		if err := noSockets(); err != nil {
			return err
		}
	}
	if _, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, fd, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_restrict_self: %v", errno)
	}
	return nil
}

// addPath allows rights under path, or on it for a file (which takes only file rights). A path that isn't
// there is skipped: not every system has every one.
func addPath(ruleset int, path string, rights uint64) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return nil
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		rights &= fileRights
	}
	if rights == 0 {
		return nil
	}
	a := unix.LandlockPathBeneathAttr{Allowed_access: rights, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH,
		uintptr(unsafe.Pointer(&a)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("landlock_add_rule %s: %v", path, errno)
	}
	return nil
}
