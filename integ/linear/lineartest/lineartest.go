// Package lineartest is a recorded Linear API for tests: the GraphQL answers in integ/linear/testdata, served as
// api.linear.app over TLS, with mutations kept and applied to what's read next.
package lineartest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin/hosttest"
)

// Key is the API key the recording accepts.
const Key = "lin_api_r3c0rd3d"

// Mutation is a change sent to Linear.
type Mutation struct {
	Op   string         // issueUpdate, issueCreate, commentCreate or attachmentCreate
	Vars map[string]any // its variables
}

// Linear is the recorded API.
type Linear struct {
	*httptest.Server
	t *testing.T

	mu        sync.Mutex
	assigned  map[string]any
	teams     map[string]any
	users     map[string]any
	issues    map[string]map[string]any // by identifier
	created   int
	mutations []Mutation
	auths     []string // each request's host and Authorization
}

// New serves the recording until the test ends.
func New(t *testing.T) *Linear {
	t.Helper()
	isolate.Test(t)
	l := &Linear{t: t, issues: map[string]map[string]any{}}
	l.assigned = l.load("assigned.json")
	l.teams, l.users = l.load("teams.json"), l.load("users.json")
	matches, _ := filepath.Glob(filepath.Join(dir(), "issue-*.json"))
	for _, m := range matches {
		key := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(m), "issue-"), ".json")
		l.issues[key] = l.load(filepath.Base(m))
	}
	l.Server = httptest.NewTLSServer(http.HandlerFunc(l.serve))
	t.Cleanup(l.Close)
	return l
}

func dir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "testdata")
}

func (l *Linear) load(name string) map[string]any {
	b, err := os.ReadFile(filepath.Join(dir(), name))
	if err != nil {
		l.t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		l.t.Fatal(err)
	}
	return v
}

// Client reaches the recording whatever host a request names, as the internet would reach Linear.
func (l *Linear) Client() *http.Client { return hosttest.Net(l.Server) }

// Mutations are the changes sent so far.
func (l *Linear) Mutations() []Mutation {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Mutation(nil), l.mutations...)
}

// Auths are the requests' hosts and Authorization headers, "host key".
func (l *Linear) Auths() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.auths...)
}

