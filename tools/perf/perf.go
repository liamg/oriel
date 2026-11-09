// Package perf is for the tests that hold code to a time budget.
package perf

import (
	"os"
	"testing"
)

// Timed marks t as a timing budget, named …Budget. make test runs those in a pass of their own, one package at a
// time, so other packages' tests running alongside can't fail them; in its parallel pass (ORIEL_BUDGETS=later) they
// skip. A budget timed by the wall clock is built only with the perf tag, and make perf runs it.
func Timed(t testing.TB) {
	t.Helper()
	if os.Getenv("ORIEL_BUDGETS") == "later" {
		t.Skip("a timing budget: make test runs these on their own")
	}
}
