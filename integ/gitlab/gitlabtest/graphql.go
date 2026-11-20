package gitlabtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
)

// Calls are the requests made of the fake, in order: "GET /api/v4/user".
func (g *GitLab) Calls() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.calls)
}

// ClearCalls forgets the requests made so far.
func (g *GitLab) ClearCalls() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = nil
}

// RefuseGraphQL makes every GraphQL query fail, as on a GitLab too old for it.
func (g *GitLab) RefuseGraphQL() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.noGraph = true
}

// graphql answers the query for a merge request's notes' award emoji, 100 notes a page after the cursor (the index
// of the last one given), and who you are.
func (g *GitLab) graphql(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Variables struct {
			Path  string  `json:"path"`
			IID   string  `json:"iid"`
			After *string `json:"after"`
		} `json:"variables"`
	}
	_ = json.NewDecoder(r.Body).Decode(&in)
	if g.noGraph {
		reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "Field 'awardEmoji' doesn't exist on type 'Note'"}}})
		return
	}
	iid, _ := strconv.Atoi(in.Variables.IID)
	m := g.find(in.Variables.Path, iid)
	if m == nil {
		reply(w, 200, map[string]any{"data": map[string]any{"currentUser": map[string]string{"username": Me}, "project": nil}})
		return
	}
	var nodes []map[string]any
	for _, d := range m.discussions {
		kind := "Note"
		if d.path != "" {
			kind = "DiffNote"
		}
		for _, n := range d.notes {
			awards := []map[string]any{}
			for _, a := range g.awards[n.id] {
				awards = append(awards, map[string]any{"name": a.name, "user": map[string]string{"username": a.user}})
			}
			nodes = append(nodes, map[string]any{"id": fmt.Sprintf("gid://gitlab/%s/%d", kind, n.id), "awardEmoji": map[string]any{"nodes": awards}})
		}
	}
	start := 0
	if in.Variables.After != nil {
		start, _ = strconv.Atoi(*in.Variables.After)
		start++
	}
	start = min(start, len(nodes))
	end := min(start+100, len(nodes))
	notes := map[string]any{"nodes": nodes[start:end], "pageInfo": map[string]any{"hasNextPage": end < len(nodes), "endCursor": strconv.Itoa(end - 1)}}
	reply(w, 200, map[string]any{"data": map[string]any{"currentUser": map[string]string{"username": Me},
		"project": map[string]any{"mergeRequest": map[string]any{"notes": notes}}}})
}
