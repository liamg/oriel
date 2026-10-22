// Package gitlabtest is a fake gitlab.com for tests: the REST calls the GitLab plugin makes, kept in memory behind an
// httptest TLS server, with what's raised, edited and merged applied to what's read next.
package gitlabtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin/hosttest"
)

// Key is the token the fake accepts.
const Key = "glpat-r3c0rd3d"

// Me is who the token is.
const Me = "you"

// Job is a pipeline job as seeded.
type Job struct {
	Name, Status string
	Trace        string
}

// MR is a merge request as seeded.
type MR struct {
	Title, Description, Author, Source, Target string
	Reviewers                                  []string
	SHA                                        string
}

// Merged is a merge the fake was asked for and made.
type Merged struct {
	Path         string
	IID          int
	SHA          string
	Squash       bool
	RemoveSource bool
}

type reviewer struct {
	user, state string
	asked       time.Time
}

type mr struct {
	id, iid                int
	title, desc            string
	author, source, target string
	state                  string
	sha                    string
	created                time.Time
	reviewers              []reviewer
	discussions            []*discussion
	jobs                   []Job
	conflicts              bool
	status                 string
	diffs                  []map[string]string
}

type note struct {
	id      int
	author  string
	body    string
	created time.Time
	system  bool
}

type discussion struct {
	id       string
	notes    []note
	path     string // "" for one not on the diff
	line     int
	resolved bool
}

type project struct {
	id                        int
	path                      string
	mergeMethod, squashOption string
	mrs                       []*mr
}

// GitLab is the fake.
type GitLab struct {
	*httptest.Server
	mu       sync.Mutex
	now      time.Time
	nextID   int
	users    []string
	projects map[string]*project
	merged   []Merged
	auths    []string
	hosts    []string
	members  map[string]int // by project path and user: access level
	notes    []string       // notes posted through the API: "path!iid discussion: body"
}

// New serves an empty gitlab.com until the test ends; its users are you, mkhan and priya-k.
func New(t testing.TB) *GitLab {
	t.Helper()
	isolate.Test(t)
	g := &GitLab{now: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC), nextID: 100, users: []string{Me, "mkhan", "priya-k"},
		projects: map[string]*project{}, hosts: []string{"gitlab.com"},
		members: map[string]int{}}
	g.Server = httptest.NewTLSServer(http.HandlerFunc(g.serve))
	t.Cleanup(g.Close)
	return g
}

// Client reaches the fake whatever host a request names, as the internet would reach gitlab.com.
func (g *GitLab) Client() *http.Client { return hosttest.Net(g.Server) }

// AddHost serves host too, as a self-hosted GitLab the same as gitlab.com.
func (g *GitLab) AddHost(host string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.hosts = append(g.hosts, host)
}

// AddProject adds a project: mergeMethod merge, rebase_merge or ff; squashOption never, always, default_on or
// default_off.
func (g *GitLab) AddProject(path, mergeMethod, squashOption string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.project(path).mergeMethod, g.project(path).squashOption = mergeMethod, squashOption
}

func (g *GitLab) project(path string) *project {
	p := g.projects[path]
	if p == nil {
		p = &project{id: len(g.projects) + 1, path: path, mergeMethod: "merge", squashOption: "default_off"}
		g.projects[path] = p
	}
	return p
}

func (g *GitLab) tick() time.Time {
	g.now = g.now.Add(time.Minute)
	return g.now
}

// AddMR opens a merge request and gives its iid.
func (g *GitLab) AddMR(path string, m MR) int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.add(path, m).iid
}

func (g *GitLab) add(path string, m MR) *mr {
	p := g.project(path)
	g.nextID++
	x := &mr{id: g.nextID, iid: len(p.mrs) + 1, title: m.Title, desc: m.Description, author: m.Author, source: m.Source, target: m.Target,
		state: "opened", sha: m.SHA, created: g.tick(), status: "mergeable"}
	if x.sha == "" {
		x.sha = fmt.Sprintf("%040d", x.id)
	}
	for _, r := range m.Reviewers {
		x.reviewers = append(x.reviewers, reviewer{user: r, state: "unreviewed", asked: g.tick()})
	}
	p.mrs = append(p.mrs, x)
	return x
}

