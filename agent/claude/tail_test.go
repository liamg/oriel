package claude

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/fswatch"
)

func logLine(text string) string {
	return `{"type":"assistant","uuid":"u-` + text + `","message":{"role":"assistant","content":[{"type":"text","text":"` + text + `"}]}}` + "\n"
}

func appendLog(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(s); err != nil {
		t.Fatal(err)
	}
}

// tailing follows a fresh session's log; the path is where Claude writes it.
func tailing(t *testing.T, watcher func() (*fswatch.Watcher, error)) (*tailer, string, <-chan agent.Event) {
	t.Helper()
	config := t.TempDir()
	tl := newTailer(config, "/w/api", "s1", false, nil)
	if watcher != nil {
		tl.watcher = watcher
	}
	got := make(chan agent.Event, 64)
	done := make(chan struct{})
	go tl.follow(done, func(ev agent.Event) { got <- ev })
	t.Cleanup(func() { close(done) })
	return tl, transcript.Path(config, "/w/api", "s1"), got
}

func said(t *testing.T, got <-chan agent.Event, text string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev := <-got:
			if m, ok := ev.(agent.Message); ok && m.Text == text {
				return
			}
		case <-deadline:
			t.Fatalf("%q not read within %v", text, within)
		}
	}
}

func (t *tailer) flushes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads
}

func TestTailReadsWhatIsWritten(t *testing.T) {
	_, p, got := tailing(t, nil)
	time.Sleep(50 * time.Millisecond)
	appendLog(t, p, logLine("first"))
	said(t, got, "first", time.Second)
	appendLog(t, p, logLine("second"))
	said(t, got, "second", time.Second)
}

func TestTailReadsSubagents(t *testing.T) {
	_, p, got := tailing(t, nil)
	appendLog(t, p, logLine("main"))
	said(t, got, "main", time.Second)
	sub := filepath.Join(p[:len(p)-len(".jsonl")], "subagents", "agent-a1.jsonl")
	appendLog(t, sub, logLine("from a subagent"))
	said(t, got, "from a subagent", time.Second)
}

func TestTailIdleReadsOnlyOnSafetyPoll(t *testing.T) {
	tl, p, got := tailing(t, nil)
	appendLog(t, p, logLine("hello"))
	said(t, got, "hello", time.Second)
	before := tl.flushes()
	window := 2 * time.Second
	time.Sleep(window)
	// At most the one safety poll that may fall in the window.
	if n := tl.flushes() - before; n > 1 {
		t.Fatalf("%d reads over %v with nothing written, want at most 1", n, window)
	}
}

func TestTailPollsWithoutAWatch(t *testing.T) {
	tl, p, got := tailing(t, func() (*fswatch.Watcher, error) { return nil, errors.New("no watches") })
	appendLog(t, p, logLine("polled"))
	said(t, got, "polled", time.Second)
	before := tl.flushes()
	time.Sleep(time.Second)
	if n := tl.flushes() - before; n < 3 {
		t.Fatalf("%d reads in a second without a watch, want it polling every %v", n, tailPoll)
	}
}

func TestTailFollowsANewSessionID(t *testing.T) {
	tl, p, got := tailing(t, nil)
	appendLog(t, p, logLine("old"))
	said(t, got, "old", time.Second)
	tl.setID("s2")
	next := filepath.Join(filepath.Dir(p), "s2.jsonl")
	time.Sleep(50 * time.Millisecond)
	appendLog(t, next, logLine("new"))
	said(t, got, "new", time.Second)
}
