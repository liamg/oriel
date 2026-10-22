// Package gitlab is the GitLab plugin's process: GitLab as a code host, gitlab.com or a self-hosted site,
// its merge requests asking for your review, and a project's merge request raised, read and merged, over its REST API.
// It never holds a token: each request goes through the host's http.request, which adds the site's only for its host.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// Site is one GitLab server: its host, its REST API's root, and the secret holding the user's token for it.
type Site struct {
	Host   string `json:"host"`
	API    string `json:"api"`
	Secret string `json:"secret"`
}

// Secret is gitlab.com's secret: a personal access token, sent as a bearer token.
const Secret = "GITLAB_TOKEN"

// Public is gitlab.com.
var Public = Site{Host: "gitlab.com", API: "https://gitlab.com/api/v4", Secret: Secret}

// Do sends one request to the site's API+path with its token, and gives back the response's status and body.
type Do func(ctx context.Context, method, path string, body []byte) (status int, resp []byte, err error)

// Client talks to one GitLab site as the token's owner.
type Client struct {
	Do      Do
	Host    string   // the site's: for its web URLs
	Members *Members // who's a member of which project, as read; nil reads each time
}

// Members caches whether users have at least Developer access to projects, by project and user id.
type Members struct {
	mu sync.Mutex
	m  map[string]bool
}

// Error is GitLab saying no.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("GitLab: %s (HTTP %d)", e.Message, e.Status) }

// call sends in as JSON (nil for none) and decodes the answer into out (nil to drop it).
func (c Client) call(ctx context.Context, method, path string, in, out any) error {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = b
	}
	status, resp, err := c.Do(ctx, method, path, body)
	if err != nil {
		return err
	}
	if status >= 400 {
		var e struct {
			Message any    `json:"message"`
			Error   string `json:"error"`
		}
		json.Unmarshal(resp, &e)
		msg := e.Error
		if e.Message != nil {
			msg = fmt.Sprint(e.Message)
		}
		if status == 401 {
			msg = "the token was refused"
		}
		return &Error{Status: status, Message: strings.TrimSpace(msg)}
	}
	if out == nil || len(resp) == 0 {
		return nil
	}
	if s, ok := out.(*string); ok {
		*s = string(resp)
		return nil
	}
	return json.Unmarshal(resp, out)
}

// project is a project's path as the API takes it in a URL: group%2Fsub%2Fname.
func project(path string) string { return url.PathEscape(path) }

// User is a GitLab account.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// MR is a merge request as the API writes it, cut to what Oriel reads.
type MR struct {
	ID           int      `json:"id"`
	IID          int      `json:"iid"`
	ProjectID    int      `json:"project_id"`
	Title        string   `json:"title"`
	Description  string   `json:"description"`
	State        string   `json:"state"` // opened, closed, locked, merged
	Draft        bool     `json:"draft"`
	WebURL       string   `json:"web_url"`
	SourceBranch string   `json:"source_branch"`
	TargetBranch string   `json:"target_branch"`
	SHA          string   `json:"sha"`
	Author       User     `json:"author"`
	Reviewers    []User   `json:"reviewers"`
	Labels       []string `json:"labels"`
	CreatedAt    string   `json:"created_at"`
	HasConflicts bool     `json:"has_conflicts"`
	MergeStatus  string   `json:"detailed_merge_status"`
	References   struct {
		Full string `json:"full"` // group/project!57
	} `json:"references"`
	HeadPipeline *struct {
		ID     int    `json:"id"`
		Status string `json:"status"`
	} `json:"head_pipeline"`
}

// Path is the MR's project path.
func (m MR) Path() string {
	p, _, _ := strings.Cut(m.References.Full, "!")
	return p
}

// Me is the token's owner.
func (c Client) Me(ctx context.Context) (User, error) {
	var u User
	return u, c.call(ctx, "GET", "/user", nil, &u)
}

// MR is one merge request.
func (c Client) MR(ctx context.Context, path string, iid int) (MR, error) {
	var m MR
	return m, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/merge_requests/%d", project(path), iid), nil, &m)
}

// Reviewer is someone asked to review an MR, and where they are with it: unreviewed, reviewed, requested_changes,
// approved, unapproved or review_started.
type Reviewer struct {
	User      User   `json:"user"`
	State     string `json:"state"`
	CreatedAt string `json:"created_at"` // when they were asked
}

// Reviewers are who an MR asks.
func (c Client) Reviewers(ctx context.Context, path string, iid int) ([]Reviewer, error) {
	var out []Reviewer
	return out, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/merge_requests/%d/reviewers", project(path), iid), nil, &out)
}

// Diff is a file an MR changes.
type Diff struct {
	NewPath string `json:"new_path"`
	Diff    string `json:"diff"`
}

