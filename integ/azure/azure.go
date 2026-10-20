// Package azure is the Azure DevOps plugin's process: the pull requests asking for your review and a project's own,
// raised, read and merged, over Azure DevOps' REST API (hosts.md). It never holds the token: each request goes
// through the host's http.request, which adds it only for Azure DevOps' hosts (security.md).
package azure

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

// Secret is the public site's secret: a personal access token, sent as HTTP Basic's password.
const Secret = "AZURE_DEVOPS_TOKEN"

const apiVersion = "7.1"

// Site is one server: dev.azure.com, or an Azure DevOps Server. API is where its REST API is: on dev.azure.com the
// organization follows it in each path, on a server it ends in the collection. Secret names the site's token.
type Site struct {
	Host   string `json:"host"`
	API    string `json:"api"`
	Secret string `json:"secret"`
}

// orgs says the site's paths start with an organization: its API has no collection of its own.
func (s Site) orgs() bool {
	u, err := url.Parse(s.API)
	return err == nil && strings.Trim(u.Path, "/") == ""
}

// apiHost is the hostname the site's API is at.
func (s Site) apiHost() string {
	if u, err := url.Parse(s.API); err == nil {
		return u.Hostname()
	}
	return s.Host
}

// Request is one call through the host: what http.request takes, the token named by Secret.
type Request struct {
	Method string
	URL    string
	Secret string
	Body   any // sent as JSON; nil for none
}

// Send makes a request with the token and gives back the response's status and body.
type Send func(ctx context.Context, r Request) (status int, body []byte, err error)

// Client talks to Azure DevOps as the token's owner.
type Client struct {
	Send Send
}

// Error is Azure DevOps saying no.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("Azure DevOps: %s (HTTP %d)", e.Message, e.Status) }

// do calls the API with a site's token and decodes its answer into out (which may be nil).
func (c Client) do(ctx context.Context, site Site, method, raw string, in, out any) error {
	u, err := url.Parse(raw)
	if err != nil {
		return err
	}
	q := u.Query()
	q.Set("api-version", apiVersion)
	u.RawQuery = q.Encode()
	status, body, err := c.Send(ctx, Request{Method: method, URL: u.String(), Secret: site.Secret, Body: in})
	if err != nil {
		return err
	}
	if status == 401 || status == 203 { // a bad token is answered with the sign-in page, 203
		return &Error{Status: 401, Message: "the token was refused"}
	}
	if status >= 400 {
		var e struct {
			Message string `json:"message"`
		}
		json.Unmarshal(body, &e)
		return &Error{Status: status, Message: cmpOr(e.Message, strconv.Itoa(status))}
	}
	if out == nil || len(body) == 0 {
		return nil
	}
	return json.Unmarshal(body, out)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Repo is a repository on a site: its organization (none on a server), project and name. Root is where its
// organization's (or collection's) API is.
type Repo struct {
	Site               Site
	Org, Project, Name string
}

// Root is the organization's API on dev.azure.com, the collection's on a server.
func (r Repo) Root() string {
	root := strings.TrimRight(r.Site.API, "/")
	if r.Org != "" {
		root += "/" + url.PathEscape(r.Org)
	}
	return root
}

// api is the repository's git API root.
func (r Repo) api() string {
	return r.Root() + "/" + url.PathEscape(r.Project) + "/_apis/git/repositories/" + url.PathEscape(r.Name)
}

// Web is the repository's page.
func (r Repo) Web() string {
	return r.Root() + "/" + url.PathEscape(r.Project) + "/_git/" + url.PathEscape(r.Name)
}

// pathOf is a remote's or URL's path, after its host: "acme/web/_git/dashboard", "v3/acme/web/dashboard".
func pathOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
		if j := strings.IndexByte(raw, '/'); j >= 0 {
			raw = raw[j+1:]
		} else {
			raw = ""
		}
	} else if i := strings.IndexByte(raw, ':'); i >= 0 { // git@ssh.dev.azure.com:v3/…
		raw = raw[i+1:]
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	return strings.Trim(raw, "/")
}

