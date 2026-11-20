package ghtest

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// What a comment is, for its reactions: each kind numbers its own on GitHub.
const (
	LineComment = "PRRC" // a review comment on the diff
	IssueNote   = "IC"   // a comment in the conversation
	ReviewBody  = "PRR"  // a review's body
)

// reactionNames are GitHub's GraphQL names for its REST ones.
var reactionNames = map[string]string{"THUMBS_UP": "+1", "THUMBS_DOWN": "-1", "LAUGH": "laugh", "HOORAY": "hooray", "CONFUSED": "confused",
	"HEART": "heart", "ROCKET": "rocket", "EYES": "eyes"}

func node(kind string, id int64) string { return fmt.Sprintf("%s_%d", kind, id) }

// ReactAs has login react to a comment of kind with name ("+1", "rocket"…).
func (s *Server) ReactAs(login, kind string, id int64, name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.react(node(kind, id), name, login, true)
}

func (s *Server) react(n, name, login string, on bool) {
	if s.noteReactions[n] == nil {
		s.noteReactions[n] = map[string][]string{}
	}
	who := slices.DeleteFunc(s.noteReactions[n][name], func(l string) bool { return l == login })
	if on {
		who = append(who, login)
	}
	s.noteReactions[n][name] = who
}

func (s *Server) reactedNode(n string) map[string]any {
	groups := []map[string]any{}
	for enum, name := range reactionNames {
		who := s.noteReactions[n][name]
		// The query names the first 10.
		nodes := []map[string]string{}
		for _, login := range who[:min(len(who), 10)] {
			nodes = append(nodes, map[string]string{"login": login})
		}
		groups = append(groups, map[string]any{"content": enum, "viewerHasReacted": slices.Contains(who, s.login),
			"reactors": map[string]any{"totalCount": len(who), "nodes": nodes}})
	}
	var id int64
	_, _ = fmt.Sscanf(n[strings.LastIndex(n, "_")+1:], "%d", &id)
	return map[string]any{"databaseId": id, "id": n, "reactionGroups": groups}
}

// commentReactions answers the query for a PR's comments' reactions. Its line comments all come under one review.
func (s *Server) commentReactions(w http.ResponseWriter, vars map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, _ := vars["number"].(float64)
	key := prKey(fmt.Sprint(vars["owner"])+"/"+fmt.Sprint(vars["name"]), int(n))
	issue, lines, reviews := []map[string]any{}, []map[string]any{}, []map[string]any{}
	for _, c := range s.issue[key] {
		issue = append(issue, s.reactedNode(node(IssueNote, c.ID)))
	}
	for _, c := range s.review[key] {
		lines = append(lines, s.reactedNode(node(LineComment, c.ID)))
	}
	for _, r := range s.reviews[key] {
		reviews = append(reviews, s.reactedNode(node(ReviewBody, r.ID)))
	}
	if len(reviews) == 0 {
		reviews = append(reviews, s.reactedNode(node(ReviewBody, 0)))
	}
	reviews[0]["comments"] = map[string]any{"nodes": lines}
	reply(w, 200, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"comments": map[string]any{"nodes": issue}, "reviews": map[string]any{"nodes": reviews}}}}})
}

// reactNode is GitHub's addReaction and removeReaction mutations, as the signed-in user.
func (s *Server) reactNode(w http.ResponseWriter, query string, vars map[string]any) {
	s.mu.Lock()
	hold := s.holdReactions
	s.mu.Unlock()
	if hold != nil {
		<-hold
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	name, ok := reactionNames[fmt.Sprint(vars["content"])]
	if !ok {
		reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "Argument 'content' has an invalid value"}}})
		return
	}
	s.react(fmt.Sprint(vars["id"]), name, s.login, strings.Contains(query, "addReaction("))
	reply(w, 200, map[string]any{"data": map[string]any{}})
}

// ReactionsOn are who reacted to a comment of kind, by each reaction's name.
func (s *Server) ReactionsOn(kind string, id int64) map[string][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string][]string{}
	for name, who := range s.noteReactions[node(kind, id)] {
		if len(who) > 0 {
			out[name] = slices.Clone(who)
		}
	}
	return out
}
