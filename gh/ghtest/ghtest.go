// Package ghtest is a fake GitHub: the REST calls gh.API makes, kept in memory behind an httptest server, for tests
// that must never reach the network.
package ghtest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/internal/isolate"
)

// Server is the fake. Its methods seed it and read back what was done to it.
type Server struct {
	*httptest.Server
	mu     sync.Mutex
	now    time.Time
	nextID int
	prs    map[string][]*gh.PR   // by owner/name
	checks map[string][]gh.Check // by owner/name@ref
	logs   map[int64]string      // Actions jobs' logs
	flows  map[int64]int64       // check suite → Actions workflow
	fail   map[string]*gh.Error  // "METHOD path-prefix" → the error it answers
	calls  []string              // "POST /repos/acme/api/pulls"
	lag    bool                  // PRs made while it's set are left out of listings, as GitHub's search lags
	unseen map[string]bool       // owner/name#number: made while lagging

	login        string                         // who the fake's client is: what it posts is by them
	review       map[string][]*gh.ReviewComment // by owner/name#number
	reviews      map[string][]gh.Review
	issue        map[string][]*gh.IssueComment
	resolved     map[int64]bool          // review threads resolved, by their first comment
	members      map[string][]string     // org → logins
	sizes        map[string]PRSize       // by owner/name#number
	diffLines    map[string][]string     // by owner/name#number: where a review may comment
	teams        map[string][]string     // org/team → logins
	commits      map[string][]string     // by owner/name#number, oldest first
	prReactions  map[string]gh.Reactions // by owner/name#number: the PR's own
	reacted      map[string]int64        // "owner/name#number content": the signed-in user's own reactions, by id
	nextReaction int64
	asked        map[string]map[string]time.Time // by owner/name#number: when each reviewer was last asked

	mergeRepo  map[string]MergeRepo // by owner/name
	mergeable  map[string][2]string // by owner/name#number: mergeable, mergeStateStatus
	mergedWith map[string]string    // by owner/name#number
	deleted    []string
}

// New starts a fake GitHub that closes with the test.
func New(t testing.TB) *Server {
	isolate.Test(t)
	s := &Server{now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), nextID: 790, prs: map[string][]*gh.PR{}, checks: map[string][]gh.Check{}, flows: map[int64]int64{},
		logs: map[int64]string{}, fail: map[string]*gh.Error{}, login: "you", review: map[string][]*gh.ReviewComment{}, reviews: map[string][]gh.Review{},
		issue: map[string][]*gh.IssueComment{}, resolved: map[int64]bool{}, members: map[string][]string{}, sizes: map[string]PRSize{}, diffLines: map[string][]string{},
		teams: map[string][]string{}, commits: map[string][]string{}, prReactions: map[string]gh.Reactions{}, reacted: map[string]int64{}, asked: map[string]map[string]time.Time{},
		mergeRepo: map[string]MergeRepo{}, mergeable: map[string][2]string{}, mergedWith: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls", s.listPRs)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls", s.createPR)
	mux.HandleFunc("PATCH /repos/{owner}/{name}/pulls/{num}", s.editPR)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{num}/requested_reviewers", s.reviewers)
	mux.HandleFunc("GET /repos/{owner}/{name}/commits/{ref}/check-runs", s.checkRuns)
	mux.HandleFunc("GET /repos/{owner}/{name}/actions/runs", s.workflowRuns)
	mux.HandleFunc("GET /repos/{owner}/{name}/commits/{ref}/status", s.status)
	mux.HandleFunc("GET /repos/{owner}/{name}/actions/jobs/{id}/logs", s.log)
	mux.HandleFunc("POST /graphql", s.graphql)
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{num}/comments", s.listReviewComments)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{num}/comments/{id}/replies", s.reply)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{num}/comments", s.fileComment)
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{num}/reviews", s.listReviews)
	mux.HandleFunc("GET /repos/{owner}/{name}/issues/{num}/comments", s.listIssueComments)
	mux.HandleFunc("GET /repos/{owner}/{name}/issues/{num}", s.getIssue)
	mux.HandleFunc("POST /repos/{owner}/{name}/issues/{num}/comments", s.addIssueComment)
	mux.HandleFunc("POST /repos/{owner}/{name}/issues/{num}/reactions", s.addReaction)
	mux.HandleFunc("GET /repos/{owner}/{name}/issues/{num}/reactions", s.listReactions)
	mux.HandleFunc("DELETE /repos/{owner}/{name}/issues/{num}/reactions/{id}", s.deleteReaction)
	mux.HandleFunc("GET /orgs/{org}/members/{user}", s.member)
	mux.HandleFunc("GET /search/issues", s.search)
	mux.HandleFunc("GET /user", s.user)
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{num}", s.prDetails)
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{num}/files", s.prFiles)
	mux.HandleFunc("GET /repos/{owner}/{name}/pulls/{num}/commits", s.prCommits)
	mux.HandleFunc("POST /repos/{owner}/{name}/pulls/{num}/reviews", s.submitReview)
	mux.HandleFunc("PUT /repos/{owner}/{name}/pulls/{num}/merge", s.merge)
	mux.HandleFunc("DELETE /repos/{owner}/{name}/git/refs/heads/{ref...}", s.deleteRef)
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.calls = append(s.calls, r.Method+" "+r.URL.Path)
		for k, e := range s.fail {
			if strings.HasPrefix(r.Method+" "+r.URL.Path, k) {
				s.mu.Unlock()
				w.WriteHeader(e.Status)
				json.NewEncoder(w).Encode(map[string]string{"message": e.Message})
				return
			}
		}
		s.mu.Unlock()
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(s.Close)
	return s
}

