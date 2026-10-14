package testrun

import (
	"bytes"
	"fmt"
	"slices"
	"testing"
	"time"
)

// runOf folds a recording's events into a run.
func runOf(t *testing.T, recording string, id string, by By) *Run {
	t.Helper()
	r := &Run{ID: id, By: by, Start: time.Unix(0, 0)}
	for _, e := range replay(t, recordingNamed(t, recording)) {
		r.Apply(e)
	}
	return r
}

func fakeRun(id, change string, by By, results map[string]Status) *Run {
	r := &Run{ID: id, Change: change, By: by, Start: time.Unix(int64(len(id)), 0)}
	keys := make([]string, 0, len(results))
	for k := range results {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		r.Apply(Event{Kind: Started, Package: "p", Test: k})
		switch results[k] {
		case StatusPassed:
			r.Apply(Event{Kind: Passed, Package: "p", Test: k})
		case StatusFailed:
			r.Apply(Event{Kind: Failed, Package: "p", Test: k, Failure: &Failure{Reason: ReasonAssert}})
		}
	}
	return r
}

func TestRunFiltersFromARecordedRun(t *testing.T) {
	run := runOf(t, "pytest-basic", "r1", ByUser)
	var failed []string
	for _, x := range run.Failed() {
		failed = append(failed, x.Test)
	}
	want := []string{"test_fail", "test_raises", "test_setup_error", "test_add_table[2-2-5]", "test_words[two words]"}
	if !slices.Equal(failed, want) {
		t.Errorf("failed = %q, want %q", failed, want)
	}
	slow := run.Slowest(3)
	if len(slow) != 3 || slow[0].Duration < slow[1].Duration || slow[1].Duration < slow[2].Duration {
		t.Fatalf("slowest = %+v", slow)
	}
	for _, x := range run.Tests {
		if x.Status != StatusRunning && x.Duration > slow[0].Duration {
			t.Errorf("%s took %s, longer than the slowest %s", x.Test, x.Duration, slow[0].Duration)
		}
	}
	p, f, s := run.Counts()
	if p == 0 || f != len(want) || s != 3 {
		t.Errorf("counts = %d passed %d failed %d skipped", p, f, s)
	}
	if x, ok := run.Result(Key("tests/test_calc.py", "test_fail")); !ok || len(x.Output) != 1 || x.Output[0] != "about to add" {
		t.Errorf("test_fail's output = %+v", x)
	}
}

// TestFlaky follows ide.md: flaky detection is "fails sometimes, passes on re-run".
func TestFlaky(t *testing.T) {
	h := NewHistory(10)
	h.Add(fakeRun("1", "c1", ByUser, map[string]Status{"rerun": StatusFailed, "fixed": StatusFailed, "mixed": StatusPassed, "broke": StatusPassed}))
	h.Add(fakeRun("2", "c1", ByUser, map[string]Status{"rerun": StatusPassed, "mixed": StatusFailed, "broke": StatusFailed}))
	h.Add(fakeRun("3", "c2", ByAgent, map[string]Status{"fixed": StatusPassed, "mixed": StatusPassed, "broke": StatusFailed}))
	for k, want := range map[string]bool{"rerun": true, "fixed": false, "mixed": true, "broke": false} {
		if got := h.Flaky(Key("p", k)); got != want {
			t.Errorf("Flaky(%s) = %v, want %v: %+v", k, got, want, h.Entries(Key("p", k)))
		}
	}
	if got := h.FlakyTests(); !slices.Equal(got, []string{Key("p", "mixed"), Key("p", "rerun")}) {
		t.Errorf("flaky tests = %q", got)
	}
	if got := h.Failing(); !slices.Equal(got, []string{Key("p", "broke")}) {
		t.Errorf("failing = %q", got)
	}
	var spark []Status
	for _, e := range h.Entries(Key("p", "mixed")) {
		spark = append(spark, e.Status)
	}
	if !slices.Equal(spark, []Status{StatusPassed, StatusFailed, StatusPassed}) {
		t.Errorf("sparkline = %v", spark)
	}
	if r, ok := h.Last(ByAgent); !ok || r.ID != "3" {
		t.Errorf("the agent's last run = %+v", r)
	}
	if r, ok := h.Run("2"); !ok || r.Change != "c1" {
		t.Errorf("run 2 = %+v", r)
	}
}

func TestHistoryKeepsTheLastN(t *testing.T) {
	h := NewHistory(3)
	for i := range 5 {
		h.Add(fakeRun(fmt.Sprint(i), "", ByWatch, map[string]Status{"a": StatusPassed}))
	}
	if n := len(h.Runs()); n != 3 {
		t.Errorf("%d runs kept, want 3", n)
	}
	es := h.Entries(Key("p", "a"))
	if len(es) != 3 || es[0].Run != "2" || es[2].Run != "4" {
		t.Errorf("entries = %+v", es)
	}
}

func TestHistorySaveLoad(t *testing.T) {
	h := NewHistory(5)
	h.Add(runOf(t, "go-basic", "go1", ByUser))
	h.Add(fakeRun("x", "c1", ByUser, map[string]Status{"rerun": StatusFailed}))
	h.Add(fakeRun("y", "c1", ByUser, map[string]Status{"rerun": StatusPassed}))
	var b bytes.Buffer
	if err := h.Save(&b); err != nil {
		t.Fatal(err)
	}
	g, err := LoadHistory(&b)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Flaky(Key("p", "rerun")) || len(g.Runs()) != 3 {
		t.Errorf("loaded: flaky %v, %d runs", g.Flaky(Key("p", "rerun")), len(g.Runs()))
	}
	r, ok := g.Run("go1")
	if !ok {
		t.Fatal("no run go1")
	}
	x, ok := r.Result(Key("example.com/basic/calc", "TestGotWant"))
	if !ok || x.Failure == nil || x.Failure.Want != "5" || x.Failure.Loc.Line != 15 {
		t.Errorf("loaded result = %+v", x)
	}
	if got, want := g.Failing(), h.Failing(); !slices.Equal(got, want) {
		t.Errorf("failing after load = %q, want %q", got, want)
	}
	if got := g.Slowest(1); len(got) != 1 || got[0] != Key("example.com/basic/slow", "TestSlow") && got[0] != h.Slowest(1)[0] {
		t.Errorf("slowest after load = %q", got)
	}
}
