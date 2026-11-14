package prui

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

func TestChecksSummary(t *testing.T) {
	ck := func(state string) registry.Check { return registry.Check{Name: state, State: state} }
	for _, tc := range []struct {
		checks      []registry.Check
		state, text string
	}{
		{nil, "todo", "No checks reported"},
		{[]registry.Check{ck(registry.CheckOK), ck(registry.CheckSkipped)}, "ok", "All checks passed (2)"},
		{[]registry.Check{ck(registry.CheckOK), ck(registry.CheckRunning), ck(registry.CheckQueued)}, "doing", "2 running, 1 passed"},
		{[]registry.Check{ck(registry.CheckFailed), ck(registry.CheckRunning), ck(registry.CheckOK)}, "fail", "1 of 3 failing"},
	} {
		if state, text := ChecksSummary(tc.checks); state != tc.state || text != tc.text {
			t.Errorf("%v: got %s %q, want %s %q", tc.checks, state, text, tc.state, tc.text)
		}
	}
}
