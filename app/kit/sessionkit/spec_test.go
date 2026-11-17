package sessionkit

import (
	"reflect"
	"testing"

	"github.com/liamg/oriel/core"
)

// SpecArgs is SpecFrom's inverse: a spec goes out as session.start's args and comes back the same. A sandbox with no
// network set asks.
func TestSpecArgsRoundTrips(t *testing.T) {
	full := core.SessionSpec{Kind: core.KindReview, Name: "n", Prompt: "p", Ticket: "T-1", Context: "c", PR: "https://x/pr/1",
		PRRemote: "origin", PRRef: "refs/pull/1/head", Repos: []core.RepoID{"a", "b"}, Dir: "/d", Branch: "br", Base: "main",
		Provider: "claude", Model: "m", Effort: "high", Template: "go",
		Sandbox: core.SandboxPolicy{Container: true, Network: "open", Gate: "auto", Backend: "docker", CustomImage: true}}
	for _, s := range []core.SessionSpec{full, {Kind: core.KindWork}} {
		if got := SpecFrom(SpecArgs(s)); !reflect.DeepEqual(got, s) {
			t.Errorf("SpecFrom(SpecArgs(%+v)) = %+v", s, got)
		}
	}
	if got := SpecFrom(SpecArgs(core.SessionSpec{Kind: core.KindWork, Sandbox: core.SandboxPolicy{Container: true}})); got.Sandbox.Network != "ask" {
		t.Errorf("a sandbox with no network has %q", got.Sandbox.Network)
	}
}
