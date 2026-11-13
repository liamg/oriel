package ghtest

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/gh"
)

// MergeRepo is how a repository merges: the methods it allows (all three when none) and its default.
type MergeRepo struct {
	Methods []string
	Default string
}

// SetMergeRepo sets how a repository merges.
func (s *Server) SetMergeRepo(repo string, m MergeRepo) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeRepo[repo] = m
}

// SetMergeable sets what GitHub says of merging a pull request: mergeable (MERGEABLE, CONFLICTING) and its
// mergeStateStatus (CLEAN, BEHIND, BLOCKED…).
func (s *Server) SetMergeable(repo string, number int, mergeable, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mergeable[prKey(repo, number)] = [2]string{mergeable, status}
}

// MergedWith is the method a pull request was merged with through the API, "" if it wasn't.
func (s *Server) MergedWith(repo string, number int) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mergedWith[prKey(repo, number)]
}

// Deleted are the branches deleted through the API, as owner/name:branch.
func (s *Server) Deleted() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.deleted)
}

// sha is a pull request's head commit: the last one added, else one made up from its number.
func (s *Server) sha(repo string, p *gh.PR) string {
	if h := s.head(repo, p.Number); h != "" {
		return h
	}
	if p.SHA != "" {
		return p.SHA
	}
	return fmt.Sprintf("%040d", p.Number)
}

func (s *Server) mergeState(w http.ResponseWriter, vars map[string]any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo := fmt.Sprint(vars["owner"]) + "/" + fmt.Sprint(vars["name"])
	n, _ := vars["number"].(float64)
	_, p := s.findNum(repo, int(n))
	if p == nil {
		reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "Could not resolve to a PullRequest"}}})
		return
	}
	m := s.mergeRepo[repo]
	if len(m.Methods) == 0 {
		m.Methods = []string{"merge", "squash", "rebase"}
	}
	k := prKey(repo, p.Number)
	st := s.mergeable[k]
	if st[0] == "" {
		st = [2]string{"MERGEABLE", "CLEAN"}
	}
	threads := []map[string]any{}
	for _, c := range s.review[k] {
		if c.InReplyTo == 0 {
			threads = append(threads, map[string]any{"isResolved": s.resolved[c.ID]})
		}
	}
	latest := map[string]string{}
	var order []string
	for _, r := range s.reviews[k] {
		if r.State == "COMMENTED" {
			continue
		}
		if _, ok := latest[r.User]; !ok {
			order = append(order, r.User)
		}
		latest[r.User] = r.State
	}
	reviews := []map[string]any{}
	decision := ""
	for _, who := range order {
		reviews = append(reviews, map[string]any{"state": latest[who], "author": map[string]string{"login": who}})
		switch {
		case latest[who] == gh.ReviewChangesRequested:
			decision = "CHANGES_REQUESTED"
		case latest[who] == gh.ReviewApproved && decision == "":
			decision = "APPROVED"
		}
	}
	reply(w, 200, map[string]any{"data": map[string]any{"repository": map[string]any{
		"mergeCommitAllowed": slices.Contains(m.Methods, "merge"), "squashMergeAllowed": slices.Contains(m.Methods, "squash"),
		"rebaseMergeAllowed": slices.Contains(m.Methods, "rebase"), "viewerDefaultMergeMethod": strings.ToUpper(m.Default),
		"pullRequest": map[string]any{"headRefOid": s.sha(repo, p), "isDraft": p.Draft, "mergeable": st[0], "mergeStateStatus": st[1],
			"reviewDecision": decision, "reviewThreads": map[string]any{"nodes": threads}, "latestReviews": map[string]any{"nodes": reviews}}}}})
}

// merge is GitHub's merge: refused for a draft, a conflict, a method the repo doesn't allow, or a head that moved on.
func (s *Server) merge(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method string `json:"merge_method"`
		SHA    string `json:"sha"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	s.mu.Lock()
	defer s.mu.Unlock()
	repo := repoOf(r)
	n, _ := strconv.Atoi(r.PathValue("num"))
	_, p := s.findNum(repo, n)
	if p == nil {
		notFound(w)
		return
	}
	m := s.mergeRepo[repo]
	if len(m.Methods) == 0 {
		m.Methods = []string{"merge", "squash", "rebase"}
	}
	k := prKey(repo, n)
	switch {
	case !p.Open():
		reply(w, 405, map[string]string{"message": "Pull Request is not mergeable"})
	case p.Draft:
		reply(w, 405, map[string]string{"message": "Pull Request is still a draft"})
	case s.mergeable[k][0] == "CONFLICTING":
		reply(w, 405, map[string]string{"message": "Pull Request is not mergeable"})
	case !slices.Contains(m.Methods, cmp.Or(in.Method, "merge")):
		reply(w, 405, map[string]string{"message": "Merge method not allowed for this repository"})
	case in.SHA != "" && in.SHA != s.sha(repo, p):
		reply(w, 409, map[string]string{"message": "Head branch was modified. Review and try the merge again."})
	default:
		p.State, p.Merged = "closed", true
		s.mergedWith[k] = cmp.Or(in.Method, "merge")
		reply(w, 200, map[string]any{"merged": true, "message": "Pull Request successfully merged"})
	}
}

func (s *Server) deleteRef(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleted = append(s.deleted, repoOf(r)+":"+r.PathValue("ref"))
	w.WriteHeader(204)
}
