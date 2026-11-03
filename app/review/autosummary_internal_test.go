package review

import (
	"testing"

	"github.com/liamg/oriel/core"
)

// The summary starts as crit's: LGTM with nothing kept, else the issues kept by severity, worst first.
func TestAutoSummary(t *testing.T) {
	keep := func(sev string) reviewItem { return reviewItem{sev: sev, pick: core.ReviewPick{State: core.PickKeep}} }
	drop := reviewItem{sev: "high", pick: core.ReviewPick{State: core.PickDrop}}
	for _, tc := range []struct {
		items []reviewItem
		want  string
	}{
		{nil, "LGTM"},
		{[]reviewItem{drop}, "LGTM"},
		{[]reviewItem{keep("high")}, "1 high issue."},
		{[]reviewItem{keep("medium"), keep("high"), keep("medium"), drop}, "1 high, 2 medium issues."},
		{[]reviewItem{keep("nit"), keep("")}, "1 nit, 1 other issues."},
	} {
		got := autoSummary(tc.items)
		if got != tc.want {
			t.Errorf("autoSummary = %q, want %q", got, tc.want)
		}
		if !isAutoSummary(got) {
			t.Errorf("%q isn't known as the automatic summary", got)
		}
	}
	if isAutoSummary("LGTM, but rename it.") {
		t.Error("the user's own summary was taken for the automatic one")
	}
}
