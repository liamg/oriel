package shell

import (
	"bytes"
	"fmt"
	"log/slog"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// watchdog logs the loop goroutine's stack when one turn holds it up past slowLoop, while it's still held: the
// stack names the culprit. A nil one does nothing.
type watchdog struct {
	log   *slog.Logger
	limit time.Duration
	gid   []byte // "goroutine N " of the loop

	depth int // turns nest: a command run from a key; only the outer one is timed

	mu    sync.Mutex
	timer *time.Timer
	turn  uint64
	began time.Time     // when the turn began
	cpu0  time.Duration // the process's CPU time then
	cpu   func() time.Duration
}

// startWatchdog is a watchdog on the calling goroutine, the loop's.
func startWatchdog(log *slog.Logger, limit time.Duration) *watchdog {
	w := &watchdog{log: log, limit: limit, gid: goroutineHeader(), cpu: cpuTime}
	w.timer = time.AfterFunc(time.Hour, func() {})
	w.timer.Stop()
	return w
}

// enter starts a turn.
func (w *watchdog) enter() {
	if w == nil {
		return
	}
	if w.depth++; w.depth > 1 {
		return
	}
	w.mu.Lock()
	w.turn++
	turn := w.turn
	w.began, w.cpu0 = time.Now(), w.cpu()
	w.timer.Stop()
	w.timer = time.AfterFunc(w.limit, func() { w.held(turn) })
	w.mu.Unlock()
}

// leave ends a turn.
func (w *watchdog) leave() {
	if w == nil {
		return
	}
	if w.depth--; w.depth > 0 {
		return
	}
	w.mu.Lock()
	w.turn++
	w.timer.Stop()
	w.mu.Unlock()
}

func (w *watchdog) stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.turn++
	w.timer.Stop()
	w.mu.Unlock()
}

// held logs the loop's stack if turn is still running.
func (w *watchdog) held(turn uint64) {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	w.mu.Lock()
	still := w.turn == turn
	began, cpu0 := w.began, w.cpu0
	w.mu.Unlock()
	if !still {
		return
	}
	stack := loopStack(buf, w.gid)
	if share, busy := w.starved(stack, began, cpu0); busy {
		// The loop was waiting for a core, and Oriel was using little of one: the machine is busy, not Oriel.
		w.log.Debug("loop held up by a busy machine", "over", w.limit, "oriel_cpu", fmt.Sprintf("%.0f%%", share*100))
		return
	}
	w.log.Warn("loop held up", "over", w.limit, "stack", stack)
}

// starvedShare is the most of one core Oriel may have used through a hold-up for it to be the machine's doing.
const starvedShare = 0.5

// starved says the loop was held up by the machine rather than its own work: it was runnable, waiting to be run,
// while the process used under starvedShare of a core since the turn began. share is what it used.
func (w *watchdog) starved(stack string, began time.Time, cpu0 time.Duration) (share float64, busy bool) {
	wall := time.Since(began)
	if wall <= 0 || w.cpu == nil {
		return 0, false
	}
	share = float64(w.cpu()-cpu0) / float64(wall)
	head, _, _ := strings.Cut(stack, "\n")
	return share, strings.Contains(head, "[runnable]") && share < starvedShare
}

// loopStack is the stack in an all-goroutines dump whose header starts with gid.
func loopStack(all, gid []byte) string {
	for g := range bytes.SplitSeq(all, []byte("\n\n")) {
		if bytes.HasPrefix(g, gid) {
			return string(g)
		}
	}
	return ""
}

// goroutineHeader is "goroutine N " for the calling goroutine.
func goroutineHeader() []byte {
	buf := make([]byte, 64)
	buf = buf[:runtime.Stack(buf, false)]
	if i := bytes.IndexByte(buf[len("goroutine "):], ' '); i >= 0 {
		id := buf[len("goroutine ") : len("goroutine ")+i]
		if _, err := strconv.Atoi(string(id)); err == nil {
			return append([]byte("goroutine "), append(id, ' ')...)
		}
	}
	return nil
}
