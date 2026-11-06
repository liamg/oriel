package linear

import (
	"reflect"
	"strings"
	"testing"
)

// The agent's linear_update_issue: a state, an assignee and a priority by name, as a person says them, sent as the
// ids and numbers Linear takes.
//
// ref:   linear/schema.graphql
// quote: "The priority of the issue. 0 = No priority, 1 = Urgent, 2 = High, 3 = Medium, 4 = Low."
func TestUpdateTicket(t *testing.T) {
	c, l := recorded(t)
	ctx := t.Context()
	urgent := 1.0
	ed, err := c.Update(ctx, "ENG-2240", Change{Title: "Prime the result cache once a reindex finishes", State: "in progress",
		Assignee: "ana", Priority: &urgent})
	if err != nil {
		t.Fatal(err)
	}
	if ed.Key != "ENG-2240" || !strings.HasPrefix(ed.URL, "https://linear.app/acme/issue/ENG-2240") {
		t.Errorf("edit %+v", ed)
	}
	for _, want := range []string{"state Todo → In Progress", "priority High → Urgent", "assignee Liam Galvin → Ana Ruiz", "title"} {
		if !strings.Contains(ed.Line, want) {
			t.Errorf("the line %q lacks %q", ed.Line, want)
		}
	}
	m := l.Mutations()
	want := map[string]any{"title": "Prime the result cache once a reindex finishes", "stateId": "6c1b3a8e-2d6f-4f0a-9a51-0b3f6f0f1a03",
		"assigneeId": "0f3a9c2e-7b1d-4e8f-9a6c-2d5b8e1f0002", "priority": 1.0}
	if len(m) != 1 || m[0].Op != "issueUpdate" || m[0].Vars["id"] != "b81c40d7-9a2e-4c55-9e0b-7d1f3c2a2240" || !reflect.DeepEqual(m[0].Vars["input"], want) {
		t.Fatalf("mutations %+v", m)
	}
	tk, err := c.Ticket(ctx, "ENG-2240")
	if err != nil {
		t.Fatal(err)
	}
	if tk.State != "In Progress" || tk.Assignee != "Ana Ruiz" || tk.PriorityLabel != "Urgent" || tk.Title != "Prime the result cache once a reindex finishes" {
		t.Errorf("read back: %+v", tk)
	}

	// Back to its owner, and unassigned: "me" is the API key's owner, "none" nobody.
	if _, err := c.Update(ctx, "ENG-2240", Change{Assignee: "me"}); err != nil {
		t.Fatal(err)
	}
	if m := l.Mutations(); m[len(m)-1].Vars["input"].(map[string]any)["assigneeId"] != "0f3a9c2e-7b1d-4e8f-9a6c-2d5b8e1f0001" {
		t.Errorf("me: %+v", m[len(m)-1])
	}
	if ed, err := c.Update(ctx, "ENG-2240", Change{Assignee: "none", Description: "Evict, then warm."}); err != nil || !strings.Contains(ed.Line, "unassigned") || !strings.Contains(ed.Line, "description") {
		t.Fatalf("none: %+v %v", ed, err)
	}
	if in := l.Mutations()[2].Vars["input"].(map[string]any); in["assigneeId"] != nil || in["description"] != "Evict, then warm." {
		t.Errorf("none: %+v", in)
	}

	sent := len(l.Mutations())
	for _, bad := range []struct {
		ch   Change
		want string
	}{
		{Change{State: "Shipped"}, `ENG-2240's team has no state "Shipped": Backlog, Todo, In Progress, In Review, Done, Canceled, Duplicate`},
		{Change{Assignee: "nobody@else.test"}, `no one in Linear is called "nobody@else.test"`},
		{Change{Priority: new(7.0)}, "priority is 0 (none), 1 (urgent), 2 (high), 3 (medium) or 4 (low)"},
		{Change{}, "nothing to change on ENG-2240"},
	} {
		if _, err := c.Update(ctx, "ENG-2240", bad.ch); err == nil || err.Error() != bad.want {
			t.Errorf("%+v: %v, want %q", bad.ch, err, bad.want)
		}
	}
	if n := len(l.Mutations()); n != sent {
		t.Errorf("%d changes sent for refused updates", n-sent)
	}
}

