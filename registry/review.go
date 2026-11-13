package registry

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Where you stand on a pull request you were asked to review.
const (
	ReviewNew         = "new"          // asked, and no review from you yet
	ReviewReRequested = "re-requested" // you reviewed, and were asked again
	ReviewChanges     = "changes"      // you asked for changes: their move
	ReviewCommented   = "commented"    // you commented: their move
	ReviewApproved    = "approved"
)

// ReviewRequest is a pull request waiting on your review, as its source tells it: a row of the Reviews page. Its JSON
// is the same shape Lua and a plugin's process give it in.
type ReviewRequest struct {
	URL    string    `json:"url"`
	Repo   string    `json:"repo"` // "acme/api"
	Number int       `json:"number"`
	Title  string    `json:"title"`
	Author string    `json:"author,omitempty"`
	Draft  bool      `json:"draft,omitempty"`
	Asked  time.Time `json:"asked,omitzero"`  // when you were (last) asked
	Via    string    `json:"via,omitempty"`   // "" asked directly, else the team: "@acme/platform"
	State  string    `json:"state,omitempty"` // ReviewNew when ""
	Since  int       `json:"since,omitempty"` // commits since your last review
	Adds   int       `json:"adds,omitempty"`
	Dels   int       `json:"dels,omitempty"`
	Files  int       `json:"files,omitempty"`
	CI     string    `json:"ci,omitempty"`     // "ok", "failed", "running"; "" none
	Source string    `json:"source,omitempty"` // the source's Title, filled in by RequestedReviews
	// Reviewers are who's asked (State "requested", a team as "@org/team") and who has reviewed, by their latest
	// review ("approved", "changes", "commented").
	Reviewers []PRReviewer `json:"reviewers,omitempty"`
	Base      string       `json:"base,omitempty"` // the branch it goes into
	Head      string       `json:"head,omitempty"` // its own branch
	Opened    time.Time    `json:"opened,omitzero"`
	Updated   time.Time    `json:"updated,omitzero"`
}

// Waiting says the next move is yours: you're asked, and haven't answered since.
func (r ReviewRequest) Waiting() bool {
	return r.State == "" || r.State == ReviewNew || r.State == ReviewReRequested
}

// Counts says it counts toward the waiting badge: waiting, not a draft, and asked of you directly unless teams count.
func (r ReviewRequest) Counts(teams bool) bool {
	return r.Waiting() && !r.Draft && (r.Via == "" || teams)
}

// Key is how it's named short: "acme/api#812".
func (r ReviewRequest) Key() string { return r.Repo + "#" + strconv.Itoa(r.Number) }

// ReviewDetail is a pull request in full, for the Reviews page's right-hand side.
type ReviewDetail struct {
	Request   ReviewRequest  `json:"request"`
	Base      string         `json:"base,omitempty"`
	Head      string         `json:"head,omitempty"`
	Commit    string         `json:"commit,omitempty"`    // the head's commit
	State     string         `json:"state,omitempty"`     // "open", "closed" or "merged"; "" unknown
	Mergeable string         `json:"mergeable,omitempty"` // "yes", "conflicts", "yes · behind main"; "" unknown
	Labels    []string       `json:"labels,omitempty"`
	Reviewers []PRReviewer   `json:"reviewers,omitempty"`
	Body      string         `json:"body,omitempty"` // markdown, the author's: shown to the user, never handed to an agent
	Files     []PRFileChange `json:"files,omitempty"`
	Checks    []Check        `json:"checks,omitempty"`
	Clone     string         `json:"clone,omitempty"`  // the repo's clone URL, for Add repo…
	Events    []PREvent      `json:"events,omitempty"` // oldest first; shown to the user, never handed to an agent
	Reactions []PRReaction   `json:"reactions,omitempty"`
}

// PRReaction is how many people reacted to a pull request, or a comment on it, with an emoji.
type PRReaction struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
}

// What a PREvent is.
const (
	PREventPush    = "push"
	PREventReview  = "review"
	PREventComment = "comment"
)

// PREvent is something that happened on a pull request: a commit pushed, a review submitted, a comment made.
type PREvent struct {
	Kind      string       `json:"kind"`
	Who       string       `json:"who,omitempty"`
	At        time.Time    `json:"at,omitzero"`
	Text      string       `json:"text,omitempty"`  // a commit's subject, a review's or comment's body
	State     string       `json:"state,omitempty"` // a review's: approved, changes, commented, dismissed
	Ref       string       `json:"ref,omitempty"`   // a push's commit; a line comment's path:line
	URL       string       `json:"url,omitempty"`
	Reactions []PRReaction `json:"reactions,omitempty"`
}

