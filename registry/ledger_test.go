package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// The review ledger is a query any feature asks (the Explorer, the summary, the Ship page): the first
// ReviewLedger by Order answers, a plugin's shadowing the built-in.
func TestLedgerOf(t *testing.T) {
	r := registry.New()
	if _, ok := registry.LedgerOf(r, "/repo"); ok {
		t.Fatal("a ledger with nothing registered")
	}
	l := registry.Ledger{Root: "/repo", Ready: true, Files: []registry.LedgerFile{
		{Path: "round.go", All: registry.LedgerCount{Read: 2, Skimmed: 1, Unread: 3}, Uncommitted: registry.LedgerCount{Unread: 3}},
		{Path: "tiers.go", All: registry.LedgerCount{Read: 4}},
	}}
	r.Contribute(registry.ReviewLedger{Name: "builtin", Order: 10, Get: func(string) registry.Ledger { return l }})
	got, ok := registry.LedgerOf(r, "/repo")
	if !ok || len(got.Files) != 2 {
		t.Fatalf("ledger: %+v %v", got, ok)
	}
	if c := got.Of([]string{"round.go", "tiers.go"}, false); c.Total() != 10 || c.NotRead() != 4 || c.Frac() != .6 {
		t.Errorf("both files: %+v", c)
	}
	if c := got.Of([]string{"round.go"}, true); c.Unread != 3 {
		t.Errorf("round.go uncommitted: %+v", c)
	}
	if (registry.LedgerCount{}).Frac() != 1 {
		t.Error("nothing to read isn't all read")
	}
	r.Contribute(registry.ReviewLedger{Name: "plugin", Order: 1, Get: func(root string) registry.Ledger { return registry.Ledger{Root: "plugin"} }})
	if got, _ := registry.LedgerOf(r, "/repo"); got.Root != "plugin" {
		t.Error("the lower Order doesn't answer")
	}
}

// Every commit path asks the same gates; a gate that panics is left out, the rest answer by Order.
func TestCheckCommit(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.CommitGate{Name: "ledger", Order: 10, Check: func(c registry.CommitCheck) registry.GateResult {
		return registry.GateResult{Mode: registry.GateHard, Done: 1, Total: 3, Unread: 2, Trailer: "Unreviewed-Agent-Lines: 2"}
	}})
	r.Contribute(registry.CommitGate{Name: "tests", Order: 5, Check: func(registry.CommitCheck) registry.GateResult {
		return registry.GateResult{Mode: registry.GateSoft, Pass: true}
	}})
	r.Contribute(registry.CommitGate{Name: "bad", Check: func(registry.CommitCheck) registry.GateResult { panic("bad gate") }})
	got := registry.CheckCommit(r, registry.CommitCheck{Root: "/repo"})
	if len(got) != 2 || got[0].Gate.Name != "tests" || got[1].Gate.Name != "ledger" {
		t.Fatalf("checks: %+v", got)
	}
	if !got[1].Blocks() || got[1].Asks() || got[0].Blocks() {
		t.Errorf("blocks: %+v", got)
	}
}
