// Package azuretest is a fake Azure DevOps for tests: the REST calls the plugin's process makes, kept in memory and
// served as dev.azure.com and vssps.dev.azure.com over TLS.
package azuretest

import (
	"encoding/base64"
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

	"github.com/liamg/oriel/integ/azure"
	"github.com/liamg/oriel/plugin/hosttest"
)

// Token is the personal access token the fake accepts.
const Token = "azdo-r3c0rd3d"

// ServerHost is the fake's Azure DevOps Server, whose collection is DefaultCollection: a pull request in "org"
// DefaultCollection is on it, any other on dev.azure.com.
const ServerHost = "tfs.acme.corp"

// Cloud and OnPrem are the fake's two sites, as Oriel hands them to the plugin.
var (
	Cloud  = azure.Site{Host: "dev.azure.com", API: "https://dev.azure.com", Secret: azure.Secret}
	OnPrem = azure.Site{Host: ServerHost, API: "https://" + ServerHost + "/DefaultCollection", Secret: azure.Secret + ":" + ServerHost}
)

// Me is who the token is.
var Me = azure.Identity{ID: "6f3c2a10-0000-4000-8000-000000000001", DisplayName: "You", UniqueName: "you@acme.com"}

// Server is the fake. Its methods seed it and read back what was done to it.
type Server struct {
	*httptest.Server

	mu         sync.Mutex
	now        time.Time
	nextID     int
	repos      map[string]string // org/project/name → id
	prs        []*pr
	identities map[string]azure.Identity // by unique name
	policies   map[string][]string       // org/project → the merge types allowed
	auths      []string                  // each request's host and Authorization
}

type pr struct {
	azure.PullRequest
	org      string
	threads  []azure.Thread
	statuses []azure.Status
	changes  []string
	strategy string // how it was completed
	deleted  bool   // its source branch, on completing
}

// New serves the fake until the test ends.
func New(t testing.TB) *Server {
	s := &Server{now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), nextID: 57, repos: map[string]string{},
		identities: map[string]azure.Identity{Me.UniqueName: Me}, policies: map[string][]string{}}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

// Client reaches the fake whatever host a request names, as the internet would reach Azure DevOps.
func (s *Server) Client() *http.Client { return hosttest.Net(s.Server) }

// Auths are the requests' hosts and Authorization headers, "host header".
func (s *Server) Auths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.auths)
}

// AddIdentity makes someone, or a team, findable by unique name.
func (s *Server) AddIdentity(id azure.Identity) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.identities[id.UniqueName] = id
}

// AllowMerges sets the merge types a project's policy allows: merge, squash, rebase.
func (s *Server) AllowMerges(org, project string, types ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.policies[org+"/"+project] = types
}

func (s *Server) repoID(org, project, name string) string {
	k := org + "/" + project + "/" + name
	if s.repos[k] == "" {
		s.repos[k] = fmt.Sprintf("a2f1c3d4-0000-4000-8000-%012d", len(s.repos)+1)
	}
	return s.repos[k]
}

func (s *Server) tick() string {
	s.now = s.now.Add(time.Minute)
	return s.now.Format(time.RFC3339)
}

// AddPR puts a pull request in a repository, numbered next; its head commit is made up from its number.
func (s *Server) AddPR(org, project, repo string, p azure.PullRequest) azure.PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.add(org, project, repo, p).PullRequest
}

func (s *Server) add(org, project, repo string, p azure.PullRequest) *pr {
	p.ID = s.nextID
	s.nextID++
	if p.Status == "" {
		p.Status = "active"
	}
	if p.MergeStatus == "" {
		p.MergeStatus = "succeeded"
	}
	if p.LastSource.CommitID == "" {
		p.LastSource.CommitID = fmt.Sprintf("%040x", p.ID)
	}
	p.Created = s.tick()
	p.Repository.ID, p.Repository.Name, p.Repository.Project.Name = s.repoID(org, project, repo), repo, project
	p.Repository.RemoteURL = Web(org, project, repo)
	x := &pr{PullRequest: p, org: org}
	s.prs = append(s.prs, x)
	return x
}

