package kit

import "testing"

// A PR's status is the one thing that matters most about it: merged or closed over anything a reviewer said, then
// changes requested over an approval, then an approval, then draft, then open. Review states come in either form
// (GitHub's "APPROVED", a code host's "approved").
func TestPRStatusOf(t *testing.T) {
	for _, tt := range []struct {
		state         string
		merged, draft bool
		reviews       []string
		want          string
	}{
		{"open", false, false, nil, "Open"},
		{"open", false, true, nil, "Draft"},
		{"open", false, false, []string{"APPROVED"}, "Approved"},
		{"open", false, false, []string{"approved", "commented"}, "Approved"},
		{"open", false, false, []string{"APPROVED", "CHANGES_REQUESTED"}, "Changes requested"},
		{"open", false, true, []string{"approved"}, "Approved"},
		{"closed", true, false, []string{"CHANGES_REQUESTED"}, "Merged"},
		{"merged", false, false, nil, "Merged"},
		{"closed", false, false, []string{"approved"}, "Closed"},
		{"", false, false, nil, "Open"},
	} {
		if got := PRStatusOf(tt.state, tt.merged, tt.draft, tt.reviews); got.Word != tt.want {
			t.Errorf("%s merged=%v draft=%v %v: %q, want %q", tt.state, tt.merged, tt.draft, tt.reviews, got.Word, tt.want)
		}
	}
}
