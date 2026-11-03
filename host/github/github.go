package github

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
)

// GitHub review mirroring: a session's PRs' review threads and conversation become threads with
// origin github, which reach the agent like any comment. On them the agent only drafts; the user forwards a draft,
// and the host posts it with the user's own GitHub login. A forwarded comment carries its GitHub ID, so it never
// comes back as a new comment (echo reconciliation); edits, deletions and resolutions on GitHub update the mirror.

// PRsReq is a search of GitHub's pull requests: gh.SearchMine, gh.SearchReview.
type PRsReq struct {
	Query string `json:"query"`
}

// SearchPRs finds pull requests across GitHub as the user's gh is signed in.
func (x *GitHub) SearchPRs(ctx context.Context, r PRsReq) (_ []gh.Found, err error) {
	if strings.TrimSpace(r.Query) == "" {
		return nil, errors.New("no search")
	}
	return x.github(gitx.Repo{}).SearchPRs(ctx, r.Query)
}

// ReviewQueue is the user's review queue on GitHub, as their gh is signed in (gh.API.ReviewQueue).
func (x *GitHub) ReviewQueue(ctx context.Context, r ReviewQueueReq) (_ []gh.Asked, err error) {
	return x.githubOn(gitx.Repo{}, r.Host, r.Token).ReviewQueue(ctx)
}

// ReviewQueueReq names the GitHub server: github.com if Host is "".
type ReviewQueueReq struct {
	Host  string `json:"host,omitempty"`
	Token string `json:"-"`
}

// PRReview is a pull request in full, as the Reviews page shows it.
type PRReview struct {
	gh.PRDetails
	Files        []gh.PRFile        `json:"files,omitempty"`
	Reviews      []gh.Review        `json:"reviews,omitempty"`
	Checks       []gh.Check         `json:"checks,omitempty"`
	Commits      []gh.PRCommit      `json:"commits,omitempty"`
	Comments     []gh.IssueComment  `json:"comments,omitempty"`
	LineComments []gh.ReviewComment `json:"lineComments,omitempty"`
	Reactions    gh.Reactions       `json:"reactions,omitempty"` // the PR's own
}

// PRReviewReq names a pull request by its URL, and its GitHub server's hostname.
type PRReviewReq struct {
	URL   string `json:"url"`
	Host  string `json:"host,omitempty"`
	Token string `json:"-"`
}

// PRReview reads a pull request in full: its details, files, reviews, the checks on its head, and its commits,
// comments and reactions when GitHub gives them.
func (x *GitHub) PRReview(ctx context.Context, r PRReviewReq) (_ PRReview, err error) {
	repo, n, err := gh.ParsePR(r.URL)
	if err != nil {
		return PRReview{}, err
	}
	c := x.githubOn(gitx.Repo{}, r.Host, r.Token)
	var out PRReview
	if out.PRDetails, err = c.PRDetails(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.Files, err = c.PRFiles(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.Reviews, err = c.Reviews(ctx, repo, n); err != nil {
		return PRReview{}, err
	}
	if out.HeadSHA != "" {
		out.Checks, _ = c.Checks(ctx, repo, out.HeadSHA)
	}
	out.Commits, _ = c.PRCommits(ctx, repo, n)
	out.Comments, _ = c.IssueComments(ctx, repo, n)
	out.LineComments, _ = c.ReviewComments(ctx, repo, n)
	out.Reactions, _ = c.PRReactions(ctx, repo, n)
	return out, nil
}

// PRSubmitReq is the user's review of a pull request on GitHub: In's event, body, head and comments.
type PRSubmitReq struct {
	URL   string         `json:"url"`
	Host  string         `json:"host,omitempty"`
	Token string         `json:"-"`
	In    gh.ReviewInput `json:"in"`
}

// SubmitPRReview submits the user's review of a pull request: never an agent's (NoPosting).
func (x *GitHub) SubmitPRReview(ctx context.Context, r PRSubmitReq) (err error) {
	repo, n, err := gh.ParsePR(r.URL)
	if err != nil {
		return err
	}
	return x.githubOn(gitx.Repo{}, r.Host, r.Token).SubmitReview(ctx, repo, n, r.In)
}

// PRReactReq reacts to a pull request on GitHub as the user: Content is one of gh.ReactionNames.
type PRReactReq struct {
	URL     string `json:"url"`
	Host    string `json:"host,omitempty"`
	Token   string `json:"-"`
	Content string `json:"content"`
}

// ReactPR reacts to a pull request as the user.
func (x *GitHub) ReactPR(ctx context.Context, r PRReactReq) error {
	repo, n, err := gh.ParsePR(r.URL)
	if err != nil {
		return err
	}
	return x.githubOn(gitx.Repo{}, r.Host, r.Token).ReactPR(ctx, repo, n, r.Content)
}

// Trust is whose GitHub text the agent may read (crit's trusted orgs): members of Orgs and the Users named. With
// neither set, the repository's owner is the trusted org.
type Trust struct {
	Orgs  []string `json:"orgs,omitempty"`
	Users []string `json:"users,omitempty"`
}

// MirrorReq syncs a session's PRs' reviews; Fresh asks GitHub for the PR again rather than the last 30 s's.
type MirrorReq struct {
	Session core.SessionID `json:"session"`
	Fresh   bool           `json:"fresh,omitempty"`
	Trust   Trust          `json:"trust,omitzero"`
}

// Mirror is what a sync found.
type Mirror struct {
	Synced time.Time  `json:"synced"`
	PRs    []MirrorPR `json:"prs,omitempty"`
	Errors []string   `json:"errors,omitempty"`
}

// MirrorPR is one project's PR and where each reviewer stands (gh.ReviewStates).
type MirrorPR struct {
	Project string            `json:"project"`
	Repo    string            `json:"repo"`
	Number  int               `json:"number"`
	URL     string            `json:"url,omitempty"`
	Reviews map[string]string `json:"reviews,omitempty"`
	Head    string            `json:"head,omitempty"` // a reviewed PR's head commit on GitHub
	Open    bool              `json:"open,omitempty"` // not closed or merged
}

// ForwardReq forwards an agent's draft reply on a GitHub thread to GitHub.
type ForwardReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
}

// DraftEditReq rewrites a draft reply before it's forwarded.
type DraftEditReq struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread"`
	Comment string         `json:"comment"`
	Body    string         `json:"body"`
}

// mirrorState serialises applying a mirror with a forward's post and mark, so a forward's echo is always recognised;
// and remembers org members.
type mirrorState struct {
	mu        sync.Mutex
	membersMu sync.Mutex
	members   map[string]bool // org/login
	mu2       sync.Mutex
	me        string // the user's GitHub login
}
