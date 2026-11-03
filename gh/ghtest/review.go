package ghtest

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/gh"
)

// A PR's review on the fake: comments on its lines and their replies, reviews, its conversation, which threads are
// resolved, and orgs' members.

func prKey(repo string, number int) string { return fmt.Sprintf("%s#%d", repo, number) }

// SetLogin is who the fake's client is signed in as ("you" until set): what it posts is theirs.
func (s *Server) SetLogin(login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.login = login
}

// AddReviewComment puts a comment on a PR's diff (InReplyTo set: a reply in that thread), numbered next.
func (s *Server) AddReviewComment(repo string, number int, c gh.ReviewComment) gh.ReviewComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.addReview(repo, number, c)
}

func (s *Server) addReview(repo string, number int, c gh.ReviewComment) *gh.ReviewComment {
	c.ID = s.id()
	c.Created = s.tick()
	c.Updated = c.Created
	if c.Side == "" {
		c.Side = "RIGHT"
	}
	if c.Association == "" {
		c.Association = "NONE"
	}
	if c.InReplyTo != 0 {
		for _, o := range s.review[prKey(repo, number)] {
			if o.ID == c.InReplyTo {
				c.Path, c.Line, c.OriginalLine, c.Side = o.Path, o.Line, o.OriginalLine, o.Side
			}
		}
	}
	if c.OriginalLine == 0 {
		c.OriginalLine = c.Line
	}
	c.URL = fmt.Sprintf("https://github.com/%s/pull/%d#discussion_r%d", repo, number, c.ID)
	p := &c
	s.review[prKey(repo, number)] = append(s.review[prKey(repo, number)], p)
	return p
}

// AddReview submits a review of a PR.
func (s *Server) AddReview(repo string, number int, r gh.Review) gh.Review {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = s.id()
	r.Submitted = s.tick()
	if r.Association == "" {
		r.Association = "NONE"
	}
	r.URL = fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-%d", repo, number, r.ID)
	s.reviewed(repo, number, &r)
	return r
}

// AddIssueComment adds a comment to a PR's conversation.
func (s *Server) AddIssueComment(repo string, number int, c gh.IssueComment) gh.IssueComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	return *s.addIssue(repo, number, c)
}

func (s *Server) addIssue(repo string, number int, c gh.IssueComment) *gh.IssueComment {
	c.ID = s.id()
	c.Created = s.tick()
	c.Updated = c.Created
	if c.Association == "" {
		c.Association = "NONE"
	}
	c.URL = fmt.Sprintf("https://github.com/%s/pull/%d#issuecomment-%d", repo, number, c.ID)
	p := &c
	s.issue[prKey(repo, number)] = append(s.issue[prKey(repo, number)], p)
	return p
}

// EditComment changes a review or conversation comment's text, as its author editing it on GitHub.
func (s *Server) EditComment(id int64, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.tick()
	for _, cs := range s.review {
		for _, c := range cs {
			if c.ID == id {
				c.Body, c.Updated = body, now
			}
		}
	}
	for _, cs := range s.issue {
		for _, c := range cs {
			if c.ID == id {
				c.Body, c.Updated = body, now
			}
		}
	}
}

// Resolve marks the review thread that starts with comment root resolved, or not.
func (s *Server) Resolve(root int64, resolved bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolved[root] = resolved
}

// AddMember makes login a member of org.
func (s *Server) AddMember(org, login string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.members[org] = append(s.members[org], login)
}

// ReviewComments are a PR's review comments, oldest first.
func (s *Server) ReviewComments(repo string, number int) []gh.ReviewComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gh.ReviewComment
	for _, c := range s.review[prKey(repo, number)] {
		out = append(out, *c)
	}
	return out
}

// IssueComments are a PR's conversation, oldest first.
func (s *Server) IssueComments(repo string, number int) []gh.IssueComment {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gh.IssueComment
	for _, c := range s.issue[prKey(repo, number)] {
		out = append(out, *c)
	}
	return out
}

func (s *Server) id() int64 {
	s.nextID++
	return int64(s.nextID) * 1000
}

func num(r *http.Request) int {
	n, _ := strconv.Atoi(r.PathValue("num"))
	return n
}

func user(login string) map[string]string { return map[string]string{"login": login} }

func restReview(c *gh.ReviewComment) map[string]any {
	m := map[string]any{"id": c.ID, "pull_request_review_id": c.Review, "user": user(c.User), "author_association": c.Association, "body": c.Body,
		"path": c.Path, "side": c.Side, "html_url": c.URL, "created_at": c.Created, "updated_at": c.Updated, "original_line": c.OriginalLine, "line": nil,
		"reactions": restReactions(c.Reactions)}
	if c.Line != 0 {
		m["line"] = c.Line
	}
	if c.InReplyTo != 0 {
		m["in_reply_to_id"] = c.InReplyTo
	}
	return m
}

func restIssue(c *gh.IssueComment) map[string]any {
	return map[string]any{"id": c.ID, "user": user(c.User), "author_association": c.Association, "body": c.Body, "html_url": c.URL,
		"created_at": c.Created, "updated_at": c.Updated, "reactions": restReactions(c.Reactions)}
}

