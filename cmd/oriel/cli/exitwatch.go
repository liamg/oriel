package cli

import (
	"fmt"
	"io"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
)

// exitAfter is how long the app has to end once its window has closed: the session host waits 5s at most for what it
// has running, and the plugins stop well within the rest.
const exitAfter = 15 * time.Second

// exitWatch ends the process if the app hasn't ended within after of its window closing, so a quit never leaves it
// running with no window. It logs every goroutine's stack first: what was stuck, to fix.
type exitWatch struct {
	clock  clock.Clock
	after  time.Duration
	log    *slog.Logger
	stderr io.Writer
	exit   func(code int)

	once sync.Once
}

// start is the window closed: the app has after to end.
func (e *exitWatch) start() {
	e.once.Do(func() { e.clock.AfterFunc(e.after, e.fire) })
}

func (e *exitWatch) fire() {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	e.log.Error("the app hadn't ended after its window closed; exiting", "after", e.after, "goroutines", string(buf))
	fmt.Fprintf(e.stderr, "oriel: still ending %v after the window closed, so exiting; what was stuck is in the log\n", e.after)
	e.exit(1)
}
