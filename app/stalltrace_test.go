package app_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/render"
)

// With --trace, a stall writes the execution trace of the seconds before it, for go tool trace.
func TestSlowFrameWritesTrace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stall.trace")
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), WorkDir: t.TempDir(), Config: func(c *apptest.Config) { c.Trace = path }})
	r.Win.Send(render.SlowFrameEvent{Waited: 900 * time.Millisecond})
	var b []byte
	apptest.Poll(t, "a trace written for the slow frame", func() bool {
		var err error
		b, err = os.ReadFile(path)
		return err == nil && len(b) > 1<<10
	})
	if !bytes.HasPrefix(b, []byte("go 1.")) {
		t.Fatalf("not a trace: %q", b[:16])
	}
}
