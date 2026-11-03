// Package testhook is how a test changes a package's variable: for the test only, put back when it ends.
package testhook

import "testing"

// Set sets *p to v until t ends.
func Set[T any](t testing.TB, p *T, v T) {
	t.Helper()
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}
