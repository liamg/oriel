package registry_test

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// An explainer's byline says who wrote a long answer: its own, else "written by" its label, else its name.
func TestExplainerWrittenBy(t *testing.T) {
	for _, tc := range []struct {
		e    registry.Explainer
		want string
	}{
		{registry.Explainer{Name: "agent", Label: func() string { return "fork of claude" },
			Byline: func() string { return "written by claude from the session's own context" }}, "written by claude from the session's own context"},
		{registry.Explainer{Name: "local", Label: func() string { return "a local model" }}, "written by a local model"},
		{registry.Explainer{Name: "local"}, "written by local"},
	} {
		if got := tc.e.WrittenBy(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.e.Name, got, tc.want)
		}
	}
}
