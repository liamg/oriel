package ship

import "testing"

// The block earlier versions wrote into each of a session's PR descriptions is taken out when Oriel next writes one,
// with the blank lines it added; a description without one is left as it is.
func TestWithoutLinkedPRs(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"## What\nRetry-After on 429s.\n\n<!-- oriel:linked-prs -->\n**Ships with** (merge in this order):\n1. acme/search-api#12 (this PR)\n2. acme/web#7\n<!-- /oriel:linked-prs -->",
			"## What\nRetry-After on 429s."},
		{"<!-- oriel:linked-prs -->\n**Ships with**:\n1. a#1\n<!-- /oriel:linked-prs -->", ""},
		{"Before.\n\n<!-- oriel:linked-prs -->\n1. a#1\n<!-- /oriel:linked-prs -->\n\nAfter.", "Before.\n\nAfter."},
		{"No block here.\n", "No block here.\n"},
		{"An unclosed <!-- oriel:linked-prs --> marker stays.", "An unclosed <!-- oriel:linked-prs --> marker stays."},
	} {
		if got := withoutLinkedPRs(c.in); got != c.want {
			t.Errorf("withoutLinkedPRs(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
