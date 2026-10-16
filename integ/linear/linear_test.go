package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/liamg/oriel/integ/linear/lineartest"
)

// recorded is a client of the recorded API, with the key it accepts.
func recorded(t *testing.T) (Client, *lineartest.Linear) {
	t.Helper()
	l := lineartest.New(t)
	hc := l.Client()
	return Client{Post: func(ctx context.Context, body []byte) (int, []byte, error) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, Endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", lineartest.Key)
		resp, err := hc.Do(req)
		if err != nil {
			return 0, nil, err
		}
		defer resp.Body.Close()
		b, err := io.ReadAll(resp.Body)
		return resp.StatusCode, b, err
	}}, l
}

// crit's ordering (internal/linear: sortIssues), as its tests have it.
func TestSortIssues(t *testing.T) {
	issues := []Issue{
		{Key: "FIX-10", Priority: 3},
		{Key: "ENG-2", Priority: 0},
		{Key: "FIX-9", Priority: 3, PR: &PR{Number: 1}},
		{Key: "FIX-1", Priority: 1},
		{Key: "ENG-7", Priority: 2, Started: true},
		{Key: "FIX-4", Priority: 4, Started: true},
		{Key: "ENG-3", Priority: 2},
		{Key: "ENG-5", Priority: 2, Started: true, Blocked: true},
	}
	sortIssues(issues)
	var got []string
	for _, is := range issues {
		got = append(got, is.Key)
	}
	want := []string{"FIX-1", "ENG-7", "ENG-3", "ENG-5", "FIX-9", "FIX-10", "FIX-4", "ENG-2"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}
}

func TestHiddenState(t *testing.T) {
	for _, c := range []struct {
		name, typ string
		want      bool
	}{
		{"Done", "completed", true}, {"Cancelled", "canceled", true}, {"Duplicate", "unstarted", true},
		{" Duplicate ", "backlog", true}, {"Todo", "unstarted", false}, {"Duplicated work", "started", false},
	} {
		if got := hiddenState(c.name, c.typ); got != c.want {
			t.Errorf("hiddenState(%q, %q) = %v", c.name, c.typ, got)
		}
	}
}

func TestPRFromURL(t *testing.T) {
	for url, want := range map[string]int{
		"https://github.com/acme/web/pull/42": 42, "https://github.com/acme/web/pull/42/files": 42, " https://github.com/acme/web/pull/7 ": 7,
		"https://github.com/acme/web/issues/42": 0, "https://gitlab.com/acme/web/pull/42": 0, "https://github.com/acme/web/pull/0": 0, "": 0,
	} {
		got := prFromURL(url)
		switch {
		case want == 0 && got != nil:
			t.Errorf("%q = %+v, want none", url, got)
		case want != 0 && (got == nil || got.Number != want || got.URL != "https://github.com/acme/web/pull/"+strconv.Itoa(want)):
			t.Errorf("%q = %+v, want #%d", url, got, want)
		}
	}
}

func TestLessTicketKey(t *testing.T) {
	if !lessTicketKey("FIX-9", "FIX-10") || lessTicketKey("FIX-10", "FIX-9") || !lessTicketKey("ENG-1", "FIX-1") || lessTicketKey("NOPE", "FIX-1") {
		t.Error("ticket keys out of order")
	}
}

func TestStates(t *testing.T) {
	states := []State{{ID: "todo", Name: "Todo", Type: "unstarted", Position: 1}, {ID: "review", Name: "In Review", Type: "started", Position: 3},
		{ID: "prog", Name: "In Progress", Type: "started", Position: 2}}
	if startedState(states) != "prog" || startedState(states[:2]) != "review" || startedState(states[:1]) != "" {
		t.Error("the started state")
	}
	if st := reviewState(states); st == nil || st.ID != "review" {
		t.Errorf("the review state %+v", st)
	}
	if st := reviewState([]State{{ID: "prog", Name: "In Progress", Type: "started"}}); st != nil {
		t.Errorf("a team with no In Review: %+v", st)
	}
	done := []State{{ID: "shipped", Name: "Shipped", Type: "completed", Position: 5}, {ID: "released", Name: "Released", Type: "completed", Position: 4}}
	if st := doneState(done); st == nil || st.ID != "released" {
		t.Errorf("a team with no Done: %+v", st)
	}
	if st := doneState(append(done, State{ID: "done", Name: "Done", Type: "completed", Position: 6})); st == nil || st.ID != "done" {
		t.Errorf("the done state: %+v", st)
	}
	if st := doneState(states); st != nil {
		t.Errorf("a team with no completed state: %+v", st)
	}
}

