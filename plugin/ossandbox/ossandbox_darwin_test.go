package ossandbox

import (
	"os"
	"testing"
)

// PL-2, live on macOS: sandbox-exec with the generated profile lets the process read and write its paths, and
// nothing else.
func TestSandboxExecConfines(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	work, other := t.TempDir(), t.TempDir()
	for _, d := range []string{work, other} {
		if err := os.WriteFile(d+"/f", []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := Profile{Write: []string{work}}
	ops := []string{"read:" + work + "/f", "write:" + work + "/g", "read:" + other + "/f", "write:" + other + "/g", "exec:/bin/echo",
		"tcp:example.com:443"}
	expect(t, ops, run(t, p, ops...), true, true, false, false, false, false)
	p.Exec = []string{"/bin/echo"}
	expect(t, ops[4:5], run(t, p, ops[4:5]...), true)
}