// Diffs are the files an MR changes.
func (c Client) Diffs(ctx context.Context, path string, iid int) ([]Diff, error) {
	var out []Diff
	return out, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/merge_requests/%d/diffs?per_page=100", project(path), iid), nil, &out)
}

// Job is a pipeline's job.
type Job struct {
	ID           int     `json:"id"`
	Name         string  `json:"name"`
	Status       string  `json:"status"`
	WebURL       string  `json:"web_url"`
	StartedAt    *string `json:"started_at"`
	FinishedAt   *string `json:"finished_at"`
	AllowFailure bool    `json:"allow_failure"`
}

// Jobs are a pipeline's jobs.
func (c Client) Jobs(ctx context.Context, path string, pipeline int) ([]Job, error) {
	var out []Job
	return out, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/pipelines/%d/jobs?per_page=100", project(path), pipeline), nil, &out)
}

// Trace is a job's log.
func (c Client) Trace(ctx context.Context, path string, job int) (string, error) {
	var out string
	return out, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/jobs/%d/trace", project(path), job), nil, &out)
}

// Note is a comment on an MR.
type Note struct {
	ID         int    `json:"id"`
	Body       string `json:"body"`
	Author     User   `json:"author"`
	CreatedAt  string `json:"created_at"`
	System     bool   `json:"system"`
	Resolvable bool   `json:"resolvable"`
	Resolved   bool   `json:"resolved"`
	Position   *struct {
		NewPath string `json:"new_path"`
		OldPath string `json:"old_path"`
		NewLine int    `json:"new_line"`
		OldLine int    `json:"old_line"`
	} `json:"position"`
}

// Discussion is a thread on an MR: a diff discussion's first note has a position.
type Discussion struct {
	ID    string `json:"id"`
	Notes []Note `json:"notes"`
}

// Discussions are an MR's threads, its general notes each a discussion of its own.
func (c Client) Discussions(ctx context.Context, path string, iid int) ([]Discussion, error) {
	var ds []Discussion
	return ds, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/merge_requests/%d/discussions?per_page=100", project(path), iid), nil, &ds)
}

// Member says whether a user has at least Developer access to a project.
func (c Client) Member(ctx context.Context, path string, user int) (bool, error) {
	key := path + "#" + fmt.Sprint(user)
	if c.Members != nil {
		c.Members.mu.Lock()
		ok, known := c.Members.m[key]
		c.Members.mu.Unlock()
		if known {
			return ok, nil
		}
	}
	var m struct {
		AccessLevel int `json:"access_level"`
	}
	err := c.call(ctx, "GET", fmt.Sprintf("/projects/%s/members/all/%d", project(path), user), nil, &m)
	var e *Error
	if errors.As(err, &e) && e.Status == 404 {
		err = nil
	}
	if err != nil {
		return false, err
	}
	ok := m.AccessLevel >= 30
	if c.Members != nil {
		c.Members.mu.Lock()
		if c.Members.m == nil {
			c.Members.m = map[string]bool{}
		}
		c.Members.m[key] = ok
		c.Members.mu.Unlock()
	}
	return ok, nil
}

// Unresolved counts an MR's resolvable threads not resolved.
func (c Client) Unresolved(ctx context.Context, path string, iid int) (int, error) {
	ds, err := c.Discussions(ctx, path, iid)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, d := range ds {
		if len(d.Notes) > 0 && d.Notes[0].Resolvable && !d.Notes[0].Resolved {
			n++
		}
	}
	return n, nil
}

// Project is a project's merge settings.
type Project struct {
	MergeMethod  string `json:"merge_method"`  // merge, rebase_merge, ff
	SquashOption string `json:"squash_option"` // never, always, default_on, default_off
}

// Project is a project's settings.
func (c Client) Project(ctx context.Context, path string) (Project, error) {
	var p Project
	return p, c.call(ctx, "GET", "/projects/"+project(path), nil, &p)
}

// UserID is a username's account id.
func (c Client) UserID(ctx context.Context, username string) (int, error) {
	var us []User
	if err := c.call(ctx, "GET", "/users?username="+url.QueryEscape(username), nil, &us); err != nil {
		return 0, err
	}
	if len(us) == 0 {
		return 0, fmt.Errorf("no GitLab user %q", username)
	}
	return us[0].ID, nil
}

// ParseMRURL reads https://<host>/group/project/-/merge_requests/57.
func ParseMRURL(raw string) (path string, iid int, err error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && u.Host != "" {
		p, rest, ok := strings.Cut(strings.Trim(u.Path, "/"), "/-/merge_requests/")
		if ok {
			n := 0
			if _, err := fmt.Sscanf(strings.SplitN(rest, "/", 2)[0], "%d", &n); err == nil && n > 0 && p != "" {
				return p, n, nil
			}
		}
	}
	return "", 0, errors.New("not a GitLab merge request: " + raw)
}
