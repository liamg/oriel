package mirror

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// A reviewer's approval or changes stands over a later comment of theirs; a thread with no reviewer counts for none.
func TestReviewStatesKeepAVerdictOverAComment(t *testing.T) {
	got := verdicts([]registry.ReviewThread{
		{Reviewer: "ana", State: registry.ReviewApproved}, {Reviewer: "ana", State: registry.ReviewCommented},
		{Reviewer: "bo", State: registry.ReviewCommented}, {Reviewer: "bo", State: registry.ReviewChanges},
		{Reviewer: "cy", State: registry.ReviewCommented}, {Reviewer: "", State: registry.ReviewApproved},
	})
	want := map[string]string{"ana": registry.ReviewApproved, "bo": registry.ReviewChanges, "cy": registry.ReviewCommented}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %q, want %q", k, got[k], v)
		}
	}
}
