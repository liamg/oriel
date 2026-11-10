package claude

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude/transcript"
	"github.com/liamg/oriel/fswatch"
	"github.com/liamg/oriel/internal/clock/clocktest"
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
	tl, p, got := newTailing(t, watcher)
	tl.start(t, got)
	return tl, p, got
}

// scriptedTailing follows a fresh session's log with a watcher the test sends changes on, and a clock it moves.
func scriptedTailing(t *testing.T) (*tailer, string, <-chan agent.Event, *fswatch.Script, *clocktest.Fake) {
	t.Helper()
	w, script := fswatch.NewScripted()
	tl, p, got := newTailing(t, func() (*fswatch.Watcher, error) { return w, nil })
	clk := clocktest.New(time.Now())
	tl.clock = clk
	tl.start(t, got)
	return tl, p, got, script, clk
}

func newTailing(t *testing.T, watcher func() (*fswatch.Watcher, error)) (*tailer, string, chan agent.Event) {
	t.Helper()
	config := t.TempDir()
	tl := newTailer(config, "/w/api", "s1", false, nil)
	if watcher != nil {
		tl.watcher = watcher
	}
	return tl, transcript.Path(config, "/w/api", "s1"), make(chan agent.Event, 64)
}

func (tl *tailer) start(t *testing.T, got chan agent.Event) {
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		tl.follow(done, func(ev agent.Event) { got <- ev })
	}()
	t.Cleanup(func() {
		close(done)
		<-stopped
	})
}

// said waits for text to be read, for as long as a real watch may take.
func said(t *testing.T, got <-chan agent.Event, text string) {
	t.Helper()
	hung := time.After(20 * time.Second)
	for {
		select {
		case ev := <-got:
			if m, ok := ev.(agent.Message); ok && m.Text == text {
				return
			}
		case <-hung:
			t.Fatalf("%q never read", text)
		}
	}
}

// read is what has been read so far.
func read(got <-chan agent.Event) []string {
	var texts []string
	for {
		select {
		case ev := <-got:
			if m, ok := ev.(agent.Message); ok {
				texts = append(texts, m.Text)
			}
		default:
			return texts
		}
	}
}

func (t *tailer) flushes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.reads
}

// The OS's watch: what's written is read, whether it lands before the watch starts or after.
func TestTailReadsWhatIsWritten(t *testing.T) {
	_, p, got := tailing(t, nil)
	appendLog(t, p, logLine("first"))
	said(t, got, "first")
	appendLog(t, p, logLine("second"))
	said(t, got, "second")
}

func TestTailReadsSubagents(t *testing.T) {
	_, p, got, script, _ := scriptedTailing(t)
	appendLog(t, p, logLine("main"))
	script.Change(p)
	sub := filepath.Join(p[:len(p)-len(".jsonl")], "subagents", "agent-a1.jsonl")
	appendLog(t, sub, logLine("from a subagent"))
	script.Change(filepath.Dir(sub))
	if got := read(got); !slices.Equal(got, []string{"main", "from a subagent"}) {
		t.Errorf("read %q", got)
	}
}

func TestTailIdleReadsOnlyOnSafetyPoll(t *testing.T) {
	tl, p, got, script, clk := scriptedTailing(t)
	appendLog(t, p, logLine("hello"))
	script.Change(p)
	if got := read(got); !slices.Equal(got, []string{"hello"}) {
		t.Fatalf("read %q", got)
	}
	before := tl.flushes()
	clk.Advance(tailSafety - time.Nanosecond)
	script.Change(filepath.Dir(p)) // the loop has done all it had to before this is Done
	if n := tl.flushes() - before; n != 1 {
		t.Fatalf("%d reads with nothing written but one change, before the safety poll", n)
	}
	clk.Advance(time.Nanosecond)
	script.Change(filepath.Dir(p))
	if n := tl.flushes() - before; n != 3 {
		t.Fatalf("%d reads, want the two changes' and the safety poll's", n)
	}
}

func TestTailPollsWithoutAWatch(t *testing.T) {
	tl, p, got := newTailing(t, func() (*fswatch.Watcher, error) { return nil, errors.New("no watches") })
	clk := clocktest.New(time.Now())
	tl.clock = clk
	tl.start(t, got)
	appendLog(t, p, logLine("polled"))
	for clk.Waiting() == 0 {
		runtime.Gosched() // the poll's first timer, set as follow starts
	}
	clk.Advance(tailPoll)
	if got := read(got); !slices.Equal(got, []string{"polled"}) {
		t.Fatalf("read %q", got)
	}
	before := tl.flushes()
	clk.Advance(3 * tailPoll)
	if n := tl.flushes() - before; n != 3 {
		t.Fatalf("%d reads over three polls without a watch, want it polling every %v", n, tailPoll)
	}
}

func TestTailFollowsANewSessionID(t *testing.T) {
	tl, p, got, script, _ := scriptedTailing(t)
	appendLog(t, p, logLine("old"))
	script.Change(p)
	tl.setID("s2")
	next := filepath.Join(filepath.Dir(p), "s2.jsonl")
	appendLog(t, next, logLine("new"))
	script.Change(next)
	if got := read(got); !slices.Equal(got, []string{"old", "new"}) {
		t.Errorf("read %q", got)
	}
	if logs := tl.logs(); logs[0] != next {
		t.Errorf("watching %q", logs)
	}
}
