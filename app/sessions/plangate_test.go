package sessions_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// Work started with no plan or ticket has nothing to approve: its plan gate is skipped, not a warning.
func TestPlanGateSkippedWhenUnplanned(t *testing.T) {
	s, se := endScene(t)
	for _, g := range registry.ShipGates(s.Registry(), string(se.ID), string(se.Kind)) {
		if g.Name == "plan" {
			if g.State != registry.GateSkipped || g.Note != "unplanned" {
				t.Errorf("the plan gate of an unplanned session: %+v", g.Gate)
			}
			return
		}
	}
	t.Fatal("no plan gate")
}
