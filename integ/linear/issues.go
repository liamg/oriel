package linear

import (
	"context"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/liamg/oriel/registry"
)

// Issue is one ticket assigned to the API key's owner, as the tickets list shows it.
type Issue struct {
	Key           string   `json:"key"` // ENG-123
	Title         string   `json:"title"`
	URL           string   `json:"url"`
	State         string   `json:"state"`
	StateType     string   `json:"stateType"`         // Linear's category: triage, backlog, unstarted, started…
	Project       string   `json:"project,omitempty"` // the Linear project it's in, if any
	Desc          string   `json:"description,omitempty"`
	Priority      float64  `json:"priority"` // 1 urgent, 2 high, 3 medium, 4 low, 0 none: the first sort key
	PriorityLabel string   `json:"priorityLabel,omitempty"`
	Estimate      float64  `json:"estimate,omitempty"` // points; 0 when unsized
	Started       bool     `json:"started"`            // in progress: above the rest of its priority band
	Blocked       bool     `json:"blocked,omitempty"`  // another open ticket blocks it: below the rest of its band
	BlockedBy     []string `json:"blockedBy,omitempty"`
	// PR is the newest pull request attached to the ticket: by Oriel as it's raised, or by Linear's GitHub app.
	PR *PR `json:"pr,omitempty"`

	ID           string `json:"-"` // Linear's id, to update it
	StartedState string `json:"-"` // the team's in-progress state
}

// PR is a pull request read off a ticket's Linear attachments.
type PR struct {
	URL    string `json:"url"`
	Number int    `json:"number"`
	Repo   string `json:"repo,omitempty"` // owner/name, from the URL
}

// State is one workflow state of a team ("Todo", "In Progress", …).
type State struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