func (g *GitLab) find(path string, iid int) *mr {
	if p := g.projects[path]; p != nil && iid > 0 && iid <= len(p.mrs) {
		return p.mrs[iid-1]
	}
	return nil
}

// SetReviewer sets where a reviewer is with a merge request: unreviewed, reviewed, requested_changes, approved.
func (g *GitLab) SetReviewer(path string, iid int, user, state string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.find(path, iid)
	for i := range m.reviewers {
		if m.reviewers[i].user == user {
			m.reviewers[i].state = state
			return
		}
	}
	m.reviewers = append(m.reviewers, reviewer{user: user, state: state, asked: g.tick()})
}

// AddDiscussion adds a resolvable thread on the diff to a merge request, by priya-k.
func (g *GitLab) AddDiscussion(path string, iid int, resolved bool) {
	d := g.AddThread(path, iid, Thread{Author: "priya-k", Body: "why?", Path: "README", Line: 1})
	if resolved {
		g.mu.Lock()
		g.find(path, iid).find(d).resolved = true
		g.mu.Unlock()
	}
}

// Thread is a discussion on a merge request's diff, as seeded: its first note.
type Thread struct {
	Author, Body, Path string
	Line               int
}

// AddThread starts a discussion on a merge request's diff, and gives its id.
func (g *GitLab) AddThread(path string, iid int, t Thread) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.find(path, iid)
	g.nextID++
	d := &discussion{id: fmt.Sprintf("d%040d", g.nextID), path: t.Path, line: t.Line,
		notes: []note{{id: g.nextID, author: t.Author, body: t.Body, created: g.tick()}}}
	m.discussions = append(m.discussions, d)
	return d.id
}

// AddNote adds a note to a merge request's general conversation.
func (g *GitLab) AddNote(path string, iid int, author, body string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.find(path, iid)
	g.nextID++
	m.discussions = append(m.discussions, &discussion{id: fmt.Sprintf("n%040d", g.nextID),
		notes: []note{{id: g.nextID, author: author, body: body, created: g.tick()}}})
}

// SetMember gives a user an access level to a project: 30 is Developer.
func (g *GitLab) SetMember(path, user string, level int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.members[path+"/"+user] = level
}

// Notes are the notes posted through the API, "path!iid discussion: body", discussion "" for the general
// conversation.
func (g *GitLab) Notes() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.notes)
}

// Discussion is a discussion's notes, "author: body".
func (g *GitLab) Discussion(path string, iid int, id string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []string
	if d := g.find(path, iid).find(id); d != nil {
		for _, n := range d.notes {
			out = append(out, n.author+": "+n.body)
		}
	}
	return out
}

func (m *mr) find(id string) *discussion {
	for _, d := range m.discussions {
		if d.id == id {
			return d
		}
	}
	return nil
}

// SetJobs sets the jobs of a merge request's head pipeline.
func (g *GitLab) SetJobs(path string, iid int, jobs ...Job) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.find(path, iid).jobs = jobs
}

// SetDiff sets a file a merge request changes, as a unified diff.
func (g *GitLab) SetDiff(path string, iid int, file, diff string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.find(path, iid)
	m.diffs = append(m.diffs, map[string]string{"new_path": file, "diff": diff})
}

// SetStatus sets what GitLab says of merging: its detailed_merge_status, and whether it conflicts.
func (g *GitLab) SetStatus(path string, iid int, status string, conflicts bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	m := g.find(path, iid)
	m.status, m.conflicts = status, conflicts
}

// Push moves a merge request's source branch on: its head is sha.
func (g *GitLab) Push(path string, iid int, sha string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.find(path, iid).sha = sha
}