// Client is a gh.Client talking to the fake.
func (s *Server) Client() gh.Client { return gh.API{T: gh.HTTP{Base: s.URL, Token: "fake"}} }

// PRs are a repository's pull requests, oldest first.
func (s *Server) PRs(repo string) []gh.PR {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []gh.PR
	for _, p := range s.prs[repo] {
		out = append(out, *p)
	}
	return out
}

// AddPR puts a pull request in a repository, numbered next.
func (s *Server) AddPR(repo string, p gh.PR) gh.PR {
	s.mu.Lock()
	defer s.mu.Unlock()
	p.Number, p.NodeID = s.nextID, fmt.Sprintf("PR_%d", s.nextID)
	s.nextID++
	if p.State == "" {
		p.State = "open"
	}
	p.URL = fmt.Sprintf("https://github.com/%s/pull/%d", repo, p.Number)
	p.Created = s.tick()
	s.prs[repo] = append(s.prs[repo], &p)
	for _, who := range p.Reviewers {
		s.ask(repo, p.Number, who)
	}
	return p
}

// Merge merges a pull request, as someone on GitHub would.
func (s *Server) Merge(repo string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, p := s.findNum(repo, number); p != nil {
		p.State, p.Merged = "closed", true
	}
}

// ClosePR closes a pull request unmerged, as GitHub does when its commits reach the base some other way.
func (s *Server) ClosePR(repo string, number int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, p := s.findNum(repo, number); p != nil {
		p.State = "closed"
	}
}

// SetChecks sets the checks on a ref of a repository: a branch name, as the Ship page asks.
func (s *Server) SetChecks(repo, ref string, cs ...gh.Check) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.checks[repo+"@"+ref] = cs
}

// SetWorkflow says check suite suite is a run of Actions workflow workflow.
func (s *Server) SetWorkflow(suite, workflow int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flows[suite] = workflow
}

// SetLog sets an Actions job's log.
func (s *Server) SetLog(id int64, log string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs[id] = log
}

// Fail makes calls starting "METHOD /path" answer with e until it's set to nil.
func (s *Server) Fail(prefix string, e *gh.Error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e == nil {
		delete(s.fail, prefix)
		return
	}
	s.fail[prefix] = e
}

// Calls are the requests made so far, as "METHOD /path".
func (s *Server) Calls() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.calls)
}

func (s *Server) tick() time.Time {
	s.now = s.now.Add(time.Minute)
	return s.now
}

func repoOf(r *http.Request) string { return r.PathValue("owner") + "/" + r.PathValue("name") }

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) { reply(w, 404, map[string]string{"message": "Not Found"}) }