// prFromURL is the GitHub pull request an attachment's URL names (https://github.com/acme/web/pull/42), or nil.
func prFromURL(raw string) *PR {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host != "github.com" {
		return nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 4 || parts[2] != "pull" {
		return nil
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n <= 0 {
		return nil
	}
	return &PR{URL: "https://github.com/" + strings.Join(parts[:4], "/"), Number: n, Repo: parts[0] + "/" + parts[1]}
}

// pickState is the team's state of type typ called name, else its first of that type by position; nil if it has
// none of that type.
func pickState(states []State, typ, name string) *State {
	var best *State
	for i := range states {
		st := &states[i]
		if st.Type != typ {
			continue
		}
		if strings.EqualFold(st.Name, name) {
			return st
		}
		if best == nil || st.Position < best.Position {
			best = st
		}
	}
	return best
}

// startedState is where Start work moves a ticket: the team's first started state, In Progress if it has one.
func startedState(states []State) string {
	if st := pickState(states, "started", "in progress"); st != nil {
		return st.ID
	}
	return ""
}

// reviewState is where a ticket goes when its PR is raised: the team's started state called In Review; nil if it
// has none, so a team without one keeps the ticket where it is.
func reviewState(states []State) *State {
	if st := pickState(states, "started", "in review"); st != nil && strings.EqualFold(st.Name, "in review") {
		return st
	}
	return nil
}

// doneState is where a ticket goes when its session's PRs are merged: the team's completed state called Done, else
// its first completed state; nil if it has none.
func doneState(states []State) *State { return pickState(states, "completed", "done") }

// AssignedIssues are the viewer's open assigned issues, in the order the work happens in (sortIssues).
func (c Client) AssignedIssues(ctx context.Context) ([]Issue, error) {
	const query = `{ viewer { assignedIssues(first: 50, filter: {state: {type: {nin: ["completed", "canceled"]}}}) {
		nodes { id identifier title url description priority priorityLabel estimate
			state { name type }
			project { name }
			attachments(first: 20) { nodes { url createdAt } }
			inverseRelations(first: 20) { nodes { type issue { identifier state { name type } } } }
			team { states(first: 50) { nodes { id name type position } } } } } } }`
	var out struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []struct {
					ID            string  `json:"id"`
					Identifier    string  `json:"identifier"`
					Title         string  `json:"title"`
					URL           string  `json:"url"`
					Description   string  `json:"description"`
					Priority      float64 `json:"priority"`
					PriorityLabel string  `json:"priorityLabel"`
					Estimate      float64 `json:"estimate"`
					State         struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"state"`
					Project struct {
						Name string `json:"name"`
					} `json:"project"`
					Attachments struct {
						Nodes []struct {
							URL       string `json:"url"`
							CreatedAt string `json:"createdAt"`
						} `json:"nodes"`
					} `json:"attachments"`
					// The relations pointing at this ticket: a "blocks" one is another blocking it.
					InverseRelations struct {
						Nodes []struct {
							Type  string `json:"type"`
							Issue struct {
								Identifier string `json:"identifier"`
								State      struct {
									Name string `json:"name"`
									Type string `json:"type"`
								} `json:"state"`
							} `json:"issue"`
						} `json:"nodes"`
					} `json:"inverseRelations"`
					Team struct {
						States struct {
							Nodes []State `json:"nodes"`
						} `json:"states"`
					} `json:"team"`
				} `json:"nodes"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	}
	if err := c.Do(ctx, query, nil, &out); err != nil {
		return nil, err
	}
	issues := make([]Issue, 0, len(out.Viewer.AssignedIssues.Nodes))
	seen := map[string]bool{}
	for _, n := range out.Viewer.AssignedIssues.Nodes {
		// The filter drops finished tickets by type; a Duplicate can be any type.
		if hiddenState(n.State.Name, n.State.Type) || seen[n.Identifier] {
			continue
		}
		seen[n.Identifier] = true
		var pr *PR
		newest := ""
		for _, a := range n.Attachments.Nodes {
			if p := prFromURL(a.URL); p != nil && (pr == nil || a.CreatedAt > newest) {
				pr, newest = p, a.CreatedAt
			}
		}
		// A blocker that is already done or cancelled blocks nothing.
		var blockedBy []string
		for _, rel := range n.InverseRelations.Nodes {
			b := rel.Issue
			if rel.Type != "blocks" || b.Identifier == "" {
				continue
			}
			if hiddenState(b.State.Name, b.State.Type) {
				continue
			}
			blockedBy = append(blockedBy, b.Identifier)
		}
		sort.Slice(blockedBy, func(i, j int) bool { return lessTicketKey(blockedBy[i], blockedBy[j]) })
		issues = append(issues, Issue{
			PR:  pr,
			Key: n.Identifier, Title: n.Title, URL: n.URL,
			State: n.State.Name, Project: n.Project.Name, Desc: n.Description,
			ID: n.ID, StateType: n.State.Type,
			StartedState:  startedState(n.Team.States.Nodes),
			Priority:      n.Priority,
			PriorityLabel: n.PriorityLabel,
			Estimate:      n.Estimate,
			Started:       n.State.Type == "started",
			Blocked:       len(blockedBy) > 0,
			BlockedBy:     blockedBy,
		})
	}
	sortIssues(issues)
	return issues, nil
}

// hiddenState says a ticket in this state is done with: finished, cancelled, or a duplicate (by name, as a
// workspace can file Duplicate under any type).
func hiddenState(name, typ string) bool {
	if typ == "completed" || typ == "canceled" {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(name), "duplicate")
}

// sortIssues orders the list as the work happens (registry.SortTickets).
func sortIssues(issues []Issue) {
	slices.SortStableFunc(issues, func(a, b Issue) int { return registry.CompareTickets(a.Ticket(), b.Ticket()) })
}

// lessTicketKey compares ticket keys by prefix, then numerically: FIX-9 before FIX-10.
func lessTicketKey(a, b string) bool { return registry.CompareTicketKeys(a, b) < 0 }

// stage is a Linear state's stage: its type, and In Review by name, since Linear files it under started.
func stage(name, typ string) string {
	switch {
	case typ != "started":
		return registry.TicketTodo
	case strings.EqualFold(strings.TrimSpace(name), "in review"):
		return registry.TicketInReview
	}
	return registry.TicketStarted
}

// Ticket is the issue as a row of Oriel's tickets.
func (is Issue) Ticket() registry.Ticket {
	st := stage(is.State, is.StateType)
	if is.Started && st == registry.TicketTodo {
		st = registry.TicketStarted
	}
	t := registry.Ticket{Key: is.Key, Title: is.Title, URL: is.URL, State: is.State, Stage: st, Project: is.Project,
		Priority: int(is.Priority), Blocked: is.Blocked, BlockedBy: is.BlockedBy}
	if is.Priority > 0 {
		t.PriorityLabel = is.PriorityLabel
	}
	if is.PR != nil {
		t.PR = &registry.TicketPR{Number: is.PR.Number, URL: is.PR.URL, Repo: is.PR.Repo}
	}
	return t
}