// PRReviewer is someone asked to review, or who did: "requested", "approved", "changes", "commented".
type PRReviewer struct {
	Login string `json:"login"`
	State string `json:"state"`
}

// PRFileChange is a file a pull request changes.
type PRFileChange struct {
	Path string `json:"path"`
	Adds int    `json:"adds,omitempty"`
	Dels int    `json:"dels,omitempty"`
}

// PRCheckout is where a review session fetches a pull request from: the registered repo whose remote contains
// Remote, and the refspec to fetch from it.
type PRCheckout struct {
	Remote string `json:"remote"` // "acme/api"
	Ref    string `json:"ref"`    // "pull/812/head"
}

// RequestedReviews asks s for the pull requests waiting on you, each's Source filled in.
func RequestedReviews(ctx context.Context, s HostSite) ([]ReviewRequest, error) {
	if s.Requested == nil {
		return nil, nil
	}
	var out []ReviewRequest
	if err := Call(func() (err error) { out, err = s.Requested(ctx, s.Site); return err }); err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Source = s.Label()
		if out[i].State == "" {
			out[i].State = ReviewNew
		}
	}
	return out, nil
}

// LookupReview asks s for url's pull request in full.
func LookupReview(ctx context.Context, s HostSite, url string) (ReviewDetail, error) {
	if s.Lookup == nil {
		return ReviewDetail{}, nil
	}
	var d ReviewDetail
	err := Call(func() (err error) { d, err = s.Lookup(ctx, s.Site, strings.TrimSpace(url)); return err })
	d.Request.Source = s.Label()
	return d, err
}

// ReviewSubmit is the user's review of a pull request: a verdict, a summary, and comments on its lines.
type ReviewSubmit struct {
	Event    string          `json:"event"` // VerdictApprove, VerdictChanges or VerdictComment
	Body     string          `json:"body,omitempty"`
	Commit   string          `json:"commit,omitempty"` // the head the review read; "" for the PR's head now
	Comments []ReviewComment `json:"comments,omitempty"`
}

// The verdicts a review is submitted with.
const (
	VerdictApprove = "approve"
	VerdictChanges = "changes"
	VerdictComment = "comment"
)

// ReviewComment is a comment on lines of the new code (Old: the removed code): Line, or StartLine to Line.
type ReviewComment struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	StartLine int    `json:"start_line,omitempty"`
	Old       bool   `json:"old,omitempty"`
	Body      string `json:"body"`
}

// SubmitReview posts the user's review of url's pull request to s.
func SubmitReview(ctx context.Context, s HostSite, url string, r ReviewSubmit) error {
	if s.Submit == nil {
		return fmt.Errorf("%s can't take a review from Oriel", s.Label())
	}
	return Call(func() error { return s.Submit(ctx, s.Site, strings.TrimSpace(url), r) })
}

// ReactToPR reacts to url's pull request on s with emoji.
func ReactToPR(ctx context.Context, s HostSite, url, emoji string) error {
	if s.React == nil {
		return fmt.Errorf("%s can't take a reaction from Oriel", s.Label())
	}
	return Call(func() error { return s.React(ctx, s.Site, strings.TrimSpace(url), emoji) })
}

// UnreactToPR takes back the user's emoji reaction to url's pull request on s.
func UnreactToPR(ctx context.Context, s HostSite, url, emoji string) error {
	if s.Unreact == nil {
		return fmt.Errorf("%s can't take a reaction back from Oriel", s.Label())
	}
	return Call(func() error { return s.Unreact(ctx, s.Site, strings.TrimSpace(url), emoji) })
}

// CheckoutFor asks s where url's pull request is fetched from.
func CheckoutFor(ctx context.Context, s HostSite, url string) (PRCheckout, error) {
	var c PRCheckout
	err := Call(func() (err error) { c, err = s.Checkout(ctx, s.Site, strings.TrimSpace(url)); return err })
	return c, err
}

// SortReviews puts what's waiting on you first, the oldest ask first, then what you're part way through, the newest
// first; drafts last in each.
func SortReviews(rs []ReviewRequest) { slices.SortStableFunc(rs, CompareReviews) }

// CompareReviews is SortReviews' order.
func CompareReviews(a, b ReviewRequest) int {
	if a.Waiting() != b.Waiting() {
		return boolFirst(a.Waiting())
	}
	if a.Draft != b.Draft {
		return boolFirst(b.Draft)
	}
	if c := a.Asked.Compare(b.Asked); c != 0 {
		if a.Waiting() {
			return c
		}
		return -c
	}
	return cmp.Or(strings.Compare(a.Repo, b.Repo), cmp.Compare(a.Number, b.Number))
}
