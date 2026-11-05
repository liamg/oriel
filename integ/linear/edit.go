package linear

import (
	"context"
	"fmt"
	"strings"
)

// Edit is what a change did, for the agent: the ticket and a line saying what was done.
type Edit struct {
	Key  string `json:"key"`
	URL  string `json:"url"`
	Line string `json:"line"`
}

// Change is an update to a ticket; what's left empty stays as it is. State, Assignee are names as a person says
// them: a state of the ticket's team, a person by name, display name or email ("me" for the key's owner, "none"
// for nobody).
type Change struct {
	Title, Description, State, Assignee string
	Priority                            *float64 // 0 none, 1 urgent, 2 high, 3 medium, 4 low
}

// NewTicket is a ticket to create, in a team named by its key or name (the only team, if there's one).
type NewTicket struct {
	Team, Title, Description, State, Assignee string
	Priority                                  *float64
}

var priorityNames = []string{"No priority", "Urgent", "High", "Medium", "Low"}

func checkPriority(p *float64) error {
	if p != nil && (*p < 0 || *p > 4 || *p != float64(int(*p))) {
		return fmt.Errorf("priority is 0 (none), 1 (urgent), 2 (high), 3 (medium) or 4 (low)")
	}
	return nil
}

// stateNamed is the state of states called name, any case.
func stateNamed(states []State, name, whose string) (State, error) {
	var names []string
	for _, st := range states {
		if strings.EqualFold(st.Name, strings.TrimSpace(name)) {
			return st, nil
		}
		names = append(names, st.Name)
	}
	return State{}, fmt.Errorf("%s has no state %q: %s", whose, name, strings.Join(names, ", "))
}

