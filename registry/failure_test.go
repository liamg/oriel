package registry_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry"
)

// errors.md: "A hook or view that errors 3 times in a row is turned off with a notice ("`tickets.lua:8` turned off
// after 3 errors", action "Turn back on"). `:Hooks` shows it as off, with its last error."
func TestThreeErrorsInARow(t *testing.T) {
	r := registry.New()
	var fails []registry.Failure
	var faults []registry.Fault
	r.OnFailure(func(f registry.Failure) { fails = append(fails, f) })
	r.OnFault(func(f registry.Fault) { faults = append(faults, f) })
	n := 0
	r.Hook(registry.Hook{Event: "X", Source: "tickets.lua:8", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		n++
		if n == 3 { // a success in between starts the count again
			return registry.Verdict{}, nil
		}
		return registry.Verdict{}, errors.New("bad " + string(rune('0'+n)))
	}})
	fire := func() { r.Fire(context.Background(), "X", "", nil) }
	for range 5 {
		fire()
	}
	if len(faults) != 0 || r.Hooks()[0].Off != nil {
		t.Fatalf("off after errors that weren't in a row: %+v", faults)
	}
	fire()
	if len(faults) != 1 || faults[0].Errors != 3 || faults[0].Source != "tickets.lua:8" || faults[0].Err.Error() != "bad 6" {
		t.Fatalf("faults %+v", faults)
	}
	if len(fails) != 5 || fails[4].InARow != 3 || fails[0].InARow != 1 || fails[2].InARow != 1 || fails[4].Source != "tickets.lua:8" {
		t.Errorf("failures %+v", fails)
	}
	h := r.Hooks()[0]
	if h.Off == nil || h.Off.Error() != "bad 6" {
		t.Errorf(":Hooks shows %+v", h)
	}
	fire()
	if n != 6 {
		t.Errorf("an off hook ran: %d calls", n)
	}
	r.TurnOn(registry.KindHook, h.ID)
	fire()
	if n != 7 || len(faults) != 1 {
		t.Errorf("turned back on: %d calls, faults %+v", n, faults)
	}
}

// Report counts any contribution's errors the same way: a view's, a decoration provider's.
func TestReport(t *testing.T) {
	r := registry.New()
	r.View("status", registry.SourcedView{Source: "init.lua:31", View: registry.ViewFunc(nil)})
	var faults []registry.Fault
	r.OnFault(func(f registry.Fault) { faults = append(faults, f) })
	for range 2 {
		r.Report(registry.KindView, "status", errors.New("x"))
	}
	r.Report(registry.KindView, "status", nil)
	for range 3 {
		r.Report(registry.KindView, "status", errors.New("y"))
	}
	if len(faults) != 1 || faults[0].Source != "init.lua:31" || faults[0].Errors != 3 {
		t.Errorf("faults %+v", faults)
	}
}

// errors.md: "A hook over its time budget is a warning, as mockup 19 shows ("4.1 ms on CommentSend · over the 2 ms
// budget")."
func TestHookBudget(t *testing.T) {
	r := registry.New()
	var fails []registry.Failure
	r.OnFailure(func(f registry.Failure) { fails = append(fails, f) })
	r.Hook(registry.Hook{Event: "CommentSend", Source: "lua/my/tickets.lua:8", Fn: func(registry.Ctx, any) (registry.Verdict, error) {
		time.Sleep(3 * time.Millisecond)
		return registry.Verdict{}, nil
	}})
	r.Fire(context.Background(), "CommentSend", "", nil)
	if len(fails) != 1 || !fails[0].Warn || fails[0].Kind != registry.KindHook || fails[0].InARow != 0 {
		t.Fatalf("failures %+v", fails)
	}
	if msg := fails[0].Err.Error(); !strings.HasSuffix(msg, "ms on CommentSend · over the 2ms budget") {
		t.Errorf("warning %q", msg)
	}
}

// Diagnostics are published by owner and file, each set replacing that owner's last for the file.
func TestDiagnostics(t *testing.T) {
	r := registry.New()
	_, gen0 := r.Diagnostics()
	r.SetDiagnostics("lua", "/c/init.lua", []registry.Diagnostic{{Line: 14, Severity: registry.SevError, Message: "attempt to index a nil value"}})
	r.SetDiagnostics("gopls", "/p/a.go", []registry.Diagnostic{{Line: 2, Severity: registry.SevWarning, Message: "unused"}})
	r.SetDiagnostics("lua", "/c/lua/t.lua", []registry.Diagnostic{{Line: 7, Severity: registry.SevError, Message: "x"}})
	ds, gen := r.Diagnostics()
	if gen == gen0 || len(ds) != 3 || ds[0].File != "/c/init.lua" || ds[0].Owner != "lua" || ds[2].File != "/p/a.go" {
		t.Fatalf("diagnostics %+v", ds)
	}
	r.SetDiagnostics("lua", "/c/init.lua", nil)
	if ds, _ := r.Diagnostics(); len(ds) != 2 {
		t.Errorf("after clearing init.lua: %+v", ds)
	}
	if got := r.DiagnosticsFor("/p/a.go"); len(got) != 1 || got[0].Message != "unused" {
		t.Errorf("for a.go: %+v", got)
	}
}

// Coverage is published by owner and file; a line two owners measured keeps the larger count.
func TestCoverage(t *testing.T) {
	r := registry.New()
	r.SetCoverage("tests", "/p/a.go", map[int]int{3: 2, 4: 0})
	r.SetCoverage("jest", "/p/a.go", map[int]int{4: 1, 9: 0})
	got := r.CoverageFor("/p/a.go")
	if len(got) != 3 || got[3] != 2 || got[4] != 1 || got[9] != 0 {
		t.Fatalf("coverage %v", got)
	}
	r.SetCoverage("jest", "/p/a.go", nil)
	if got := r.CoverageFor("/p/a.go"); len(got) != 2 || got[4] != 0 {
		t.Errorf("after clearing jest's: %v", got)
	}
	r.ClearCoverage("tests")
	if got := r.CoverageFor("/p/a.go"); len(got) != 0 {
		t.Errorf("after clearing tests': %v", got)
	}
}