// restPR is a PR as GitHub's REST API writes it.
func (s *Server) restPR(repo string, p *gh.PR) map[string]any {
	owner, _, _ := strings.Cut(repo, "/")
	var users, teams []map[string]string
	for _, r := range p.Reviewers {
		if o, t, ok := strings.Cut(r, "/"); ok && o == owner {
			teams = append(teams, map[string]string{"slug": t})
		} else {
			users = append(users, map[string]string{"login": r})
		}
	}
	var merged any
	if p.Merged {
		merged = p.Created
	}
	var user any
	if p.Author != "" {
		user = map[string]string{"login": p.Author}
	}
	return map[string]any{"number": p.Number, "node_id": p.NodeID, "title": p.Title, "user": user, "body": p.Body, "html_url": p.URL, "state": p.State,
		"draft": p.Draft, "merged_at": merged, "created_at": p.Created, "head": map[string]string{"ref": p.Head, "sha": s.sha(repo, p)}, "base": map[string]string{"ref": p.Base},
		"requested_reviewers": users, "requested_teams": teams}
}

func (s *Server) find(r *http.Request) (string, *gh.PR) {
	n, _ := strconv.Atoi(r.PathValue("num"))
	return s.findNum(repoOf(r), n)
}

// Lag has PRs raised from now on left out of listings, as GitHub's lag behind what it has just made, until
// CatchUp.
func (s *Server) Lag() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lag = true
}

// CatchUp lists every PR again.
func (s *Server) CatchUp() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lag, s.unseen = false, nil
}

func (s *Server) listPRs(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo := repoOf(r)
	owner, _, _ := strings.Cut(repo, "/")
	head := strings.TrimPrefix(r.URL.Query().Get("head"), owner+":")
	state := r.URL.Query().Get("state")
	out := []map[string]any{}
	for _, p := range slices.Backward(s.prs[repo]) { // newest first, as GitHub sorts them
		if head != "" && p.Head != head || state != "all" && p.State != cmpOr(state, "open") || s.unseen[repo+"#"+strconv.Itoa(p.Number)] {
			continue
		}
		out = append(out, s.restPR(repo, p))
	}
	reply(w, 200, out)
}

func cmpOr(s, d string) string {
	if s == "" {
		return d
	}
	return s
}

func (s *Server) createPR(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title, Body, Head, Base string
		Draft                   bool
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Title == "" || in.Head == "" || in.Base == "" {
		reply(w, 422, map[string]string{"message": "Validation Failed"})
		return
	}
	repo := repoOf(r)
	s.mu.Lock()
	for _, p := range s.prs[repo] {
		if p.Head == in.Head && p.State == "open" {
			s.mu.Unlock()
			reply(w, 422, map[string]string{"message": "A pull request already exists for " + in.Head + "."})
			return
		}
	}
	s.mu.Unlock()
	p := s.AddPR(repo, gh.PR{Title: in.Title, Body: in.Body, Head: in.Head, Base: in.Base, Draft: in.Draft})
	s.mu.Lock()
	defer s.mu.Unlock()
	_, found := s.findNum(repo, p.Number)
	if s.lag {
		if s.unseen == nil {
			s.unseen = map[string]bool{}
		}
		s.unseen[repo+"#"+strconv.Itoa(p.Number)] = true
	}
	reply(w, 201, s.restPR(repo, found))
}

func (s *Server) findNum(repo string, n int) (string, *gh.PR) {
	for _, p := range s.prs[repo] {
		if p.Number == n {
			return repo, p
		}
	}
	return repo, nil
}

func (s *Server) editPR(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, p := s.find(r)
	if p == nil {
		notFound(w)
		return
	}
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	if t, ok := in["title"].(string); ok {
		p.Title = t
	}
	if b, ok := in["body"].(string); ok {
		p.Body = b
	}
	reply(w, 200, s.restPR(repo, p))
}