// ParseRemote reads a git remote on a site: …/{org}/{project}/_git/{repo} over https (the organization only on
// dev.azure.com; on a server the collection is in the site's API), or v3/{org}/{project}/{repo} over ssh.
func ParseRemote(site Site, remote string) (Repo, bool) {
	parts := strings.Split(pathOf(remote), "/")
	for i := range parts {
		parts[i] = unescape(parts[i])
	}
	r := Repo{Site: site}
	if len(parts) == 4 && parts[0] == "v3" {
		r.Org, r.Project, r.Name = parts[1], parts[2], parts[3]
	} else {
		g := slices.Index(parts, "_git")
		if g < 1 || g+1 >= len(parts) {
			return Repo{}, false
		}
		r.Project, r.Name = parts[g-1], parts[g+1]
		if site.orgs() {
			if g < 2 {
				return Repo{}, false
			}
			r.Org = parts[0]
		}
	}
	r.Name = strings.TrimSuffix(r.Name, ".git")
	if !site.orgs() {
		r.Org = ""
	}
	return r, r.Project != "" && r.Name != ""
}

// ParsePR reads a pull request's page URL on a site.
func ParsePR(site Site, raw string) (Repo, int, error) {
	p := pathOf(raw)
	i := strings.LastIndex(p, "/pullrequest/")
	if i < 0 {
		return Repo{}, 0, fmt.Errorf("not an Azure DevOps pull request: %q", raw)
	}
	n, err := strconv.Atoi(strings.Trim(p[i+len("/pullrequest/"):], "/"))
	r, ok := ParseRemote(site, "https://"+site.Host+"/"+p[:i])
	if err != nil || !ok {
		return Repo{}, 0, fmt.Errorf("not an Azure DevOps pull request: %q", raw)
	}
	return r, n, nil
}

func unescape(s string) string {
	if u, err := url.PathUnescape(s); err == nil {
		return u
	}
	return s
}

// The REST shapes read, cut to what Oriel uses.

// Identity is a person or a team.
type Identity struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	UniqueName  string `json:"uniqueName"`
}

// Reviewer is someone asked to review, and their vote: 10 approved, 5 approved with suggestions, 0 none, -5
// waiting for the author, -10 rejected.
type Reviewer struct {
	Identity
	Vote        int  `json:"vote"`
	IsContainer bool `json:"isContainer"`
	IsRequired  bool `json:"isRequired"`
}

// PullRequest is a pull request as the API writes it.
type PullRequest struct {
	ID          int        `json:"pullRequestId"`
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Status      string     `json:"status"` // active, completed, abandoned
	IsDraft     bool       `json:"isDraft"`
	MergeStatus string     `json:"mergeStatus"` // succeeded, conflicts, queued…
	CreatedBy   Identity   `json:"createdBy"`
	Created     string     `json:"creationDate"`
	Source      string     `json:"sourceRefName"`
	Target      string     `json:"targetRefName"`
	Reviewers   []Reviewer `json:"reviewers"`
	LastSource  struct {
		CommitID string `json:"commitId"`
	} `json:"lastMergeSourceCommit"`
	Repository struct {
		ID        string `json:"id"`
		Name      string `json:"name"`
		RemoteURL string `json:"remoteUrl"`
		Project   struct {
			Name string `json:"name"`
		} `json:"project"`
	} `json:"repository"`
}

// Account is an organization on dev.azure.com, or a server's collection (Org "").
type Account struct {
	Site Site
	Org  string
}

// Root is the account's API.
func (a Account) Root() string { return Repo{Site: a.Site, Org: a.Org}.Root() }