// crit-reference → Linear: "A list of assigned tickets", in crit's order, from the recorded API: the duplicate left
// out, the newest PR linked, only open blockers counted.
func TestAssignedTickets(t *testing.T) {
	c, _ := recorded(t)
	issues, err := c.AssignedIssues(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, is := range issues {
		keys = append(keys, is.Key)
	}
	if want := "ENG-2231 ENG-2240 ENG-2219 ENG-2252"; strings.Join(keys, " ") != want {
		t.Fatalf("tickets %v, want %s", keys, want)
	}
	if is := issues[0]; is.PR == nil || is.PR.Number != 4398 || is.PR.Repo != "acme/cloud" || is.State != "Todo" || is.PriorityLabel != "High" ||
		is.Project != "Usage" {
		t.Errorf("ENG-2231: %+v", is)
	}
	if is := issues[1]; !is.Blocked || strings.Join(is.BlockedBy, ",") != "ENG-2231" {
		t.Errorf("ENG-2240 isn't blocked by ENG-2231: %+v", is)
	}
	if is := issues[2]; !is.Started || is.Blocked || is.PR == nil || is.PR.Number != 530 {
		t.Errorf("ENG-2219: %+v", is)
	}
}

// "Start work from a ticket: the ticket, its comments and attachments become the plan prompt."
func TestTicketForTheAgent(t *testing.T) {
	c, _ := recorded(t)
	tk, err := c.Ticket(t.Context(), "ENG-2231")
	if err != nil {
		t.Fatal(err)
	}
	if !tk.Mine || tk.Assignee != "Liam Galvin" || len(tk.Comments) != 4 || len(tk.Attachments) != 2 || tk.PR == nil || tk.PR.Number != 4398 {
		t.Fatalf("ticket %+v", tk)
	}
	if want := "Customers on the new Team plan see last month's unit prices in /v1/usage for up to 24h. Reported by 3 accounts; repro steps and a HAR attached."; tk.Summary() != want {
		t.Errorf("summary %q", tk.Summary())
	}
	if got := strings.Join(tk.Facts(), " · "); got != "linear.app/acme/issue/ENG-2231 · 2 attachments · linked PR #4398 · 4 comments" {
		t.Errorf("facts %q", got)
	}
	ctx := tk.Context()
	for _, want := range []string{
		"Linear ticket ENG-2231: Usage API returns stale prices after plan change",
		"State: Todo · Priority: High · Project: Usage",
		"3. Unit prices are Pro's until the nightly job runs",
		"## Comments (4)",
		"**priya** · 2026-10-14 11:20:\n\n> Probably the price table cache in usage-api. It's keyed by org, not org + plan.",
		"## Attachments (2)",
		"- usage-stale-prices.har (412 KB): https://uploads.linear.app/1a2b3c/usage-stale-prices.har",
		"- #4398 fix(usage): key price cache by plan (acme/cloud): https://github.com/acme/cloud/pull/4398",
	} {
		if !strings.Contains(ctx, want) {
			t.Errorf("the agent's context has no %q:\n%s", want, ctx)
		}
	}
	if strings.Index(ctx, "**ana** · 2026-10-13") > strings.Index(ctx, "**ben**") {
		t.Error("the comments aren't oldest first")
	}
	if _, err := c.Ticket(t.Context(), "ENG-9999"); err == nil || !strings.Contains(err.Error(), "Entity not found") {
		t.Errorf("a ticket Linear hasn't: %v", err)
	}
}

// "Ticket state moves (to in progress on start, to in review on PR)", and to done on merge: once each, never backwards.
func TestStateMoves(t *testing.T) {
	c, l := recorded(t)
	ctx := t.Context()
	if moved, state, err := c.Start(ctx, "ENG-2240"); err != nil || !moved || state != "In Progress" {
		t.Fatalf("start: %v %q %v", moved, state, err)
	}
	if moved, state, _ := c.Start(ctx, "ENG-2240"); moved || state != "In Progress" {
		t.Errorf("started again: %v %q", moved, state)
	}
	if moved, state, err := c.Review(ctx, "ENG-2240"); err != nil || !moved || state != "In Review" {
		t.Fatalf("review: %v %q %v", moved, state, err)
	}
	if moved, _, _ := c.Review(ctx, "ENG-2240"); moved {
		t.Error("reviewed again")
	}
	if moved, state, _ := c.Start(ctx, "ENG-2240"); moved || state != "In Review" {
		t.Errorf("start moved a ticket in review back: %v %q", moved, state)
	}
	if moved, state, err := c.Done(ctx, "ENG-2240"); err != nil || !moved || state != "Done" {
		t.Fatalf("done: %v %q %v", moved, state, err)
	}
	if moved, state, _ := c.Done(ctx, "ENG-2240"); moved || state != "Done" {
		t.Errorf("done again: %v %q", moved, state)
	}
	if moved, _, _ := c.Review(ctx, "ENG-2240"); moved {
		t.Error("review moved a done ticket back")
	}
	m := l.Mutations()
	if len(m) != 3 || m[2].Vars["input"].(map[string]any)["stateId"] != "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a05" || m[0].Op != "issueUpdate" || m[0].Vars["id"] != "b81c40d7-9a2e-4c55-9e0b-7d1f3c2a2240" ||
		m[0].Vars["input"].(map[string]any)["stateId"] != "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a03" ||
		m[1].Vars["input"].(map[string]any)["stateId"] != "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a04" {
		t.Errorf("mutations %+v", m)
	}
}

// "…and links back to the PR."
func TestLinkPR(t *testing.T) {
	c, l := recorded(t)
	if err := c.LinkPR(t.Context(), "ENG-2231", "https://github.com/acme/cloud/pull/4398", 4398, "fix(usage): key price cache by plan"); err != nil {
		t.Fatal(err)
	}
	m := l.Mutations()
	want := map[string]any{"issueId": "a3f9e2c1-0d44-4b7e-8f61-2c5d9b7e2231", "url": "https://github.com/acme/cloud/pull/4398",
		"title": "#4398 fix(usage): key price cache by plan"}
	if len(m) != 1 || m[0].Op != "attachmentCreate" || !reflect.DeepEqual(m[0].Vars["input"], want) {
		t.Errorf("mutations %+v", m)
	}
}

func TestErrorsComeBackAsErrors(t *testing.T) {
	c := Client{Post: func(context.Context, []byte) (int, []byte, error) {
		return 400, []byte(`{"errors":[{"message":"Authentication required, not authenticated"}]}`), nil
	}}
	if _, err := c.AssignedIssues(t.Context()); err == nil || err.Error() != "linear: Authentication required, not authenticated" {
		t.Errorf("err = %v", err)
	}
	if _, err := json.Marshal(Params{}); err != nil {
		t.Fatal(err)
	}
}
