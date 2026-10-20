package registry

import (
	"cmp"
	"context"
	"errors"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// CodeHost is where pull requests live (GitHub, GitLab, Azure DevOps), as its plugin brings it. The Reviews page
// reads its review funcs; the Ship page reads, raises and merges a project's pull request through the rest. The funcs
// may block and are called off the app's loop. Any but Requested may be nil, and what it would do isn't offered.
type CodeHost struct {
	Name  string // "github"
	Title string // "GitHub"
	Icon  string
	Order int
	Words Words
	// Sites are its public servers; a self-hosted one is set up from Setup. Path matches a remote's path on any of
	// them, its first group the repo's name ("acme/api"); Takes a pull request URL's path. Shape, when it matches a
	// whole remote URL, says the server is this host's though no one has named it (Azure DevOps' "/_git/").
	Sites  []Site
	Path   *regexp.Regexp
	Takes  *regexp.Regexp
	Shape  *regexp.Regexp
	Setup  SiteSetup
	Tokens TokenSources
	// Allow lets the host's plugin reach a site set up for it: its hostname, and the site's secret there.
	Allow func(host, secret string)

	Requested func(ctx context.Context, s Site) ([]ReviewRequest, error) // open pull requests asking you, and those you're part way through
	Lookup    func(ctx context.Context, s Site, url string) (ReviewDetail, error)
	Checkout  func(ctx context.Context, s Site, url string) (PRCheckout, error)

	ForBranch        func(ctx context.Context, r CodeRepo, branch string) (*PR, error) // the branch's newest PR, open first; nil for none
	Create           func(ctx context.Context, r CodeRepo, pr NewPR) (*PR, error)
	Edit             func(ctx context.Context, r CodeRepo, pr PR, title, body string) (*PR, error)
	Ready            func(ctx context.Context, r CodeRepo, pr PR) error
	RequestReviewers func(ctx context.Context, r CodeRepo, pr PR, who []string) error
	Checks           func(ctx context.Context, r CodeRepo, pr PR) ([]Check, error)

	// Threads are the PR's review threads and its conversation, mirrored into the session's threads; Reply posts the
	// user's forwarded draft on one (with no ID, the conversation) and answers the new comment's id.
	Threads func(ctx context.Context, r CodeRepo, pr PR) ([]ReviewThread, error)
	Reply   func(ctx context.Context, r CodeRepo, pr PR, thread ReviewThread, body string) (string, error)

	MergeMethods func(ctx context.Context, r CodeRepo, pr PR) (MergeMethods, error)
	Blockers     func(ctx context.Context, r CodeRepo, pr PR) ([]MergeBlocker, error)
	Merge        func(ctx context.Context, r CodeRepo, pr PR, o MergeOpts) error

	Source string
}

func (CodeHost) ContributionKind() string   { return "code.host" }
func (h CodeHost) ContributionName() string { return h.Name }

// Words are how a host names a pull request.
type Words struct {
	Short string `json:"short,omitempty"` // "PR", "MR"
	Long  string `json:"long,omitempty"`  // "pull request", "merge request"
	Sigil string `json:"sigil,omitempty"` // before its number: "#", "!"
}

// Said are the host's Words, with "PR", "pull request" and "#" where it says none.
func (h CodeHost) Said() Words {
	return Words{Short: cmp.Or(h.Words.Short, "PR"), Long: cmp.Or(h.Words.Long, "pull request"), Sigil: cmp.Or(h.Words.Sigil, "#")}
}

// Ref is a pull request as named in text: "acme/api#812", "group/api!57".
func (h CodeHost) Ref(repo string, number int) string {
	return repo + h.Said().Sigil + strconv.Itoa(number)
}

// Label is how the host is named to the user.
func (h CodeHost) Label() string { return cmp.Or(h.Title, h.Name) }

// TakesPath says whether path (a URL's) is one of the host's pull requests.
func (h CodeHost) TakesPath(path string) bool { return h.Takes != nil && h.Takes.MatchString(path) }

// RepoAt is the repo a remote's path names on the host.
func (h CodeHost) RepoAt(path string) (string, bool) {
	if h.Path == nil {
		return "", false
	}
	m := h.Path.FindStringSubmatch(path)
	if m == nil {
		return "", false
	}
	if len(m) > 1 && m[1] != "" {
		return strings.TrimSuffix(m[1], ".git"), true
	}
	return strings.Trim(path, "/"), true
}

// PublicSite is the host's own site at hostname, or one of its Aliases.
func (h CodeHost) PublicSite(hostname string) (Site, bool) {
	for _, s := range h.Sites {
		if strings.EqualFold(s.Host, hostname) || slices.ContainsFunc(s.Aliases, func(a string) bool { return strings.EqualFold(a, hostname) }) {
			return s, true
		}
	}
	return Site{}, false
}

// Site is one server of a code host: its public one, or one you host. Secret is the token it needs, "" for none.
// Aliases are other hostnames its remotes use for it, its ssh hosts.
type Site struct {
	Host    string   `json:"host"`              // "gitlab.com", "git.acme.corp"
	API     string   `json:"api,omitempty"`     // "https://git.acme.corp/api/v4"
	Secret  string   `json:"secret,omitempty"`  // "GITLAB_TOKEN", "GITLAB_TOKEN:git.acme.corp"
	Aliases []string `json:"aliases,omitempty"` // "ssh.dev.azure.com"
}

// SiteSetup is how a self-hosted site of a host is set up: its API's URL and its token's secret, "{host}" replaced
// by the hostname, and where and how to make the token.
type SiteSetup struct {
	API       string // "https://{host}/api/v4"
	Secret    string // "GITLAB_TOKEN": a site's is "GITLAB_TOKEN:git.acme.corp"
	TokenURL  string // the page that makes a token
	TokenHelp string // which scopes, and why
	// Probe says whether hostname is one of the host's servers, from a request that sends no token.
	Probe func(ctx context.Context, hostname string) (bool, error)
}

// TokenSources are where a site's token is found before the user is asked: environment variables, then a command's
// output ("{host}" replaced). Optional says the host can work without one (GitHub's gh has its own login).
type TokenSources struct {
	Env       []string // for a public site: "GH_TOKEN", "GITHUB_TOKEN"
	HostedEnv []string // for a self-hosted one; Env when empty
	Command   []string // "gh", "auth", "token", "--hostname", "{host}"
	Optional  bool
}

// EnvFor are the variables tried for a site, public or not.
func (t TokenSources) EnvFor(public bool) []string {
	if public || len(t.HostedEnv) == 0 {
		return t.Env
	}
	return t.HostedEnv
}

// For is the site at hostname, with api in place of the default when given.
func (s SiteSetup) For(hostname, api string) Site {
	out := Site{Host: hostname, API: cmp.Or(api, strings.ReplaceAll(s.API, "{host}", hostname))}
	if s.Secret != "" {
		out.Secret = s.Secret + ":" + hostname
	}
	return out
}

// TokenPage is the page that makes a token on hostname.
func (s SiteSetup) TokenPage(hostname string) string {
	return strings.ReplaceAll(s.TokenURL, "{host}", hostname)
}

// HostSite is a code host at one of its sites.
type HostSite struct {
	CodeHost
	Site Site
}

// Key names it uniquely: "gitlab@git.acme.corp".
func (s HostSite) Key() string { return s.Name + "@" + s.Site.Host }

// Label is the host's, with the hostname of a site that isn't its first public one.
func (s HostSite) Label() string {
	if len(s.Sites) > 0 && strings.EqualFold(s.Sites[0].Host, s.Site.Host) || s.Site.Host == "" {
		return s.CodeHost.Label()
	}
	return s.CodeHost.Label() + " (" + s.Site.Host + ")"
}

// CodeRepo is a session's project as its host's funcs are asked about it.
type CodeRepo struct {
	Session string `json:"session,omitempty"`
	Project string `json:"project,omitempty"`
	Remote  string `json:"remote"` // origin's URL
	Repo    string `json:"repo"`   // its name on the host: "acme/api"
	Site    Site   `json:"site"`
}

// ParseRemote is a git remote's hostname and path: https://, ssh://, git:// and scp-like user@host:path.
func ParseRemote(remote string) (host, path string, ok bool) {
	remote = strings.TrimSpace(remote)
	if i := strings.Index(remote, "://"); i >= 0 {
		rest := remote[i+3:]
		hostport, p, _ := strings.Cut(rest, "/")
		if _, h, found := strings.Cut(hostport, "@"); found {
			hostport = h
		}
		host, _, _ = strings.Cut(hostport, ":")
		return strings.ToLower(host), "/" + p, host != ""
	}
	if at, p, found := strings.Cut(remote, ":"); found && !strings.Contains(at, "/") {
		if _, h, ok := strings.Cut(at, "@"); ok {
			at = h
		}
		return strings.ToLower(at), "/" + strings.TrimPrefix(p, "/"), at != ""
	}
	return "", "", false
}

// PR is a pull request (a merge request on GitLab) as a code host tells it.
type PR struct {
	Number    int      `json:"number"`
	ID        string   `json:"id,omitempty"` // the host's own id, when calls need it
	Title     string   `json:"title"`
	Body      string   `json:"body,omitempty"`
	URL       string   `json:"url,omitempty"`
	Author    string   `json:"author,omitempty"`
	State     string   `json:"state,omitempty"` // open or closed
	Draft     bool     `json:"draft,omitempty"`
	Merged    bool     `json:"merged,omitempty"`
	Branch    string   `json:"branch,omitempty"` // the one it merges from
	Base      string   `json:"base,omitempty"`
	Head      string   `json:"head,omitempty"`      // the commit it's at
	Reviewers []string `json:"reviewers,omitempty"` // asked for: logins, and org/team for a team
}

// Open says the PR can still be merged.
func (p *PR) Open() bool { return p != nil && p.State == "open" && !p.Merged }

// ReviewThread is a review thread on a pull request as its code host has it: on a line of a file, or, with no ID, the
// PR's conversation.
type ReviewThread struct {
	ID       string       `json:"id,omitempty"`
	Path     string       `json:"path,omitempty"`
	Line     int          `json:"line,omitempty"`
	Old      bool         `json:"old,omitempty"` // on the old side of the diff
	Outdated bool         `json:"outdated,omitempty"`
	Resolved *bool        `json:"resolved,omitempty"` // nil when the host doesn't say
	Reviewer string       `json:"reviewer,omitempty"`
	State    string       `json:"state,omitempty"` // the reviewer's: ReviewChanges, ReviewApproved, ReviewCommented
	URL      string       `json:"url,omitempty"`
	Comments []ReviewNote `json:"comments"`
}

// ReviewNote is one comment of a ReviewThread. Member says its author belongs to the repo, so the agent may read it.
type ReviewNote struct {
	ID     string    `json:"id"`
	Author string    `json:"author"`
	Body   string    `json:"body"`
	At     time.Time `json:"at,omitzero"`
	Member bool      `json:"member,omitempty"`
}

// NewPR is a pull request to raise.
type NewPR struct {
	Title  string `json:"title"`
	Body   string `json:"body,omitempty"`
	Branch string `json:"branch"`
	Base   string `json:"base"`
	Draft  bool   `json:"draft,omitempty"`
}

// Check states.
const (
	CheckOK      = "ok"
	CheckFailed  = "failed"
	CheckRunning = "running"
	CheckQueued  = "queued"
	CheckSkipped = "skipped"
)

// Check is a check on a pull request's head.
type Check struct {
	Name      string    `json:"name"`
	State     string    `json:"state"` // CheckOK, CheckFailed…
	URL       string    `json:"url,omitempty"`
	Started   time.Time `json:"started,omitzero"`
	Completed time.Time `json:"completed,omitzero"`
	Detail    string    `json:"detail,omitempty"` // a failing one's: the line of its log that says why
}

// Pending says it hasn't finished.
func (c Check) Pending() bool { return c.State == CheckRunning || c.State == CheckQueued }

// Took is how long it ran, or has run so far.
func (c Check) Took(now time.Time) time.Duration {
	if c.Started.IsZero() {
		return 0
	}
	end := c.Completed
	if end.IsZero() {
		end = now
	}
	return end.Sub(c.Started)
}

// Merge methods.
const (
	MergeCommit = "merge"
	MergeSquash = "squash"
	MergeRebase = "rebase"
)

// MergeMethods are what a repo allows, and its own default if it has one.
type MergeMethods struct {
	Allowed []string `json:"allowed"`
	Default string   `json:"default,omitempty"`
}

// Pick is the method to merge with: want when allowed, else the repo's default, else squash, else the first allowed.
func (m MergeMethods) Pick(want string) string {
	for _, w := range []string{want, m.Default, MergeSquash} {
		if w != "" && slices.Contains(m.Allowed, w) {
			return w
		}
	}
	if len(m.Allowed) > 0 {
		return m.Allowed[0]
	}
	return ""
}

// Merge blocker kinds.
const (
	BlockThreads      = "threads"       // reviewers' threads not resolved
	BlockOrielThreads = "oriel-threads" // the session's own threads still open
	BlockChanges      = "changes"       // someone's asking for changes
	BlockChecks       = "checks"        // failing or still running
	BlockDraft        = "draft"
	BlockConflicts    = "conflicts"
	BlockBehind       = "behind"
	BlockReviews      = "reviews" // required reviews missing
	BlockOrder        = "order"   // an earlier PR of the session's merge order isn't merged
)

// MergeBlocker is a reason not to merge now. A hard one is the host's: it would refuse the merge.
type MergeBlocker struct {
	Kind  string `json:"kind"`
	Count int    `json:"count,omitempty"`
	Text  string `json:"text"`
	Hard  bool   `json:"hard,omitempty"`
	URL   string `json:"url,omitempty"` // where to see it
}

// MergeOpts is how to merge. Head is the commit the user saw: the host refuses if the PR has moved on.
type MergeOpts struct {
	Method       string `json:"method"`
	Head         string `json:"head,omitempty"`
	Title        string `json:"title,omitempty"`
	Body         string `json:"body,omitempty"`
	DeleteBranch bool   `json:"delete_branch,omitempty"`
}

// ErrNotOffered is a host asked for something it doesn't do.
var ErrNotOffered = errors.New("the code host doesn't offer that")

// CodeHosts are the hosts by Order, then name.
func CodeHosts(r Registry) []CodeHost {
	var out []CodeHost
	for _, c := range r.Contributions("code.host") {
		out = append(out, c.(CodeHost))
	}
	slices.SortFunc(out, func(a, b CodeHost) int {
		return cmp.Or(cmp.Compare(a.Order, b.Order), strings.Compare(a.Name, b.Name))
	})
	return out
}

// HostByName is the host named name.
func HostByName(r Registry, name string) (CodeHost, bool) {
	for _, h := range CodeHosts(r) {
		if h.Name == name {
			return h, true
		}
	}
	return CodeHost{}, false
}