// Me is who the token is, in an account.
func (c Client) Me(ctx context.Context, a Account) (Identity, error) {
	var out struct {
		User Identity `json:"authenticatedUser"`
	}
	if err := c.do(ctx, a.Site, "GET", a.Root()+"/_apis/connectionData", nil, &out); err != nil {
		return Identity{}, err
	}
	if out.User.ID == "" {
		return Identity{}, &Error{Status: 401, Message: "the token was refused"}
	}
	return out.User, nil
}

// Reviewing are an account's active pull requests that ask reviewer to review, directly or through a team.
func (c Client) Reviewing(ctx context.Context, a Account, reviewer string) ([]PullRequest, error) {
	var out struct {
		Value []PullRequest `json:"value"`
	}
	u := a.Root() + "/_apis/git/pullrequests?searchCriteria.reviewerId=" + url.QueryEscape(reviewer) + "&searchCriteria.status=active"
	return out.Value, c.do(ctx, a.Site, "GET", u, nil, &out)
}

// PR is one pull request.
func (c Client) PR(ctx context.Context, r Repo, id int) (PullRequest, error) {
	var out PullRequest
	return out, c.do(ctx, r.Site, "GET", r.api()+"/pullrequests/"+strconv.Itoa(id), nil, &out)
}

// ForBranch is a branch's pull request: its active one, else the newest; nil for none.
func (c Client) ForBranch(ctx context.Context, r Repo, branch string) (*PullRequest, error) {
	var out struct {
		Value []PullRequest `json:"value"`
	}
	u := r.api() + "/pullrequests?searchCriteria.sourceRefName=" + url.QueryEscape("refs/heads/"+branch) + "&searchCriteria.status=all"
	if err := c.do(ctx, r.Site, "GET", u, nil, &out); err != nil {
		return nil, err
	}
	var best *PullRequest
	for i, p := range out.Value {
		switch {
		case best == nil,
			p.Status == "active" && best.Status != "active",
			p.Status == best.Status && p.ID > best.ID:
			best = &out.Value[i]
		}
	}
	return best, nil
}

// Create raises a pull request.
func (c Client) Create(ctx context.Context, r Repo, title, body, branch, base string, draft bool) (PullRequest, error) {
	var out PullRequest
	in := map[string]any{"title": title, "description": body, "sourceRefName": "refs/heads/" + branch, "targetRefName": "refs/heads/" + base,
		"isDraft": draft}
	return out, c.do(ctx, r.Site, "POST", r.api()+"/pullrequests", in, &out)
}

// Update changes a pull request's fields.
func (c Client) Update(ctx context.Context, r Repo, id int, fields map[string]any) (PullRequest, error) {
	var out PullRequest
	return out, c.do(ctx, r.Site, "PATCH", r.api()+"/pullrequests/"+strconv.Itoa(id), fields, &out)
}

// Lookup is the identity with a unique name (an email, or a team's "[project]\name"), in a repo's account: through
// vssps.dev.azure.com on dev.azure.com, the collection's own API on a server.
func (c Client) Lookup(ctx context.Context, r Repo, name string) (Identity, error) {
	var out struct {
		Value []struct {
			ID          string `json:"id"`
			DisplayName string `json:"providerDisplayName"`
		} `json:"value"`
	}
	root := r.Root()
	if r.Org != "" {
		root = "https://vssps." + r.Site.apiHost() + "/" + url.PathEscape(r.Org)
	}
	u := root + "/_apis/identities?searchFilter=General&queryMembership=None&filterValue=" + url.QueryEscape(name)
	if err := c.do(ctx, r.Site, "GET", u, nil, &out); err != nil {
		return Identity{}, err
	}
	if len(out.Value) == 0 {
		return Identity{}, fmt.Errorf("Azure DevOps has no one called %q", name)
	}
	return Identity{ID: out.Value[0].ID, DisplayName: out.Value[0].DisplayName, UniqueName: name}, nil
}

