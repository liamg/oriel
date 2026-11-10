package testrun

import (
	"context"
	"github.com/liamg/oriel/internal/testhook"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func goBasic(t *testing.T) (Workspace, string) {
	root := fixtureRoot(t, "go/basic")
	return Workspace{Root: root, Env: []string{"GOWORK=off", "GOFLAGS=-count=1"}}, root
}

func TestExecStreamsEventsIntoARun(t *testing.T) {
	ws, root := goBasic(t)
	file := filepath.Join(root, "calc", "calc_test.go")
	items := []Item{{Package: "example.com/basic/calc", Name: "TestGotWant", File: file, Kind: KindTest}}
	var seen []Event
	x, err := Start(context.Background(), Request{Adapter: &Go{}, Workspace: ws, Items: items, Trigger: "nearest", By: ByUser}, func(e Event) {
		seen = append(seen, e)
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := x.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 || seen[0].Kind != Started {
		t.Fatalf("events = %+v", seen)
	}
	r, ok := run.Result(items[0].Key())
	if !ok || r.Status != StatusFailed || r.Failure == nil {
		t.Fatalf("result = %+v", r)
	}
	if loc := r.Failure.Loc; loc == nil || loc.File != file || loc.Line != 15 {
		t.Errorf("loc = %+v, want %s:16", loc, file)
	}
	if run.Status != StatusFailed || run.ID == "" || run.Trigger != "nearest" || run.By != ByUser || run.End.Before(run.Start) {
		t.Errorf("run = %s %q %q %s", run.Status, run.ID, run.Trigger, run.By)
	}
	if !strings.Contains(run.Raw, `"Action":"fail"`) || len(run.Scope) != 1 {
		t.Errorf("raw output or scope missing: scope %q", run.Scope)
	}
	if p, f, s := run.Counts(); p != 0 || f != 1 || s != 0 {
		t.Errorf("counts = %d %d %d", p, f, s)
	}
}

func slowItem(root string) []Item {
	return []Item{{Package: "example.com/basic/slow", Name: "TestSlow", File: filepath.Join(root, "slow", "slow_test.go"), Kind: KindTest}}
}

func TestExecCancel(t *testing.T) {
	ws, root := goBasic(t)
	started := make(chan struct{})
	var once bool
	x, err := Start(context.Background(), Request{Adapter: &Go{}, Workspace: ws, Items: slowItem(root)}, func(e Event) {
		if e.Kind == Started && e.Test == "TestSlow" && !once {
			once = true
			close(started)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Minute):
		t.Fatal("TestSlow never started")
	}
	begin := time.Now()
	x.Cancel()
	run, _ := x.Wait()
	if run.Status != StatusCanceled {
		t.Errorf("status = %s, want canceled", run.Status)
	}
	if d := time.Since(begin); d > 10*time.Second {
		t.Errorf("cancel took %s", d)
	}
}

func TestExecTimeout(t *testing.T) {
	ws, root := goBasic(t)
	x, err := Start(context.Background(), Request{Adapter: &Go{}, Workspace: ws, Items: slowItem(root), Timeout: 3 * time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	run, _ := x.Wait()
	if run.Status != StatusTimedOut {
		t.Errorf("status = %s, want timed out", run.Status)
	}
	r, ok := run.Result(slowItem(root)[0].Key())
	if !ok || r.Status != StatusTimedOut || r.Failure == nil || r.Failure.Reason != ReasonTimeout {
		t.Errorf("TestSlow = %+v", r)
	}
}

// shell is an adapter that runs a shell line and reads nothing from it.
type shell struct {
	Go
	line string
}

func (s *shell) Command(Workspace, []Item, Mode) (Cmd, error) {
	return Cmd{Args: []string{"sh", "-c", s.line}}, nil
}
func (*shell) Parse(r io.Reader, _ func(Event)) error { _, err := io.Copy(io.Discard, r); return err }

// os/exec: "If WaitDelay is non-zero, ... Wait ... closes the I/O pipes after the command exits" — a child left
// holding the runner's stdout doesn't keep the run going.
func TestExecEndsWhenAChildHoldsStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("a shell line")
	}
	testhook.Set(t, &waitDelay, 200*time.Millisecond)
	x, err := Start(context.Background(), Request{Adapter: &shell{line: "echo hi; sleep 30 &"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-x.Done():
	case <-time.After(20 * time.Second):
		t.Fatal("the run waited on a child holding its stdout")
	}
	if run, _ := x.Wait(); run.Raw != "hi\n" || run.Status == StatusError {
		t.Errorf("raw = %q, status %s", run.Raw, run.Status)
	}
}
