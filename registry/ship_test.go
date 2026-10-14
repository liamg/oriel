package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// FG-6: "before this ships" is a contribution point: gates by order, a kind naming its own gates, and one that
// panics or has nothing to say left out.
func TestShipGates(t *testing.T) {
	r := registry.New()
	r.Contribute(registry.ShipGate{Name: "tests", Title: "Tests ran this session", Order: 30, Check: func(s string) registry.Gate {
		return registry.Gate{State: registry.GatePass, Note: "go test · " + s}
	}})
	r.Contribute(registry.ShipGate{Name: "plan", Title: "Plan approved", Order: 10, Check: func(string) registry.Gate {
		return registry.Gate{State: registry.GatePass}
	}})
	r.Contribute(registry.ShipGate{Name: "quiet", Order: 20, Check: func(string) registry.Gate { return registry.Gate{} }})
	r.Contribute(registry.ShipGate{Name: "boom", Order: 40, Check: func(string) registry.Gate { panic("boom") }})
	got := registry.ShipGates(r, "s1", "work")
	if len(got) != 2 || got[0].Name != "plan" || got[1].Name != "tests" || got[1].Note != "go test · s1" {
		t.Fatalf("gates %+v", got)
	}
	r.Contribute(registry.SessionKind{Name: "hotfix", Gates: []string{"tests"}})
	if got := registry.ShipGates(r, "s1", "hotfix"); len(got) != 1 || got[0].Name != "tests" {
		t.Errorf("a kind's own gates: %+v", got)
	}
}