// AddReviewer asks someone to review.
func (c Client) AddReviewer(ctx context.Context, r Repo, id int, reviewer string) error {
	return c.do(ctx, r.Site, "PUT", r.api()+"/pullrequests/"+strconv.Itoa(id)+"/reviewers/"+url.PathEscape(reviewer), map[string]any{"vote": 0}, nil)
}

// Status is a status posted on a pull request: a build, a policy's check.
type Status struct {
	ID          int    `json:"id"`
	State       string `json:"state"` // succeeded, failed, error, pending, notApplicable, notSet
	Description string `json:"description"`
	TargetURL   string `json:"targetUrl"`
	Created     string `json:"creationDate"`
	Updated     string `json:"updatedDate"`
	Context     struct {
		Name  string `json:"name"`
		Genre string `json:"genre"`
	} `json:"context"`
}

// Statuses are a pull request's statuses, oldest first.
func (c Client) Statuses(ctx context.Context, r Repo, id int) ([]Status, error) {
	var out struct {
		Value []Status `json:"value"`
	}
	return out.Value, c.do(ctx, r.Site, "GET", r.api()+"/pullrequests/"+strconv.Itoa(id)+"/statuses", nil, &out)
}

// Thread is a pull request's comment thread: on a line of a file when it has a Context, else the conversation's.
type Thread struct {
	ID        int            `json:"id"`
	Status    string         `json:"status"` // active, pending, fixed, wontFix, closed, byDesign
	IsDeleted bool           `json:"isDeleted"`
	Context   *ThreadContext `json:"threadContext,omitempty"`
	Comments  []Comment      `json:"comments"`
}

// ThreadContext is where a thread is: a file, and a line on its new side (Right) or old (Left).
type ThreadContext struct {
	FilePath string    `json:"filePath"`
	Right    *Position `json:"rightFileStart,omitempty"`
	Left     *Position `json:"leftFileStart,omitempty"`
}

// Position is a line and its offset.
type Position struct {
	Line   int `json:"line"`
	Offset int `json:"offset"`
}

// Comment is one comment of a thread.
type Comment struct {
	ID          int      `json:"id"`
	Parent      int      `json:"parentCommentId,omitempty"`
	Author      Identity `json:"author"`
	Content     string   `json:"content"`
	Published   string   `json:"publishedDate,omitempty"`
	CommentType string   `json:"commentType"` // text, system, codeChange
	IsDeleted   bool     `json:"isDeleted,omitempty"`
}

// Resolved says the thread is settled: fixed, closed, won't fix or by design.
func (t Thread) Resolved() bool {
	switch t.Status {
	case "fixed", "closed", "wontFix", "byDesign":
		return true
	}
	return false
}

// System says Azure DevOps wrote it: a vote, a push, a policy.
func (t Thread) System() bool { return len(t.Comments) == 0 || t.Comments[0].CommentType == "system" }

// Open says it's a person's thread still waiting on an answer.
func (t Thread) Open() bool {
	if t.IsDeleted || t.Status != "active" && t.Status != "pending" || len(t.Comments) == 0 {
		return false
	}
	return !t.System()
}

// Threads are a pull request's comment threads.
func (c Client) Threads(ctx context.Context, r Repo, id int) ([]Thread, error) {
	var out struct {
		Value []Thread `json:"value"`
	}
	return out.Value, c.do(ctx, r.Site, "GET", r.api()+"/pullrequests/"+strconv.Itoa(id)+"/threads", nil, &out)
}

// Reply adds a comment to a thread, under its first, and answers the comment.
func (c Client) Reply(ctx context.Context, r Repo, id, thread int, body string) (Comment, error) {
	var out Comment
	in := map[string]any{"content": body, "parentCommentId": 1, "commentType": "text"}
	return out, c.do(ctx, r.Site, "POST", r.api()+"/pullRequests/"+strconv.Itoa(id)+"/threads/"+strconv.Itoa(thread)+"/comments", in, &out)
}

