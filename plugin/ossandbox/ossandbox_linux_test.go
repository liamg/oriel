package ossandbox

import (
	"net"
	"os"
	"os/exec"
	"testing"
)

// Process plugins get "Landlock + seccomp on Linux … scoped to the
// worktree and the declared fs, net, exec grants". Live, against this kernel.
func TestLandlockConfines(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	work, other, ro := t.TempDir(), t.TempDir(), t.TempDir()
	for _, d := range []string{work, other, ro} {
		if err := os.WriteFile(d+"/f", []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tool, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1)")
	}
	p := Profile{Read: []string{ro}, Write: []string{work}}
	ops := []string{"read:" + work + "/f", "write:" + work + "/g", "read:" + ro + "/f", "write:" + ro + "/g",
		"read:" + other + "/f", "write:" + other + "/g", "exec:" + tool}
	expect(t, ops, run(t, p, ops...), true, true, true, false, false, false, false)

	p.Exec = []string{tool}
	expect(t, ops[6:], run(t, p, ops[6:]...), true)
}

// With no ports there's no network at all: TCP is refused by Landlock and the seccomp filter, UDP by the filter.
// A listed port is reachable, and only that one.
func TestLandlockNetwork(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	l2, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	defer l2.Close()
	addr, addr2 := l.Addr().String(), l2.Addr().String()
	ops := []string{"tcp:" + addr, "udp:127.0.0.1:9", "tcp:" + addr2}
	expect(t, ops, run(t, Profile{}, ops...), false, false, false)
	if ABI() < 4 {
		t.Skip("Landlock before ABI 4 can't filter by port")
	}
	port := l.Addr().(*net.TCPAddr).Port
	expect(t, ops, run(t, Profile{Ports: []int{port}}, ops...), true, true, false)
}
