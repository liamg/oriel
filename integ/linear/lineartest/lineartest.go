// Package lineartest is a recorded Linear API for tests: the GraphQL answers in integ/linear/testdata, served as
// api.linear.app over TLS, with mutations kept and applied to what's read next.
package lineartest

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
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
	Op   string         // issueUpdate or attachmentCreate
	Vars map[string]any // its variables
}

// Linear is the recorded API.
type Linear struct {
	*httptest.Server
	t *testing.T

	mu        sync.Mutex
	assigned  map[string]any
	issues    map[string]map[string]any // by identifier
	mutations []Mutation
	auths     []string // each request's host and Authorization
}

// New serves the recording until the test ends.
func New(t *testing.T) *Linear {
	t.Helper()
	isolate.Test(t)
	l := &Linear{t: t, issues: map[string]map[string]any{}}
	l.assigned = l.load("assigned.json")
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
	case strings.Contains(q, "attachmentCreate"):
		l.mutations = append(l.mutations, Mutation{Op: "attachmentCreate", Vars: in.Variables})
		json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"attachmentCreate": map[string]any{"success": true}}})
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

// update applies an issueUpdate's stateId to the issue wherever the recording has it; false for an issue or state
// it hasn't.
func (l *Linear) update(vars map[string]any) bool {
	id, _ := vars["id"].(string)
	input, _ := vars["input"].(map[string]any)
	stateID, _ := input["stateId"].(string)
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
		for _, s := range path(n, "team", "states")["nodes"].([]any) {
			st := s.(map[string]any)
			if st["id"] == stateID {
				n["state"] = map[string]any{"name": st["name"], "type": st["type"]}
				found = true
			}
		}
	}
	return found
}

func path(v map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		v, _ = v[k].(map[string]any)
	}
	return v
}