// person is the id to assign for who, with their name; "" for "none".
func (c Client) person(ctx context.Context, who, viewer string) (id, name string, err error) {
	who = strings.TrimSpace(who)
	switch strings.ToLower(who) {
	case "none", "nobody", "unassigned":
		return "", "", nil
	case "me":
		return viewer, "you", nil
	}
	var out struct {
		Users struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"nodes"`
		} `json:"users"`
	}
	const query = `query($filter: UserFilter) { users(filter: $filter) { nodes { id name } } }`
	eq := map[string]any{"eqIgnoreCase": who}
	filter := map[string]any{"or": []any{map[string]any{"name": eq}, map[string]any{"displayName": eq}, map[string]any{"email": eq}}}
	if err := c.Do(ctx, query, map[string]any{"filter": filter}, &out); err != nil {
		return "", "", err
	}
	switch n := out.Users.Nodes; len(n) {
	case 1:
		return n[0].ID, n[0].Name, nil
	case 0:
		return "", "", fmt.Errorf("no one in Linear is called %q", who)
	default:
		var names []string
		for _, u := range n {
			names = append(names, u.Name)
		}
		return "", "", fmt.Errorf("%q could be %s: say which by email", who, strings.Join(names, " or "))
	}
}

// Update changes a ticket: its title, description, state, assignee or priority.
func (c Client) Update(ctx context.Context, key string, ch Change) (Edit, error) {
	if err := checkPriority(ch.Priority); err != nil {
		return Edit{}, err
	}
	if ch == (Change{}) {
		return Edit{}, fmt.Errorf("nothing to change on %s", key)
	}
	t, err := c.Ticket(ctx, key)
	if err != nil {
		return Edit{}, err
	}
	input := map[string]any{}
	var did []string
	if ch.Title != "" {
		input["title"], did = ch.Title, append(did, "title")
	}
	if ch.Description != "" {
		input["description"], did = ch.Description, append(did, "description")
	}
	if ch.State != "" {
		st, err := stateNamed(t.States, ch.State, t.Key+"'s team")
		if err != nil {
			return Edit{}, err
		}
		input["stateId"], did = st.ID, append(did, "state "+t.State+" → "+st.Name)
	}
	if ch.Priority != nil {
		p := int(*ch.Priority)
		input["priority"], did = p, append(did, "priority "+cmpOr(t.PriorityLabel, "none")+" → "+priorityNames[p])
	}
	if ch.Assignee != "" {
		id, name, err := c.person(ctx, ch.Assignee, t.viewer)
		if err != nil {
			return Edit{}, err
		}
		if id == "" {
			input["assigneeId"], did = nil, append(did, "unassigned")
		} else {
			input["assigneeId"], did = id, append(did, "assignee "+cmpOr(t.Assignee, "nobody")+" → "+name)
		}
	}
	ok, err := c.UpdateIssue(ctx, t.ID, input)
	if err != nil {
		return Edit{}, err
	}
	if !ok {
		return Edit{}, fmt.Errorf("linear refused to update %s", t.Key)
	}
	return Edit{Key: t.Key, URL: t.URL, Line: t.Key + ": " + strings.Join(did, ", ")}, nil
}

// Comment adds a comment, in markdown, to a ticket.
func (c Client) Comment(ctx context.Context, key, body string) (Edit, error) {
	if strings.TrimSpace(body) == "" {
		return Edit{}, fmt.Errorf("a comment needs a body")
	}
	const mutation = `mutation($input: CommentCreateInput!) {
		commentCreate(input: $input) { success comment { url } } }`
	var out struct {
		CommentCreate struct {
			Success bool `json:"success"`
			Comment struct {
				URL string `json:"url"`
			} `json:"comment"`
		} `json:"commentCreate"`
	}
	if err := c.Do(ctx, mutation, map[string]any{"input": map[string]any{"issueId": key, "body": body}}, &out); err != nil {
		return Edit{}, err
	}
	if !out.CommentCreate.Success {
		return Edit{}, fmt.Errorf("linear refused the comment on %s", key)
	}
	return Edit{Key: key, URL: out.CommentCreate.Comment.URL, Line: "commented on " + key}, nil
}

// Create files a new ticket.
func (c Client) Create(ctx context.Context, n NewTicket) (Edit, error) {
	if strings.TrimSpace(n.Title) == "" {
		return Edit{}, fmt.Errorf("a ticket needs a title")
	}
	if err := checkPriority(n.Priority); err != nil {
		return Edit{}, err
	}
	const query = `{ viewer { id } teams(first: 100) { nodes { id key name states(first: 50) { nodes { id name type position } } } } }`
	type team struct {
		ID     string `json:"id"`
		Key    string `json:"key"`
		Name   string `json:"name"`
		States struct {
			Nodes []State `json:"nodes"`
		} `json:"states"`
	}
	var out struct {
		Viewer struct {
			ID string `json:"id"`
		} `json:"viewer"`
		Teams struct {
			Nodes []team `json:"nodes"`
		} `json:"teams"`
	}
	if err := c.Do(ctx, query, nil, &out); err != nil {
		return Edit{}, err
	}
	var tm *team
	var names []string
	for i, t := range out.Teams.Nodes {
		names = append(names, t.Key+" ("+t.Name+")")
		if n.Team != "" && (strings.EqualFold(t.Key, n.Team) || strings.EqualFold(t.Name, n.Team)) {
			tm = &out.Teams.Nodes[i]
		}
	}
	switch {
	case tm == nil && n.Team == "" && len(out.Teams.Nodes) == 1:
		tm = &out.Teams.Nodes[0]
	case tm == nil && n.Team == "":
		return Edit{}, fmt.Errorf("which team? %s", strings.Join(names, ", "))
	case tm == nil:
		return Edit{}, fmt.Errorf("no team %s in Linear: %s", n.Team, strings.Join(names, ", "))
	}
	input := map[string]any{"teamId": tm.ID, "title": n.Title}
	if n.Description != "" {
		input["description"] = n.Description
	}
	if n.State != "" {
		st, err := stateNamed(tm.States.Nodes, n.State, "team "+tm.Key)
		if err != nil {
			return Edit{}, err
		}
		input["stateId"] = st.ID
	}
	if n.Priority != nil {
		input["priority"] = int(*n.Priority)
	}
	if n.Assignee != "" {
		id, _, err := c.person(ctx, n.Assignee, out.Viewer.ID)
		if err != nil {
			return Edit{}, err
		}
		if id != "" {
			input["assigneeId"] = id
		}
	}
	const mutation = `mutation($input: IssueCreateInput!) {
		issueCreate(input: $input) { success issue { identifier url } } }`
	var made struct {
		IssueCreate struct {
			Success bool `json:"success"`
			Issue   *struct {
				Identifier string `json:"identifier"`
				URL        string `json:"url"`
			} `json:"issue"`
		} `json:"issueCreate"`
	}
	if err := c.Do(ctx, mutation, map[string]any{"input": input}, &made); err != nil {
		return Edit{}, err
	}
	is := made.IssueCreate.Issue
	if !made.IssueCreate.Success || is == nil {
		return Edit{}, fmt.Errorf("linear refused the new ticket in %s", tm.Key)
	}
	return Edit{Key: is.Identifier, URL: is.URL, Line: "created " + is.Identifier + " in " + tm.Name}, nil
}