// The agent's linear_comment, on a ticket by its key.
//
// ref:   linear/schema.graphql
// quote: "The issue to associate the comment with. Can be a UUID or issue identifier (e.g., 'LIN-123')."
func TestCommentOnTicket(t *testing.T) {
	c, l := recorded(t)
	ctx := t.Context()
	ed, err := c.Comment(ctx, "ENG-2231", "Cache key now includes the index version; PR to follow.")
	if err != nil {
		t.Fatal(err)
	}
	if ed.Key != "ENG-2231" || !strings.Contains(ed.URL, "ENG-2231") || ed.Line != "commented on ENG-2231" {
		t.Errorf("edit %+v", ed)
	}
	m := l.Mutations()
	want := map[string]any{"issueId": "ENG-2231", "body": "Cache key now includes the index version; PR to follow."}
	if len(m) != 1 || m[0].Op != "commentCreate" || !reflect.DeepEqual(m[0].Vars["input"], want) {
		t.Fatalf("mutations %+v", m)
	}
	tk, _ := c.Ticket(ctx, "ENG-2231")
	if n := len(tk.Comments); n != 5 || tk.Comments[n-1].Body != want["body"] || tk.Comments[n-1].Author != "liam" {
		t.Errorf("comments %+v", tk.Comments)
	}
	if _, err := c.Comment(ctx, "ENG-2231", "  "); err == nil || err.Error() != "a comment needs a body" {
		t.Errorf("an empty comment: %v", err)
	}
	if _, err := c.Comment(ctx, "ENG-9999", "hello"); err == nil || !strings.Contains(err.Error(), "Entity not found") {
		t.Errorf("a ticket Linear hasn't: %v", err)
	}
}

// The agent's linear_create_issue: in a team named by its key (or its name), with its state, assignee and priority
// by name.
//
// ref:   linear/schema.graphql
// quote: "The team's unique key, used as a prefix in issue identifiers (e.g., 'ENG' in 'ENG-123') and in URLs."
func TestCreateTicket(t *testing.T) {
	c, l := recorded(t)
	ctx := t.Context()
	high := 2.0
	ed, err := c.Create(ctx, NewTicket{Team: "ops", Title: "Alert when a reindex runs past an hour", Description: "Seen during ENG-2231.",
		State: "doing", Assignee: "me", Priority: &high})
	if err != nil {
		t.Fatal(err)
	}
	if ed.Key != "OPS-3001" || ed.URL != "https://linear.app/acme/issue/OPS-3001" || ed.Line != "created OPS-3001 in Operations" {
		t.Errorf("edit %+v", ed)
	}
	m := l.Mutations()
	want := map[string]any{"teamId": "e1a7c3d5-5b8f-4c2a-9d01-3f6b2e8a0e02", "title": "Alert when a reindex runs past an hour",
		"description": "Seen during ENG-2231.", "stateId": "7d2c4b9f-3e70-4a1b-8b62-1c4a7a0e2b03",
		"assigneeId": "0f3a9c2e-7b1d-4e8f-9a6c-2d5b8e1f0001", "priority": 2.0}
	if len(m) != 1 || m[0].Op != "issueCreate" || !reflect.DeepEqual(m[0].Vars["input"], want) {
		t.Fatalf("mutations %+v", m)
	}
	tk, err := c.Ticket(ctx, "OPS-3001")
	if err != nil || tk.State != "Doing" || tk.Assignee != "Liam Galvin" || !tk.Mine || tk.PriorityLabel != "High" {
		t.Errorf("read back: %+v, %v", tk, err)
	}
	if ed, err := c.Create(ctx, NewTicket{Team: "Engineering", Title: "Bare"}); err != nil || ed.Key != "ENG-3002" {
		t.Errorf("by the team's name: %+v, %v", ed, err)
	}

	for _, bad := range []struct {
		in   NewTicket
		want string
	}{
		{NewTicket{Title: "Where?"}, "which team? ENG (Engineering), OPS (Operations)"},
		{NewTicket{Team: "SEC", Title: "Where?"}, "no team SEC in Linear: ENG (Engineering), OPS (Operations)"},
		{NewTicket{Team: "ENG"}, "a ticket needs a title"},
		{NewTicket{Team: "OPS", Title: "x", State: "In Review"}, `team OPS has no state "In Review": Triage, Todo, Doing, Done`},
	} {
		if _, err := c.Create(ctx, bad.in); err == nil || err.Error() != bad.want {
			t.Errorf("%+v: %v, want %q", bad.in, err, bad.want)
		}
	}
}