// Web is a repository's page: on the server for the collection DefaultCollection, else on dev.azure.com.
func Web(org, project, repo string) string {
	if org == "DefaultCollection" {
		return azure.Repo{Site: OnPrem, Project: project, Name: repo}.Web()
	}
	return azure.Repo{Site: Cloud, Org: org, Project: project, Name: repo}.Web()
}

// PRs are a repository's pull requests, oldest first.
func (s *Server) PRs(org, project, repo string) []azure.PullRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []azure.PullRequest
	for _, p := range s.prs {
		if p.org == org && p.Repository.Project.Name == project && p.Repository.Name == repo {
			out = append(out, p.PullRequest)
		}
	}
	return out
}

// Completed is how a pull request was completed through the API ("" if it wasn't), and whether its branch went too.
func (s *Server) Completed(id int) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.find(id); p != nil {
		return p.strategy, p.deleted
	}
	return "", false
}

// AddThread adds a comment thread: status active, fixed…; system for one Azure DevOps wrote itself.
func (s *Server) AddThread(id int, status string, system bool) {
	kind := "text"
	if system {
		kind = "system"
	}
	s.thread(id, status, nil, azure.Comment{Author: Me, Content: "A " + kind + " comment", CommentType: kind})
}

// AddFileThread starts a thread on a line of a file, its first comment by who, and answers its id.
func (s *Server) AddFileThread(id int, path string, line int, who azure.Identity, body string) int {
	return s.thread(id, "active", &azure.ThreadContext{FilePath: "/" + path, Right: &azure.Position{Line: line, Offset: 1}},
		azure.Comment{Author: who, Content: body, CommentType: "text"})
}

// AddConversation starts a thread in the conversation, with no file, and answers its id.
func (s *Server) AddConversation(id int, who azure.Identity, body string) int {
	return s.thread(id, "active", nil, azure.Comment{Author: who, Content: body, CommentType: "text"})
}

func (s *Server) thread(id int, status string, where *azure.ThreadContext, first azure.Comment) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(id)
	first.ID, first.Published = 1, s.tick()
	t := azure.Thread{ID: len(p.threads) + 1, Status: status, Context: where, Comments: []azure.Comment{first}}
	p.threads = append(p.threads, t)
	return t.ID
}

// Resolve sets a thread's status: fixed, closed, active…
func (s *Server) Resolve(id, thread int, status string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.find(id).threads[thread-1].Status = status
}

// Threads are a pull request's threads, with what's been posted in them.
func (s *Server) Threads(id int) []azure.Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.find(id).threads)
}

// AddStatus posts a status on a pull request: state succeeded, failed, pending…
func (s *Server) AddStatus(id int, genre, name, state, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(id)
	st := azure.Status{ID: len(p.statuses) + 1, State: state, Description: description, Created: s.tick()}
	st.Updated = st.Created
	st.Context.Name, st.Context.Genre = name, genre
	p.statuses = append(p.statuses, st)
}

// SetChanges sets the files a pull request's latest iteration changes.
func (s *Server) SetChanges(id int, paths ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.find(id).changes = paths
}

// Vote sets a reviewer's vote, asking them first if they weren't.
func (s *Server) Vote(id int, who azure.Identity, vote int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.find(id)
	for i, r := range p.Reviewers {
		if r.ID == who.ID {
			p.Reviewers[i].Vote = vote
			return
		}
	}
	p.Reviewers = append(p.Reviewers, azure.Reviewer{Identity: who, Vote: vote})
}

// Update changes a pull request as someone on Azure DevOps would.
func (s *Server) Update(id int, fn func(*azure.PullRequest)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.find(id).PullRequest)
}

func (s *Server) find(id int) *pr {
	for _, p := range s.prs {
		if p.ID == id {
			return p
		}
	}
	return nil
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	reply(w, status, map[string]any{"message": msg, "typeKey": "InvalidArgumentValueException"})
}

