package gh

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// Repo is a repository on GitHub.
type Repo struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

func (r Repo) String() string { return r.Owner + "/" + r.Name }

// IsZero says the repo isn't one: a remote that isn't GitHub's.
func (r Repo) IsZero() bool { return r.Owner == "" || r.Name == "" }

var scpLike = regexp.MustCompile(`^[\w.-]+@github\.com:([\w.-]+)/([\w.-]+?)(?:\.git)?/?$`)

// ParseRepo is the repository a git remote URL names on github.com: https, ssh or scp-like.
func ParseRepo(remote string) (Repo, bool) {
	remote = strings.TrimSpace(remote)
	if m := scpLike.FindStringSubmatch(remote); m != nil {
		return Repo{Owner: m[1], Name: m[2]}, true
	}
	u, err := url.Parse(remote)
	if err != nil || !strings.EqualFold(u.Hostname(), "github.com") {
		return Repo{}, false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, false
	}
	return Repo{Owner: parts[0], Name: strings.TrimSuffix(parts[1], ".git")}, true
}

// PR is a pull request.
type PR struct {
	Number    int       `json:"number"`
	NodeID    string    `json:"nodeId,omitempty"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	URL       string    `json:"url,omitempty"`
	State     string    `json:"state,omitempty"` // open or closed
	Draft     bool      `json:"draft,omitempty"`
	Merged    bool      `json:"merged,omitempty"`
	Head      string    `json:"head,omitempty"` // its branch
	Base      string    `json:"base,omitempty"`
	Reviewers []string  `json:"reviewers,omitempty"` // asked for: logins, and org/team for a team
	Created   time.Time `json:"created,omitzero"`
}

// Open says the PR can still be merged.
func (p *PR) Open() bool { return p != nil && p.State == "open" && !p.Merged }

// Check is a check run or a commit status on a ref.
type Check struct {
	ID         int64     `json:"id,omitempty"` // a check run's; an Actions job's for Actions
	Name       string    `json:"name"`
	Status     string    `json:"status"`               // queued, in_progress or completed
	Conclusion string    `json:"conclusion,omitempty"` // success, failure, neutral, cancelled, skipped, timed_out, action_required
	URL        string    `json:"url,omitempty"`
	Started    time.Time `json:"started,omitzero"`
	Completed  time.Time `json:"completed,omitzero"`
	Actions    bool      `json:"actions,omitempty"` // run by GitHub Actions, so its log can be read
}

// Check states, as the Ship page shows them.
const (
	CheckPass    = "pass"
	CheckFail    = "fail"
	CheckPending = "pending"
	CheckSkipped = "skipped"
)

// State is the check in one word: pass, fail, pending or skipped.
func (c Check) State() string {
	if c.Status != "completed" {
		return CheckPending
	}
	switch c.Conclusion {
	case "success":
		return CheckPass
	case "skipped", "neutral":
		return CheckSkipped
	}
	return CheckFail
}

// Took is how long it ran, or has been running at now.
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

// NewPR is a pull request to open.
type NewPR struct {
	Title, Body, Head, Base string
	Draft                   bool
}

// Client is what Oriel asks of GitHub (ui.md 18): the PR for a branch, opening and editing one, its reviewers,
// and the checks on a ref.
type Client interface {
	// PRForBranch is the branch's pull request: its open one, else the newest; nil when it has none.
	PRForBranch(ctx context.Context, repo Repo, branch string) (*PR, error)
	CreatePR(ctx context.Context, repo Repo, pr NewPR) (*PR, error)
	// EditPR sets a PR's title and body.
	EditPR(ctx context.Context, repo Repo, number int, title, body string) (*PR, error)
	// MarkReady takes a draft PR out of draft.
	MarkReady(ctx context.Context, repo Repo, pr *PR) error
	// RequestReviewers asks people (logins) and teams (org/team) to review.
	RequestReviewers(ctx context.Context, repo Repo, number int, reviewers []string) error
	// Checks are the check runs and statuses on ref (a branch or a commit).
	Checks(ctx context.Context, repo Repo, ref string) ([]Check, error)
	// Log is an Actions check's log.
	Log(ctx context.Context, repo Repo, c Check) (string, error)
}

// Error is GitHub saying no: its HTTP status and message.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("GitHub: %s (HTTP %d)", e.Message, e.Status) }

// NotFound reports whether err is GitHub's 404.
func NotFound(err error) bool {
	e, ok := err.(*Error)
	return ok && e.Status == 404
}

var stamp = regexp.MustCompile(`^\d{4}-\d\d-\d\dT[\d:.]+Z\s*`)
var failing = regexp.MustCompile(`(?i)\b(fail(ed|ure)?|error|panic)\b`)

// FailLine is the line of a check's log that says what failed: the first that names a failure, with its timestamp
// cut, a runner's own "##[error]" lines last; "" for none.
func FailLine(log string) string {
	var runner string
	for _, l := range strings.Split(log, "\n") {
		l = strings.TrimSpace(stamp.ReplaceAllString(strings.TrimRight(l, "\r"), ""))
		if rest, ok := strings.CutPrefix(l, "##[error]"); ok {
			if runner == "" {
				runner = rest
			}
			continue
		}
		if !strings.HasPrefix(l, "##[") && failing.MatchString(l) {
			return l
		}
	}
	return runner
}