// The agent's linear_projects, and a ticket filed in a project or moved to one by its name, slug or URL: sent as
// the project's id, which Linear takes as the issue's project.
//
// ref:   linear/schema.graphql
// quote: "The project associated with the issue. Can be a UUID or project identifier (e.g., 'P-LIN-123')."
func TestTicketsInProjects(t *testing.T) {
	c, l := recorded(t)
	ctx := t.Context()
	ps, err := c.Projects(ctx, "", "")
	if err != nil || len(ps) != 2 || ps[0].Name != "Usage" || ps[1].Status != "Planned" || !reflect.DeepEqual(ps[1].Teams, []string{"ENG", "OPS"}) {
		t.Fatalf("projects %+v, %v", ps, err)
	}
	if ps, _ := c.Projects(ctx, "ops", ""); len(ps) != 1 || ps[0].Name != "Billing revamp" {
		t.Errorf("OPS's projects: %+v", ps)
	}
	if ps, _ := c.Projects(ctx, "", "billing"); len(ps) != 1 || ps[0].Name != "Billing revamp" {
		t.Errorf("projects with billing in their names: %+v", ps)
	}

	ed, err := c.Update(ctx, "ENG-2231", Change{Project: "billing revamp"})
	if err != nil || !strings.Contains(ed.Line, "project Usage → Billing revamp") {
		t.Fatalf("moved: %+v %v", ed, err)
	}
	if in := l.Mutations()[0].Vars["input"].(map[string]any); in["projectId"] != "7d2e4f60-1a3b-4c5d-8e9f-0a1b2c3d0002" {
		t.Errorf("moved: %+v", in)
	}
	if tk, _ := c.Ticket(ctx, "ENG-2231"); tk.Project != "Billing revamp" {
		t.Errorf("read back in %q", tk.Project)
	}
	if ed, err := c.Update(ctx, "ENG-2231", Change{Project: "none"}); err != nil || !strings.Contains(ed.Line, "out of Billing revamp") {
		t.Fatalf("taken out: %+v %v", ed, err)
	}
	if in := l.Mutations()[1].Vars["input"].(map[string]any); in["projectId"] != nil {
		t.Errorf("taken out: %+v", in)
	}

	made, err := c.Create(ctx, NewTicket{Team: "ENG", Title: "Meter the export API", Project: "https://linear.app/acme/project/usage-8f3a1c2b9d4e"})
	if err != nil || !strings.HasSuffix(made.Line, "in project Usage") {
		t.Fatalf("filed: %+v %v", made, err)
	}
	if tk, _ := c.Ticket(ctx, made.Key); tk.Project != "Usage" {
		t.Errorf("filed in %q", tk.Project)
	}

	sent := len(l.Mutations())
	if _, err := c.Update(ctx, "ENG-2231", Change{Project: "Pricing"}); err == nil || !strings.Contains(err.Error(), "Usage, Billing revamp") {
		t.Errorf("a project Linear hasn't: %v", err)
	}
	if len(l.Mutations()) != sent {
		t.Error("a ticket was changed for a project Linear hasn't")
	}
}