func list[T any](v []T) map[string]any {
	if v == nil {
		v = []T{}
	}
	return map[string]any{"count": len(v), "value": v}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.auths = append(s.auths, r.Host+" "+r.Header.Get("Authorization"))
	if r.Header.Get("Authorization") == "" && r.URL.Path == "/_apis/connectionData" {
		// What a probe of an unnamed host meets: a server says what it is even as it turns you away.
		if r.Host == ServerHost {
			w.Header().Set("X-TFS-ProcessId", "8a1b6f2e-0000-4000-8000-000000000001")
			w.Header().Set("X-VSS-E2EID", "1")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+Token)) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if r.URL.Query().Get("api-version") == "" {
		fail(w, 400, "No api-version was supplied for the request.")
		return
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	q := r.URL.Query()
	at := func(i int) string {
		if i < len(parts) {
			return parts[i]
		}
		return ""
	}
	switch {
	case (r.Host == "vssps.dev.azure.com" || r.Host == ServerHost) && at(1) == "_apis" && at(2) == "identities":
		if id, ok := s.identities[q.Get("filterValue")]; ok {
			reply(w, 200, list([]map[string]string{{"id": id.ID, "providerDisplayName": id.DisplayName}}))
			return
		}
		reply(w, 200, list([]map[string]string{}))
	case r.Host != "dev.azure.com" && r.Host != ServerHost, (r.Host == ServerHost) != (at(0) == "DefaultCollection"):
		http.NotFound(w, r)
	case at(1) == "_apis" && at(2) == "connectionData":
		reply(w, 200, map[string]any{"authenticatedUser": Me})
	case at(1) == "_apis" && at(2) == "git" && at(3) == "pullrequests":
		var out []azure.PullRequest
		for _, p := range s.prs {
			if p.org == at(0) && p.Status == "active" && s.asks(p, q.Get("searchCriteria.reviewerId")) {
				out = append(out, p.PullRequest)
			}
		}
		reply(w, 200, list(out))
	case at(2) == "_apis" && at(3) == "policy" && at(4) == "configurations":
		var out []map[string]any
		if types, ok := s.policies[at(0)+"/"+at(1)]; ok {
			out = append(out, map[string]any{"isEnabled": true, "type": map[string]string{"id": "fa4e907d-c16b-4a4c-9dfa-4916e5d171ab"},
				"settings": map[string]any{"allowNoFastForward": slices.Contains(types, "merge"), "allowSquash": slices.Contains(types, "squash"),
					"allowRebase": slices.Contains(types, "rebase"), "scope": []map[string]any{{"repositoryId": nil}}}})
		}
		reply(w, 200, list(out))
	case at(2) == "_apis" && at(3) == "git" && at(4) == "repositories" && strings.EqualFold(at(6), "pullrequests"):
		s.pullRequests(w, r, parts)
	default:
		http.NotFound(w, r)
	}
}

// asks says the pull request asks reviewer, directly or through a team they're in (every team, in the fake).
func (s *Server) asks(p *pr, reviewer string) bool {
	for _, r := range p.Reviewers {
		if r.ID == reviewer || r.IsContainer {
			return true
		}
	}
	return false
}

// pullRequests is a repository's pull requests: listed, raised, read, updated and completed.
func (s *Server) pullRequests(w http.ResponseWriter, r *http.Request, parts []string) {
	org, project, repo := parts[0], parts[1], parts[5]
	if len(parts) == 7 {
		switch r.Method {
		case "GET":
			src := r.URL.Query().Get("searchCriteria.sourceRefName")
			var out []azure.PullRequest
			for _, p := range s.prs {
				if p.org == org && p.Repository.Project.Name == project && p.Repository.Name == repo && (src == "" || p.Source == src) {
					out = append(out, p.PullRequest)
				}
			}
			reply(w, 200, list(out))
		case "POST":
			var in azure.PullRequest
			json.NewDecoder(r.Body).Decode(&in)
			if in.Title == "" || in.Source == "" || in.Target == "" {
				fail(w, 400, "A title, source and target are required.")
				return
			}
			in.CreatedBy = Me
			reply(w, 201, s.add(org, project, repo, in).PullRequest)
		}
		return
	}
	id, _ := strconv.Atoi(parts[7])
	p := s.find(id)
	if p == nil || p.org != org || p.Repository.Name != repo {
		fail(w, 404, fmt.Sprintf("TF401180: The requested pull request was not found: %d", id))
		return
	}
	switch {
	case len(parts) == 8 && r.Method == "GET":
		reply(w, 200, p.PullRequest)
	case len(parts) == 8 && r.Method == "PATCH":
		s.update(w, r, p)
	case len(parts) == 10 && parts[8] == "reviewers" && r.Method == "PUT":
		for _, id := range s.identities {
			if id.ID == parts[9] {
				p.Reviewers = append(p.Reviewers, azure.Reviewer{Identity: id})
				reply(w, 200, id)
				return
			}
		}
		fail(w, 400, "The identity was not found.")
	case len(parts) == 9 && parts[8] == "statuses":
		reply(w, 200, list(p.statuses))
	case len(parts) == 9 && parts[8] == "threads" && r.Method == "GET":
		reply(w, 200, list(p.threads))
	case len(parts) == 9 && parts[8] == "threads" && r.Method == "POST":
		var in azure.Thread
		json.NewDecoder(r.Body).Decode(&in)
		if len(in.Comments) == 0 || in.Comments[0].Content == "" {
			fail(w, 400, "A thread needs a comment.")
			return
		}
		in.ID, in.Status = len(p.threads)+1, "active"
		in.Comments = in.Comments[:1]
		in.Comments[0].ID, in.Comments[0].Author, in.Comments[0].Published = 1, Me, s.tick()
		p.threads = append(p.threads, in)
		reply(w, 200, in)
	case len(parts) == 11 && parts[8] == "threads" && parts[10] == "comments" && r.Method == "POST":
		tid, _ := strconv.Atoi(parts[9])
		if tid < 1 || tid > len(p.threads) {
			fail(w, 404, "TF401181: The pull request thread was not found.")
			return
		}
		var in azure.Comment
		json.NewDecoder(r.Body).Decode(&in)
		if in.Content == "" {
			fail(w, 400, "A comment needs content.")
			return
		}
		t := &p.threads[tid-1]
		in.ID, in.Author, in.Published, in.CommentType = len(t.Comments)+1, Me, s.tick(), "text"
		t.Comments = append(t.Comments, in)
		reply(w, 200, in)
	case len(parts) == 9 && parts[8] == "iterations":
		reply(w, 200, list([]map[string]int{{"id": 1}}))
	case len(parts) == 11 && parts[8] == "iterations" && parts[10] == "changes":
		var entries []map[string]any
		for _, path := range p.changes {
			entries = append(entries, map[string]any{"changeType": "edit", "item": map[string]string{"path": "/" + path}})
		}
		reply(w, 200, map[string]any{"changeEntries": entries})
	default:
		http.NotFound(w, r)
	}
}

// update is a PATCH: a title, body or draft flag, or completing it, which Azure DevOps refuses for a draft, a
// conflict, an unknown strategy or a source that has moved on.
func (s *Server) update(w http.ResponseWriter, r *http.Request, p *pr) {
	var in map[string]json.RawMessage
	json.NewDecoder(r.Body).Decode(&in)
	str := func(k string) (string, bool) {
		var v string
		raw, ok := in[k]
		return v, ok && json.Unmarshal(raw, &v) == nil
	}
	if v, ok := str("title"); ok {
		p.Title = v
	}
	if v, ok := str("description"); ok {
		p.Description = v
	}
	if raw, ok := in["isDraft"]; ok {
		json.Unmarshal(raw, &p.IsDraft)
	}
	if v, _ := str("status"); v == "completed" {
		var last struct {
			CommitID string `json:"commitId"`
		}
		var opts struct {
			Strategy string `json:"mergeStrategy"`
			Delete   bool   `json:"deleteSourceBranch"`
		}
		json.Unmarshal(in["lastMergeSourceCommit"], &last)
		json.Unmarshal(in["completionOptions"], &opts)
		switch {
		case p.Status != "active":
			fail(w, 409, "The pull request isn't active.")
			return
		case p.IsDraft:
			fail(w, 400, "TF401406: Draft pull requests cannot be completed.")
			return
		case p.MergeStatus == "conflicts":
			fail(w, 409, "TF401179: The pull request has merge conflicts.")
			return
		case last.CommitID != p.LastSource.CommitID:
			fail(w, 409, "TF401192: The source branch has been updated since the pull request was last viewed.")
			return
		case !slices.Contains([]string{"noFastForward", "squash", "rebase", "rebaseMerge"}, opts.Strategy):
			fail(w, 400, "The merge strategy is not valid.")
			return
		}
		p.Status, p.strategy, p.deleted = "completed", opts.Strategy, opts.Delete
	}
	reply(w, 200, p.PullRequest)
}
