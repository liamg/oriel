package ship

import (
	"testing"

	"github.com/liamg/oriel/app/kit/prui"

	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
)

// panelStates are the projects the Pull request panel is held to: every line of each says something.
func panelStates() map[string]shipRead {
	open := &registry.PR{Number: 7, Title: "t", State: "open", Head: "aaaaaaa1"}
	return map[string]shipRead{
		"not raised": {Ship: host.Ship{Base: "main"}},
		"unpushed":   {Ship: host.Ship{Base: "main", Files: []host.ShipFile{{Path: "a"}}, Behind: 2, Unpushed: []host.ShipCommit{{Hash: "b"}}}},
		"open":       {Ship: host.Ship{Base: "main", Pushed: true, Head: "aaaaaaa1"}, PR: open},
		"no checks":  {Ship: host.Ship{Pushed: true}, PR: open},
		"remote":     {Ship: host.Ship{Base: "main", Pushed: true, Head: "bbbbbbb2"}, PR: open},
		"merged":     {Ship: host.Ship{Base: "main", Pushed: true}, PR: &registry.PR{Number: 7, State: "closed", Merged: true}},
	}
}

func TestPanelLinesAlwaysSaySomething(t *testing.T) {
	for name, s := range panelStates() {
		for _, l := range branchLines(s) {
			if l.note == "" || l.label == "" {
				t.Errorf("%s: %s has no word: %+v", name, l.key, l)
			}
		}
		if state, text := prui.ChecksSummary(s.Checks); state == "" || text == "" {
			t.Errorf("%s: the checks summary is empty", name)
		}
	}
}

// Origin has moved on when the PR's head isn't the commit here and nothing here is waiting to be pushed.
func TestRemoteAhead(t *testing.T) {
	pr := &registry.PR{State: "open", Head: "c3f1a9e0b7d24e6f"}
	for _, tc := range []struct {
		name string
		s    shipRead
		want bool
	}{
		{"same commit", shipRead{Ship: host.Ship{Head: "c3f1a9e0b7d24e6f"}, PR: pr}, false},
		{"same commit, short sha", shipRead{Ship: host.Ship{Head: "c3f1a9e"}, PR: pr}, false},
		{"someone pushed", shipRead{Ship: host.Ship{Head: "0d9e8f7a6b5c4d3e"}, PR: pr}, true},
		{"ours to push", shipRead{Ship: host.Ship{Head: "0d9e8f7a6b5c4d3e", Unpushed: []host.ShipCommit{{Hash: "0d9e8f7"}}}, PR: pr}, false},
		{"no PR", shipRead{Ship: host.Ship{Head: "0d9e8f7a6b5c4d3e"}}, false},
		{"host gives no head", shipRead{Ship: host.Ship{Head: "0d9e8f7a6b5c4d3e"}, PR: &registry.PR{State: "open"}}, false},
	} {
		if got := remoteAhead(tc.s); got != tc.want {
			t.Errorf("%s: remoteAhead = %v, want %v", tc.name, got, tc.want)
		}
	}
	lines := branchLines(shipRead{Ship: host.Ship{Pushed: true, Head: "0d9e8f7a6b5c4d3e"}, PR: pr})
	if l := lines[len(lines)-1]; l.key != "remote" || l.note != "new commits" || l.state != "warn" {
		t.Errorf("the Remote line: %+v", l)
	}
}

func TestReviewStatesKeepAVerdictOverAComment(t *testing.T) {
	got := reviewStates([]registry.ReviewThread{
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
