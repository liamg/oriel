package host

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/core"
)

// crit-reference → Linear: "Start work from a ticket: the ticket, its comments and attachments become the plan
// prompt". ui.md 04: "the ticket, comments and attachments go to the agent as context". The prompt stays the
// session's own; the ticket's text follows it in the first request.
func TestTicketContextGoesWithThePrompt(t *testing.T) {
	r := newRig(t)
	ctx := "## ENG-2231 · Search API returns stale results after a reindex\n\n### Comments\n\n**Ana:** Repro attached."
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Pick up ENG-2231. Confirm the cache first.", Ticket: "ENG-2231",
		Context: ctx, Provider: "fake", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	sent := r.agent().WaitSent(t)
	if !strings.Contains(sent.Text, "Pick up ENG-2231. Confirm the cache first.") || !strings.Contains(sent.Text, "**Ana:** Repro attached.") {
		t.Errorf("the agent was sent %q", sent.Text)
	}
	se := r.session(id)
	if se.Prompt != "Pick up ENG-2231. Confirm the cache first." || se.Ticket != "ENG-2231" {
		t.Errorf("the session's prompt %q, ticket %q", se.Prompt, se.Ticket)
	}
	if len(se.Threads) == 0 || se.Threads[0].Title != "Pick up ENG-2231. Confirm the cache first." {
		t.Errorf("the first request: %+v", se.Threads)
	}

	// With no prompt of its own, the ticket is the request.
	r.start(core.SessionSpec{Kind: core.KindWork, Ticket: "ENG-2240", Context: "## ENG-2240 · Price cache invalidation", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	if sent := r.fake.WaitStarted(t).WaitSent(t); !strings.Contains(sent.Text, "ENG-2240 · Price cache invalidation") {
		t.Errorf("a ticket alone sent %q", sent.Text)
	}
}
