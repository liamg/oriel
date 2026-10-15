package linear

import (
	"context"
	"fmt"
	"slices"
	"time"
)

// Activity is one line of the workspace's recent activity: a ticket created or moved.
type Activity struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"` // created or moved
	Key    string    `json:"key"`  // ENG-34
	Title  string    `json:"title"`
	Who    string    `json:"who,omitempty"`
	URL    string    `json:"url,omitempty"`
	From   string    `json:"from,omitempty"`
	To     string    `json:"to,omitempty"`
	ToType string    `json:"toType,omitempty"` // Linear's state type: started, completed…
	Ago    string    `json:"ago,omitempty"`    // how long before now: 5m, 2h, 3d
}

const activityQuery = `query($since: DateTimeOrDuration!) {
	issues(first: 60, orderBy: updatedAt, filter: { updatedAt: { gte: $since } }) { nodes {
		identifier title url createdAt
		creator { displayName }
		history(first: 25) { nodes { createdAt actor { displayName } fromState { name } toState { name type } } } } } }`

// Activity is every ticket created, and every state change made, across the workspace since since, newest first.
func (c Client) Activity(ctx context.Context, since, now time.Time) ([]Activity, error) {
	type person struct {
		DisplayName string `json:"displayName"`
	}
	type state struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	var out struct {
		Issues struct {
			Nodes []struct {
				Identifier string    `json:"identifier"`
				Title      string    `json:"title"`
				URL        string    `json:"url"`
				CreatedAt  time.Time `json:"createdAt"`
				Creator    *person   `json:"creator"`
				History    struct {
					Nodes []struct {
						CreatedAt time.Time `json:"createdAt"`
						Actor     *person   `json:"actor"`
						FromState *state    `json:"fromState"`
						ToState   *state    `json:"toState"`
					} `json:"nodes"`
				} `json:"history"`
			} `json:"nodes"`
		} `json:"issues"`
	}
	if err := c.Do(ctx, activityQuery, map[string]any{"since": since.UTC().Format(time.RFC3339)}, &out); err != nil {
		return nil, err
	}
	events := []Activity{}
	for _, is := range out.Issues.Nodes {
		base := Activity{Key: is.Identifier, Title: is.Title, URL: is.URL}
		if is.CreatedAt.After(since) {
			e := base
			e.At, e.Kind = is.CreatedAt, "created"
			if is.Creator != nil {
				e.Who = is.Creator.DisplayName
			}
			events = append(events, e)
		}
		for _, h := range is.History.Nodes {
			// The state a ticket is created in is not a move.
			if h.ToState == nil || h.FromState == nil || h.CreatedAt.Before(since) {
				continue
			}
			e := base
			e.At, e.Kind, e.From, e.To, e.ToType = h.CreatedAt, "moved", h.FromState.Name, h.ToState.Name, h.ToState.Type
			if h.Actor != nil {
				e.Who = h.Actor.DisplayName
			}
			events = append(events, e)
		}
	}
	slices.SortStableFunc(events, func(a, b Activity) int { return b.At.Compare(a.At) })
	for i := range events {
		events[i].Ago = ago(now.Sub(events[i].At))
	}
	return events, nil
}

// ago is a duration as the feed says it: 5m, 2h, 3d.
func ago(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}
