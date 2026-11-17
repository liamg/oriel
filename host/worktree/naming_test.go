package worktree

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// A session's branch is its ticket's id as the tracker writes it, and nothing else; with no ticket, <you>/<session id>.
func TestBranchName(t *testing.T) {
	for _, c := range []struct {
		ticket string
		want   string
	}{
		{"TRK-1234", "TRK-1234"},
		{" ENG-2231 ", "ENG-2231"},
		{"acme/web#57", "acme-web57"},
		{"PROJ.12", "PROJ-12"},
		{"", "liam/k7m2q9xa"},
		{"#!", "liam/k7m2q9xa"},
	} {
		if got := BranchName("liam", c.ticket, core.SessionID("k7m2q9xa")); got != c.want {
			t.Errorf("BranchName(%q) = %q, want %q", c.ticket, got, c.want)
		}
	}
}

// A new session's id is 8 characters, lower-case letters and digits with none that read alike.
func TestNewSessionID(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		id := hostcore.NewSessionID()
		if len(id) != 8 || strings.Trim(id, "0123456789abcdefghjkmnpqrstvwxyz") != "" {
			t.Fatalf("id %q", id)
		}
		seen[id] = true
	}
	if len(seen) < 199 {
		t.Errorf("only %d different ids in 200", len(seen))
	}
}

// A session's name, until it's renamed: what the spec names it, else its ticket and the prompt's first words.
func TestSessionName(t *testing.T) {
	for _, c := range []struct {
		spec core.SessionSpec
		want string
	}{
		{core.SessionSpec{Name: "My Work"}, "my-work"},
		{core.SessionSpec{Prompt: "Fix stale search results now please", Ticket: "ENG-2231"}, "eng-2231-fix-stale-search-results-now"},
		{core.SessionSpec{Kind: core.KindDirect}, "direct"},
	} {
		if got := sessionName(c.spec); got != c.want {
			t.Errorf("sessionName(%+v) = %q, want %q", c.spec, got, c.want)
		}
	}
}
