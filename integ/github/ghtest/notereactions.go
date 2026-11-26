package ghtest

import (
	"fmt"

	gh "github.com/liamg/oriel/integ/github"
	"net/http"
	"slices"
	"strings"
	"sync"
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
	return map[string]any{"id": n, "reactionGroups": groups}
}

// reactionsWith are a comment's reactions as its REST summary counts them: those set on it, and those people made.
func (s *Server) reactionsWith(kind string, id int64, set gh.Reactions) gh.Reactions {
	out := gh.Reactions{}
	for name, n := range set {
		out[name] += n
	}
	for name, who := range s.noteReactions[node(kind, id)] {
		out[name] += len(who)
	}
	return out
}

// commentReactions answers the query for comments' reactions by their nodes, counting each read of it.
func (s *Server) commentReactions(w http.ResponseWriter, vars map[string]any) {
	s.mu.Lock()
	nodes := []map[string]any{}
	ids, _ := vars["ids"].([]any)
	for _, id := range ids {
		nodes = append(nodes, s.reactedNode(fmt.Sprint(id)))
	}
	s.reactionReads++
	held, stop := s.heldReads, s.stopReads
	s.mu.Unlock()
	if held != nil {
		r := make(chan struct{})
		select {
		case held <- r:
			select {
			case <-r:
			case <-stop:
			}
		case <-stop:
		}
	}
	reply(w, 200, map[string]any{"data": map[string]any{"nodes": nodes}})
}

// ReactionReads is how many times comments' reactions have been read.
func (s *Server) ReactionReads() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reactionReads
}

// HoldReactionReads holds each read of reactions after it has read them. held takes the next one waiting, if any,
// with the release that lets it go. The test's cleanup lets every read go.
func (s *Server) HoldReactionReads(t interface{ Cleanup(func()) }) (held func() (release func(), ok bool)) {
	reads, stop := make(chan chan struct{}), make(chan struct{})
	s.mu.Lock()
	s.heldReads, s.stopReads = reads, stop
	s.mu.Unlock()
	t.Cleanup(func() { close(stop) })
	return func() (func(), bool) {
		select {
		case r := <-reads:
			return sync.OnceFunc(func() { close(r) }), true
		default:
			return nil, false
		}
	}
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