// restReactions is a reactions summary as GitHub sends it: every reaction's count, 0 for none, and the total.
func restReactions(r gh.Reactions) map[string]any {
	out := map[string]any{"url": "", "total_count": 0}
	total := 0
	for _, name := range gh.ReactionNames {
		out[name] = r[name]
		total += r[name]
	}
	out["total_count"] = total
	return out
}

// SetPRReactions sets a PR's own reactions.
func (s *Server) SetPRReactions(repo string, number int, r gh.Reactions) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.prReactions[prKey(repo, number)] = r
}

// PRReactions are a PR's own reactions.
func (s *Server) PRReactions(repo string, number int) gh.Reactions {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.prReactions[prKey(repo, number)])
}

// addReaction reacts to a PR as the signed-in user: once per reaction, as GitHub counts them.
func (s *Server) addReaction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Content string `json:"content"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || !slices.Contains(gh.ReactionNames, in.Content) {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, p := s.find(r)
	if p == nil {
		notFound(w)
		return
	}
	key := prKey(repo, p.Number)
	if s.reacted[key+" "+in.Content] {
		reply(w, 200, map[string]any{"content": in.Content})
		return
	}
	if s.prReactions[key] == nil {
		s.prReactions[key] = gh.Reactions{}
	}
	s.prReactions[key][in.Content]++
	s.reacted[key+" "+in.Content] = true
	reply(w, 201, map[string]any{"content": in.Content})
}

func (s *Server) getIssue(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, p := s.find(r)
	if p == nil {
		notFound(w)
		return
	}
	reply(w, 200, map[string]any{"number": p.Number, "title": p.Title, "reactions": restReactions(s.prReactions[prKey(repo, p.Number)])})
}

func (s *Server) listReviewComments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, c := range s.review[prKey(repoOf(r), num(r))] {
		out = append(out, restReview(c))
	}
	reply(w, 200, out)
}

func (s *Server) reply(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	to, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	key := prKey(repoOf(r), num(r))
	i := slices.IndexFunc(s.review[key], func(c *gh.ReviewComment) bool { return c.ID == to })
	if i < 0 {
		notFound(w)
		return
	}
	root := s.review[key][i]
	if root.InReplyTo != 0 {
		to = root.InReplyTo
	}
	c := s.addReview(repoOf(r), num(r), gh.ReviewComment{InReplyTo: to, User: s.login, Association: "OWNER", Body: in.Body})
	reply(w, 201, restReview(c))
}

func (s *Server) listReviews(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, rv := range s.reviews[prKey(repoOf(r), num(r))] {
		out = append(out, map[string]any{"id": rv.ID, "user": user(rv.User), "author_association": rv.Association, "state": rv.State, "body": rv.Body,
			"html_url": rv.URL, "submitted_at": rv.Submitted, "commit_id": rv.Commit})
	}
	reply(w, 200, out)
}

func (s *Server) listIssueComments(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []map[string]any{}
	for _, c := range s.issue[prKey(repoOf(r), num(r))] {
		out = append(out, restIssue(c))
	}
	reply(w, 200, out)
}

func (s *Server) addIssueComment(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Body string `json:"body"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.Body == "" {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, p := s.findNum(repoOf(r), num(r)); p == nil {
		notFound(w)
		return
	}
	c := s.addIssue(repoOf(r), num(r), gh.IssueComment{User: s.login, Association: "OWNER", Body: in.Body})
	reply(w, 201, restIssue(c))
}

func (s *Server) member(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if slices.Contains(s.members[r.PathValue("org")], r.PathValue("user")) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	notFound(w)
}

func (s *Server) reviewThreads(w http.ResponseWriter, vars map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, _ := vars["number"].(float64)
	key := prKey(fmt.Sprint(vars["owner"])+"/"+fmt.Sprint(vars["name"]), int(n))
	nodes := []map[string]any{}
	for _, c := range s.review[key] {
		if c.InReplyTo != 0 {
			continue
		}
		nodes = append(nodes, map[string]any{"id": fmt.Sprintf("RT_%d", c.ID), "isResolved": s.resolved[c.ID],
			"comments": map[string]any{"nodes": []map[string]any{{"databaseId": c.ID}}}})
	}
	reply(w, 200, map[string]any{"data": map[string]any{"repository": map[string]any{"pullRequest": map[string]any{
		"reviewThreads": map[string]any{"nodes": nodes}}}}})
}

// resolveThread is GitHub's resolveReviewThread and unresolveReviewThread mutations, on a thread's id from
// reviewThreads.
func (s *Server) resolveThread(w http.ResponseWriter, query string, vars map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var root int64
	if _, err := fmt.Sscanf(fmt.Sprint(vars["id"]), "RT_%d", &root); err != nil {
		reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "Could not resolve to a node"}}})
		return
	}
	resolved := !strings.Contains(query, "unresolveReviewThread")
	s.resolved[root] = resolved
	reply(w, 200, map[string]any{"data": map[string]any{"resolveReviewThread": map[string]any{"thread": map[string]bool{"isResolved": resolved}}}})
}

// Resolved says whether the review thread that starts with comment root is resolved.
func (s *Server) Resolved(root int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.resolved[root]
}