func (s *Server) reviewers(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	repo, p := s.find(r)
	if p == nil {
		notFound(w)
		return
	}
	var in struct {
		Reviewers     []string `json:"reviewers"`
		TeamReviewers []string `json:"team_reviewers"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	owner, _, _ := strings.Cut(repo, "/")
	for _, t := range in.TeamReviewers {
		in.Reviewers = append(in.Reviewers, owner+"/"+t)
	}
	for _, u := range in.Reviewers {
		if !slices.Contains(p.Reviewers, u) {
			p.Reviewers = append(p.Reviewers, u)
		}
		s.ask(repo, p.Number, u)
	}
	reply(w, 201, s.restPR(repo, p))
}

func (s *Server) checkRuns(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	runs := []map[string]any{}
	for _, c := range s.checks[repoOf(r)+"@"+r.PathValue("ref")] {
		if c.ID == 0 && !c.Actions {
			continue // a commit status
		}
		run := map[string]any{"id": c.ID, "name": c.Name, "status": c.Status, "html_url": c.URL, "app": map[string]string{"slug": "other"},
			"check_suite": map[string]int64{"id": c.Suite}}
		if c.Conclusion != "" {
			run["conclusion"] = c.Conclusion
		}
		if c.Actions {
			run["app"] = map[string]string{"slug": "github-actions"}
		}
		if !c.Started.IsZero() {
			run["started_at"] = c.Started
		}
		if !c.Completed.IsZero() {
			run["completed_at"] = c.Completed
		}
		runs = append(runs, run)
	}
	reply(w, 200, map[string]any{"total_count": len(runs), "check_runs": runs})
}

// workflowRuns are the Actions workflow runs on a branch or commit: those of the suites its checks are in.
func (s *Server) workflowRuns(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ref := r.URL.Query().Get("branch") + r.URL.Query().Get("head_sha")
	runs := []map[string]int64{}
	seen := map[int64]bool{}
	for _, c := range s.checks[repoOf(r)+"@"+ref] {
		if wf, ok := s.flows[c.Suite]; ok && c.Actions && !seen[c.Suite] {
			seen[c.Suite] = true
			runs = append(runs, map[string]int64{"id": c.Suite, "workflow_id": wf, "check_suite_id": c.Suite})
		}
	}
	reply(w, 200, map[string]any{"total_count": len(runs), "workflow_runs": runs})
}

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sts := []map[string]any{}
	for _, c := range s.checks[repoOf(r)+"@"+r.PathValue("ref")] {
		if c.ID != 0 || c.Actions {
			continue
		}
		state := "pending"
		switch c.State() {
		case gh.CheckPass:
			state = "success"
		case gh.CheckFail:
			state = "failure"
		}
		sts = append(sts, map[string]any{"context": c.Name, "state": state, "target_url": c.URL, "created_at": c.Started, "updated_at": c.Completed})
	}
	reply(w, 200, map[string]any{"statuses": sts})
}

func (s *Server) log(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	l, ok := s.logs[id]
	if !ok {
		notFound(w)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(l))
}

func (s *Server) graphql(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if strings.Contains(in.Query, "mergeStateStatus") {
		s.mergeState(w, in.Variables)
		return
	}
	if strings.Contains(in.Query, "resolveReviewThread") {
		s.resolveThread(w, in.Query, in.Variables)
		return
	}
	if strings.Contains(in.Query, "reviewThreads") {
		s.reviewThreads(w, in.Variables)
		return
	}
	if strings.Contains(in.Query, "search(") {
		s.queue(w, in.Variables)
		return
	}
	if !strings.Contains(in.Query, "markPullRequestReadyForReview") {
		reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "the fake knows only markPullRequestReadyForReview, reviewThreads, (un)resolveReviewThread and search"}}})
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ps := range s.prs {
		for _, p := range ps {
			if p.NodeID == in.Variables["id"] {
				p.Draft = false
				reply(w, 200, map[string]any{"data": map[string]any{"markPullRequestReadyForReview": map[string]any{"pullRequest": map[string]bool{"isDraft": false}}}})
				return
			}
		}
	}
	reply(w, 200, map[string]any{"errors": []map[string]string{{"message": "Could not resolve to a node"}}})
}

// search is GitHub's issue search over the fake's pull requests (match).
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	items := []map[string]any{}
	for _, h := range s.match(r.URL.Query().Get("q")) {
		items = append(items, map[string]any{"number": h.p.Number, "title": h.p.Title, "html_url": h.p.URL, "state": h.p.State,
			"draft": h.p.Draft, "created_at": h.p.Created, "updated_at": h.p.Created, "user": map[string]string{"login": cmpOr(h.p.Author, s.login)},
			"repository_url": s.URL + "/repos/" + h.repo, "pull_request": map[string]any{"html_url": h.p.URL}})
	}
	reply(w, 200, map[string]any{"total_count": len(items), "items": items})
}