// NewThread starts a thread in the pull request's conversation, and answers it.
func (c Client) NewThread(ctx context.Context, r Repo, id int, body string) (Thread, error) {
	var out Thread
	in := map[string]any{"status": "active", "comments": []map[string]any{{"parentCommentId": 0, "content": body, "commentType": "text"}}}
	return out, c.do(ctx, r.Site, "POST", r.api()+"/pullRequests/"+strconv.Itoa(id)+"/threads", in, &out)
}

// Change is a file a pull request changes.
type Change struct {
	Path       string
	ChangeType string
}

// Changes are the files a pull request's latest iteration changes.
func (c Client) Changes(ctx context.Context, r Repo, id int) ([]Change, error) {
	var its struct {
		Value []struct {
			ID int `json:"id"`
		} `json:"value"`
	}
	base := r.api() + "/pullrequests/" + strconv.Itoa(id) + "/iterations"
	if err := c.do(ctx, r.Site, "GET", base, nil, &its); err != nil || len(its.Value) == 0 {
		return nil, err
	}
	var out struct {
		Entries []struct {
			ChangeType string `json:"changeType"`
			Item       struct {
				Path string `json:"path"`
			} `json:"item"`
		} `json:"changeEntries"`
	}
	if err := c.do(ctx, r.Site, "GET", base+"/"+strconv.Itoa(its.Value[len(its.Value)-1].ID)+"/changes", nil, &out); err != nil {
		return nil, err
	}
	var cs []Change
	for _, e := range out.Entries {
		cs = append(cs, Change{Path: strings.TrimPrefix(e.Item.Path, "/"), ChangeType: e.ChangeType})
	}
	return cs, nil
}

// mergeTypePolicy is the "Require a merge strategy" policy's type.
const mergeTypePolicy = "fa4e907d-c16b-4a4c-9dfa-4916e5d171ab"

// MergeTypes are the merge strategies a branch allows, by Oriel's names (merge, squash, rebase); nil when no policy
// limits them.
func (c Client) MergeTypes(ctx context.Context, r Repo, repoID, branch string) ([]string, error) {
	var out struct {
		Value []struct {
			IsEnabled bool `json:"isEnabled"`
			Type      struct {
				ID string `json:"id"`
			} `json:"type"`
			Settings struct {
				NoFastForward bool `json:"allowNoFastForward"`
				Squash        bool `json:"allowSquash"`
				Rebase        bool `json:"allowRebase"`
				RebaseMerge   bool `json:"allowRebaseMerge"`
				Scope         []struct {
					RepositoryID string `json:"repositoryId"`
					RefName      string `json:"refName"`
					MatchKind    string `json:"matchKind"`
				} `json:"scope"`
			} `json:"settings"`
		} `json:"value"`
	}
	u := r.Root() + "/" + url.PathEscape(r.Project) + "/_apis/policy/configurations"
	if err := c.do(ctx, r.Site, "GET", u, nil, &out); err != nil {
		return nil, err
	}
	ref := "refs/heads/" + branch
	for _, p := range out.Value {
		if !p.IsEnabled || p.Type.ID != mergeTypePolicy {
			continue
		}
		applies := len(p.Settings.Scope) == 0
		for _, s := range p.Settings.Scope {
			if (s.RepositoryID == "" || s.RepositoryID == repoID) &&
				(s.RefName == "" || s.RefName == ref || strings.EqualFold(s.MatchKind, "prefix") && strings.HasPrefix(ref, s.RefName)) {
				applies = true
			}
		}
		if !applies {
			continue
		}
		var types []string
		if p.Settings.NoFastForward {
			types = append(types, "merge")
		}
		if p.Settings.Squash {
			types = append(types, "squash")
		}
		if p.Settings.Rebase || p.Settings.RebaseMerge {
			types = append(types, "rebase")
		}
		return types, nil
	}
	return nil, nil
}

// errNoPR is a pull request a call needs and wasn't given.
var errNoPR = errors.New("no pull request")
