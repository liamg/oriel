// Package isolate keeps a test from reaching the tests after it: every harness that starts an app, window or host
// calls Test first.
package isolate

import (
	"sync"
	"testing"

	"github.com/liamg/oriel/internal/leaktest"
)

var started sync.Map // test name → true, while that test runs

// Test gives t a temporary directory of its own (TMPDIR), puts the XDG directories back under HOME (which a harness
// sets to one of the test's), and fails it if it leaves goroutines running once its
// cleanups are done (leaktest.Check). A second call in the same test does nothing.
func Test(t testing.TB) {
	t.Helper()
	if _, dup := started.LoadOrStore(t.Name(), true); dup {
		return
	}
	t.Cleanup(func() { started.Delete(t.Name()) })
	leaktest.Check(t)
	t.Setenv("TMPDIR", t.TempDir())
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(v, "")
	}
}