// MR is a merge request as the API writes it, nil when there's none.
func (g *GitLab) MR(path string, iid int) map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	if m := g.find(path, iid); m != nil {
		return g.json(g.projects[path], m)
	}
	return nil
}

// MRs are a project's merge requests, as the API writes them.
func (g *GitLab) MRs(path string) []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	var out []map[string]any
	if p := g.projects[path]; p != nil {
		for _, m := range p.mrs {
			out = append(out, g.json(p, m))
		}
	}
	return out
}

// Merged are the merges made.
func (g *GitLab) Merged() []Merged {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.merged)
}

// Auths are the requests' hosts and Authorization headers, "host header".
func (g *GitLab) Auths() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.auths)
}

var draft = regexp.MustCompile(`(?i)^\s*(\[draft\]|draft:|\(draft\))`)

func (g *GitLab) user(name string) map[string]any {
	return map[string]any{"id": slices.Index(g.users, name) + 1, "username": name}
}

func (g *GitLab) json(p *project, m *mr) map[string]any {
	var rs []map[string]any
	for _, r := range m.reviewers {
		rs = append(rs, g.user(r.user))
	}
	out := map[string]any{"id": m.id, "iid": m.iid, "project_id": p.id, "title": m.title, "description": m.desc, "state": m.state,
		"draft": draft.MatchString(m.title), "web_url": fmt.Sprintf("https://gitlab.com/%s/-/merge_requests/%d", p.path, m.iid),
		"source_branch": m.source, "target_branch": m.target, "sha": m.sha, "author": g.user(m.author), "reviewers": rs,
		"labels": []string{}, "created_at": m.created.Format(time.RFC3339), "has_conflicts": m.conflicts, "detailed_merge_status": m.status,
		"references": map[string]string{"full": fmt.Sprintf("%s!%d", p.path, m.iid)}}
	if len(m.jobs) > 0 {
		out["head_pipeline"] = map[string]any{"id": m.id * 10, "status": "running"}
	}
	return out
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func (g *GitLab) serve(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.auths = append(g.auths, r.Host+" "+r.Header.Get("Authorization"))
	rest, ok := strings.CutPrefix(r.URL.EscapedPath(), "/api/v4/")
	if !slices.Contains(g.hosts, r.Host) || !ok {
		reply(w, 404, map[string]string{"message": "404 Not Found"})
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+Key {
		w.Header().Set("X-Gitlab-Meta", `{"correlation_id":"c0ffee","version":"1"}`)
		reply(w, 401, map[string]string{"message": "401 Unauthorized"})
		return
	}
	var in map[string]any
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		json.Unmarshal(b, &in)
	}
	seg := strings.Split(rest, "/")
	for i := range seg {
		seg[i], _ = url.PathUnescape(seg[i])
	}
	q := r.URL.Query()
	route := r.Method + " " + seg[0]
	switch {
	case route == "GET user":
		reply(w, 200, g.user(Me))
	case route == "GET users":
		var out []map[string]any
		if slices.Contains(g.users, q.Get("username")) {
			out = append(out, g.user(q.Get("username")))
		}
		reply(w, 200, out)
	case route == "GET merge_requests":
		out := []map[string]any{}
		for _, p := range g.sorted() {
			for _, m := range p.mrs {
				if m.state == "opened" && slices.ContainsFunc(m.reviewers, func(rv reviewer) bool { return rv.user == q.Get("reviewer_username") }) {
					out = append(out, g.json(p, m))
				}
			}
		}
		reply(w, 200, out)
	case seg[0] == "projects" && len(seg) >= 2:
		g.projectRoute(w, r.Method, seg[1], seg[2:], q, in)
	default:
		reply(w, 404, map[string]string{"message": "404 Not Found"})
	}
}

func (g *GitLab) sorted() []*project {
	var ps []*project
	for _, p := range g.projects {
		ps = append(ps, p)
	}
	slices.SortFunc(ps, func(a, b *project) int { return a.id - b.id })
	return ps
}

func (g *GitLab) projectRoute(w http.ResponseWriter, method, path string, seg []string, q url.Values, in map[string]any) {
	p := g.projects[path]
	if p == nil {
		reply(w, 404, map[string]string{"message": "404 Project Not Found"})
		return
	}
	num := func(i int) int {
		if i >= len(seg) {
			return 0
		}
		n, _ := strconv.Atoi(seg[i])
		return n
	}
	switch {
	case len(seg) == 0 && method == "GET":
		reply(w, 200, map[string]any{"id": p.id, "path_with_namespace": p.path, "merge_method": p.mergeMethod, "squash_option": p.squashOption})
	case len(seg) == 1 && seg[0] == "merge_requests" && method == "GET":
		out := []map[string]any{}
		for _, m := range p.mrs {
			if b := q.Get("source_branch"); b == "" || b == m.source {
				out = append(out, g.json(p, m))
			}
		}
		slices.Reverse(out)
		reply(w, 200, out)
	case len(seg) == 1 && seg[0] == "merge_requests" && method == "POST":
		for _, m := range p.mrs {
			if m.state == "opened" && m.source == str(in["source_branch"]) {
				reply(w, 409, map[string]any{"message": []string{"Another open merge request already exists for this source branch"}})
				return
			}
		}
		m := g.add(path, MR{Title: str(in["title"]), Description: str(in["description"]), Author: Me, Source: str(in["source_branch"]),
			Target: str(in["target_branch"])})
		reply(w, 201, g.json(p, m))
	case len(seg) >= 2 && seg[0] == "merge_requests":
		m := g.find(path, num(1))
		if m == nil {
			reply(w, 404, map[string]string{"message": "404 Not found"})
			return
		}
		g.mrRoute(w, method, p, m, seg[2:], in)
	case len(seg) == 3 && seg[0] == "pipelines" && seg[2] == "jobs":
		for _, m := range p.mrs {
			if m.id*10 == num(1) {
				out := []map[string]any{}
				for i, j := range m.jobs {
					started := m.created.Format(time.RFC3339)
					out = append(out, map[string]any{"id": m.id*100 + i, "name": j.Name, "status": j.Status, "started_at": started,
						"web_url": fmt.Sprintf("https://gitlab.com/%s/-/jobs/%d", p.path, m.id*100+i)})
				}
				reply(w, 200, out)
				return
			}
		}
		reply(w, 404, map[string]string{"message": "404 Not found"})
	case len(seg) == 3 && seg[0] == "members" && seg[1] == "all":
		id := num(2)
		if id < 1 || id > len(g.users) || g.members[path+"/"+g.users[id-1]] == 0 {
			reply(w, 404, map[string]string{"message": "404 Not found"})
			return
		}
		reply(w, 200, map[string]any{"id": id, "username": g.users[id-1], "access_level": g.members[path+"/"+g.users[id-1]]})
	case len(seg) == 3 && seg[0] == "jobs" && seg[2] == "trace":
		for _, m := range p.mrs {
			for i, j := range m.jobs {
				if m.id*100+i == num(1) {
					w.Header().Set("Content-Type", "text/plain")
					w.Write([]byte(j.Trace))
					return
				}
			}
		}
		reply(w, 404, map[string]string{"message": "404 Not found"})
	default:
		reply(w, 404, map[string]string{"message": "404 Not Found"})
	}
}

func (g *GitLab) mrRoute(w http.ResponseWriter, method string, p *project, m *mr, seg []string, in map[string]any) {
	switch {
	case len(seg) == 0 && method == "GET":
		reply(w, 200, g.json(p, m))
	case len(seg) == 0 && method == "PUT":
		if t, ok := in["title"].(string); ok {
			m.title = t
		}
		if d, ok := in["description"].(string); ok {
			m.desc = d
		}
		if ids, ok := in["reviewer_ids"].([]any); ok {
			var rs []reviewer
			for _, id := range ids {
				name := g.users[int(id.(float64))-1]
				i := slices.IndexFunc(m.reviewers, func(r reviewer) bool { return r.user == name })
				if i >= 0 {
					rs = append(rs, m.reviewers[i])
				} else {
					rs = append(rs, reviewer{user: name, state: "unreviewed", asked: g.tick()})
				}
			}
			m.reviewers = rs
		}
		reply(w, 200, g.json(p, m))
	case len(seg) == 1 && seg[0] == "reviewers":
		out := []map[string]any{}
		for _, r := range m.reviewers {
			out = append(out, map[string]any{"user": g.user(r.user), "state": r.state, "created_at": r.asked.Format(time.RFC3339)})
		}
		reply(w, 200, out)
	case len(seg) == 1 && seg[0] == "diffs":
		out := []map[string]string{}
		reply(w, 200, append(out, m.diffs...))
	case len(seg) == 1 && seg[0] == "discussions":
		out := []map[string]any{}
		for _, d := range m.discussions {
			var ns []map[string]any
			for _, n := range d.notes {
				j := map[string]any{"id": n.id, "body": n.body, "author": g.user(n.author), "created_at": n.created.Format(time.RFC3339),
					"system": n.system, "resolvable": d.path != "", "resolved": d.resolved}
				if d.path != "" {
					j["position"] = map[string]any{"new_path": d.path, "old_path": d.path, "new_line": d.line}
				}
				ns = append(ns, j)
			}
			out = append(out, map[string]any{"id": d.id, "individual_note": d.path == "", "notes": ns})
		}
		reply(w, 200, out)
	case len(seg) == 1 && seg[0] == "notes" && method == "POST":
		g.nextID++
		n := note{id: g.nextID, author: Me, body: str(in["body"]), created: g.tick()}
		m.discussions = append(m.discussions, &discussion{id: fmt.Sprintf("n%040d", n.id), notes: []note{n}})
		g.notes = append(g.notes, fmt.Sprintf("%s!%d : %s", p.path, m.iid, n.body))
		reply(w, 201, map[string]any{"id": n.id, "body": n.body, "author": g.user(Me)})
	case len(seg) == 3 && seg[0] == "discussions" && seg[2] == "notes" && method == "POST":
		d := m.find(seg[1])
		if d == nil {
			reply(w, 404, map[string]string{"message": "404 Discussion Not Found"})
			return
		}
		g.nextID++
		n := note{id: g.nextID, author: Me, body: str(in["body"]), created: g.tick()}
		d.notes = append(d.notes, n)
		g.notes = append(g.notes, fmt.Sprintf("%s!%d %s: %s", p.path, m.iid, d.id, n.body))
		reply(w, 201, map[string]any{"id": n.id, "body": n.body, "author": g.user(Me)})
	case len(seg) == 1 && seg[0] == "merge" && method == "PUT":
		switch {
		case m.state != "opened" || draft.MatchString(m.title):
			reply(w, 405, map[string]string{"message": "405 Method Not Allowed"})
		case m.conflicts:
			reply(w, 406, map[string]string{"message": "Branch cannot be merged"})
		case str(in["sha"]) != "" && str(in["sha"]) != m.sha:
			reply(w, 409, map[string]string{"message": "SHA does not match HEAD of source branch: " + m.sha})
		default:
			m.state = "merged"
			squash, _ := in["squash"].(bool)
			remove, _ := in["should_remove_source_branch"].(bool)
			g.merged = append(g.merged, Merged{Path: p.path, IID: m.iid, SHA: m.sha, Squash: squash, RemoveSource: remove})
			reply(w, 200, g.json(p, m))
		}
	default:
		reply(w, 404, map[string]string{"message": "404 Not Found"})
	}
}

func str(v any) string { s, _ := v.(string); return s }