func (l *Linear) serve(w http.ResponseWriter, r *http.Request) {
	l.mu.Lock()
	l.auths = append(l.auths, r.Host+" "+r.Header.Get("Authorization"))
	l.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if r.Host != "api.linear.app" || r.URL.Path != "/graphql" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	if r.Header.Get("Authorization") != Key {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"message":"Authentication required, not authenticated","extensions":{"code":"AUTHENTICATION_ERROR"}}]}`))
		return
	}
	var in struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	body, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(body, &in); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"message":"Must provide query string."}]}`))
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	q := in.Query
	switch {
	case strings.Contains(q, "issueUpdate"):
		l.mutations = append(l.mutations, Mutation{Op: "issueUpdate", Vars: in.Variables})
		ok := l.update(in.Variables)
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"issueUpdate": map[string]any{"success": ok}}})
	case strings.Contains(q, "issueCreate"):
		l.mutations = append(l.mutations, Mutation{Op: "issueCreate", Vars: in.Variables})
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"issueCreate": l.create(in.Variables)}})
	case strings.Contains(q, "commentCreate"):
		l.mutations = append(l.mutations, Mutation{Op: "commentCreate", Vars: in.Variables})
		input, _ := in.Variables["input"].(map[string]any)
		n := l.issue(fmt.Sprint(input["issueId"]))
		if n == nil {
			w.Write([]byte(`{"data":null,"errors":[{"message":"Entity not found: Issue","extensions":{"code":"INPUT_ERROR"}}]}`))
			return
		}
		url := fmt.Sprint(n["url"]) + "#comment-" + strconv.Itoa(len(l.mutations))
		viewer := l.user(fmt.Sprint(path(l.teams, "data", "viewer")["id"]))
		comments := path(n, "comments")
		comments["nodes"] = append(comments["nodes"].([]any), map[string]any{"body": input["body"], "createdAt": "2026-11-05T12:00:00.000Z",
			"user": map[string]any{"name": viewer["name"], "displayName": viewer["displayName"]}})
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"commentCreate": map[string]any{"success": true, "comment": map[string]any{"url": url}}}})
	case strings.Contains(q, "attachmentCreate"):
		l.mutations = append(l.mutations, Mutation{Op: "attachmentCreate", Vars: in.Variables})
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"attachmentCreate": map[string]any{"success": true}}})
	case strings.Contains(q, "teams"):
		json.NewEncoder(w).Encode(l.teams)
	case strings.Contains(q, "users("):
		json.NewEncoder(w).Encode(l.matchUsers(in.Variables))
	case strings.Contains(q, "assignedIssues"):
		json.NewEncoder(w).Encode(l.assigned)
	case strings.Contains(q, "issue(id:"):
		key, _ := in.Variables["id"].(string)
		is, ok := l.issues[key]
		if !ok {
			w.Write([]byte(`{"data":null,"errors":[{"message":"Entity not found: Issue","extensions":{"code":"INPUT_ERROR"}}]}`))
			return
		}
		json.NewEncoder(w).Encode(is)
	default:
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"errors":[{"message":"not in the recording"}]}`))
	}
}

// update applies an issueUpdate to the issue wherever the recording has it: its title, description, priority,
// assignee and state; false for an issue, state or person it hasn't.
func (l *Linear) update(vars map[string]any) bool {
	id, _ := vars["id"].(string)
	input, _ := vars["input"].(map[string]any)
	var nodes []map[string]any
	for _, is := range l.issues {
		nodes = append(nodes, path(is, "data", "issue"))
	}
	for _, n := range path(l.assigned, "data", "viewer", "assignedIssues")["nodes"].([]any) {
		nodes = append(nodes, n.(map[string]any))
	}
	found := false
	for _, n := range nodes {
		if n["id"] != id {
			continue
		}
		if !l.apply(n, input) {
			return false
		}
		found = true
	}
	return found
}

// apply sets an issue's fields from an IssueUpdateInput or IssueCreateInput; false if it names a state or person
// the recording hasn't.
func (l *Linear) apply(n, input map[string]any) bool {
	for _, k := range []string{"title", "description"} {
		if v, ok := input[k]; ok {
			n[k] = v
		}
	}
	if p, ok := input["priority"].(float64); ok {
		n["priority"] = p
		n["priorityLabel"] = []string{"No priority", "Urgent", "High", "Medium", "Low"}[int(p)]
	}
	if v, ok := input["assigneeId"]; ok {
		if v == nil {
			n["assignee"] = nil
		} else if u := l.user(fmt.Sprint(v)); u != nil {
			n["assignee"] = map[string]any{"id": u["id"], "name": u["name"]}
		} else {
			return false
		}
	}
	if stateID, ok := input["stateId"].(string); ok {
		found := false
		for _, s := range path(n, "team", "states")["nodes"].([]any) {
			st := s.(map[string]any)
			if st["id"] == stateID {
				n["state"] = map[string]any{"name": st["name"], "type": st["type"]}
				found = true
			}
		}
		return found
	}
	return true
}

// issue is the issue with this identifier or id, as the recording reads it.
func (l *Linear) issue(idOrKey string) map[string]any {
	if is, ok := l.issues[idOrKey]; ok {
		return path(is, "data", "issue")
	}
	for _, is := range l.issues {
		if n := path(is, "data", "issue"); n["id"] == idOrKey {
			return n
		}
	}
	return nil
}

func (l *Linear) user(id string) map[string]any {
	for _, u := range path(l.users, "data", "users")["nodes"].([]any) {
		if um := u.(map[string]any); um["id"] == id {
			return um
		}
	}
	return nil
}

// matchUsers answers users(filter: {or: [{name: {eqIgnoreCase}}, …]}) from the recorded users.
func (l *Linear) matchUsers(vars map[string]any) map[string]any {
	filter, _ := vars["filter"].(map[string]any)
	or, _ := filter["or"].([]any)
	matched := []any{}
users:
	for _, u := range path(l.users, "data", "users")["nodes"].([]any) {
		um := u.(map[string]any)
		for _, f := range or {
			for field, cmp := range f.(map[string]any) {
				want, _ := cmp.(map[string]any)["eqIgnoreCase"].(string)
				if got, _ := um[field].(string); want != "" && strings.EqualFold(got, want) {
					matched = append(matched, um)
					continue users
				}
			}
		}
	}
	return map[string]any{"data": map[string]any{"users": map[string]any{"nodes": matched}}}
}

// create files an IssueCreateInput as the team's next issue, readable by its key from then on.
func (l *Linear) create(vars map[string]any) map[string]any {
	input, _ := vars["input"].(map[string]any)
	var team map[string]any
	for _, t := range path(l.teams, "data", "teams")["nodes"].([]any) {
		if tm := t.(map[string]any); tm["id"] == input["teamId"] {
			team = tm
		}
	}
	if team == nil {
		return map[string]any{"success": false}
	}
	l.created++
	key := fmt.Sprintf("%s-%d", team["key"], 3000+l.created)
	states := team["states"].(map[string]any)
	n := map[string]any{"id": fmt.Sprintf("c0ffee00-0000-4000-8000-%012d", l.created), "identifier": key,
		"url": "https://linear.app/acme/issue/" + key, "description": "", "priorityLabel": "No priority",
		"state": map[string]any{"name": "Todo", "type": "unstarted"}, "project": nil, "assignee": nil,
		"comments": map[string]any{"nodes": []any{}}, "attachments": map[string]any{"nodes": []any{}},
		"team": map[string]any{"states": states}}
	if !l.apply(n, input) {
		return map[string]any{"success": false}
	}
	l.issues[key] = map[string]any{"data": map[string]any{"viewer": path(l.teams, "data", "viewer"), "issue": n}}
	return map[string]any{"success": true, "issue": map[string]any{"identifier": key, "url": n["url"]}}
}

func path(v map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, _ = v[k].(map[string]any)
	}
	return v
}
