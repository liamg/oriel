package gh

import (
	"context"
	"fmt"
	"net/url"
	"time"
)

// A pull request's review on GitHub (RV-4): the comments on its diff's lines and their replies, the reviews and
// their states, and its conversation; and the two ways Oriel answers there, as the user.

// Review states, as GitHub names them.
const (
	ReviewApproved         = "APPROVED"
	ReviewChangesRequested = "CHANGES_REQUESTED"
	ReviewCommented        = "COMMENTED"
	ReviewDismissed        = "DISMISSED"
	ReviewPending          = "PENDING"
)

// ReviewComment is a comment on a line of a pull request's diff, or a reply in its thread.
type ReviewComment struct {
	ID           int64     `json:"id"`
	InReplyTo    int64     `json:"inReplyTo,omitempty"` // the thread's first comment, for a reply
	Review       int64     `json:"review,omitempty"`
	User         string    `json:"user"`
	Association  string    `json:"association,omitempty"` // OWNER, MEMBER, COLLABORATOR, CONTRIBUTOR, NONE…
	Body         string    `json:"body"`
	Path         string    `json:"path"`
	Line         int       `json:"line,omitempty"` // in the PR's head (or base, Side LEFT); 0 once the line is gone
	OriginalLine int       `json:"originalLine,omitempty"`
	Side         string    `json:"side,omitempty"` // RIGHT or LEFT
	URL          string    `json:"url,omitempty"`
	Created      time.Time `json:"created,omitzero"`
	Updated      time.Time `json:"updated,omitzero"`
}

// Review is a submitted review: its state and what it says overall.
type Review struct {
	ID          int64     `json:"id"`
	User        string    `json:"user"`
	Association string    `json:"association,omitempty"`
	State       string    `json:"state"`
	Body        string    `json:"body,omitempty"`
	URL         string    `json:"url,omitempty"`
	Submitted   time.Time `json:"submitted,omitzero"`
}

// IssueComment is a comment in a pull request's conversation.
type IssueComment struct {
	ID          int64     `json:"id"`
	User        string    `json:"user"`
	Association string    `json:"association,omitempty"`
	Body        string    `json:"body"`
	URL         string    `json:"url,omitempty"`
	Created     time.Time `json:"created,omitzero"`
	Updated     time.Time `json:"updated,omitzero"`
}

// ReviewStates is where each reviewer stands: their latest approval or request for changes. A review that only
// comments leaves it as it was, and a dismissal clears it.
func ReviewStates(rs []Review) map[string]string {
	out := map[string]string{}
	for _, r := range rs { // oldest first, as GitHub lists them
		switch r.State {
		case ReviewApproved, ReviewChangesRequested:
			out[r.User] = r.State
		case ReviewDismissed:
			delete(out, r.User)
		case ReviewCommented:
			if _, ok := out[r.User]; !ok {
				out[r.User] = r.State
			}
		}
	}
	return out
}

type restUser struct {
	Login string `json:"login"`
}

type restReviewComment struct {
	ID           int64     `json:"id"`
	InReplyTo    int64     `json:"in_reply_to_id"`
	Review       int64     `json:"pull_request_review_id"`
	User         restUser  `json:"user"`
	Association  string    `json:"author_association"`
	Body         string    `json:"body"`
	Path         string    `json:"path"`
	Line         *int      `json:"line"`
	OriginalLine *int      `json:"original_line"`
	Side         string    `json:"side"`
	HTMLURL      string    `json:"html_url"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

func (c restReviewComment) comment() ReviewComment {
	out := ReviewComment{ID: c.ID, InReplyTo: c.InReplyTo, Review: c.Review, User: c.User.Login, Association: c.Association, Body: c.Body,
		Path: c.Path, Side: c.Side, URL: c.HTMLURL, Created: c.CreatedAt, Updated: c.UpdatedAt}
	if c.Line != nil {
		out.Line = *c.Line
	}
	if c.OriginalLine != nil {
		out.OriginalLine = *c.OriginalLine
	}
	return out
}

type restIssueComment struct {
	ID          int64     `json:"id"`
	User        restUser  `json:"user"`
	Association string    `json:"author_association"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

func (c restIssueComment) comment() IssueComment {
	return IssueComment{ID: c.ID, User: c.User.Login, Association: c.Association, Body: c.Body, URL: c.HTMLURL, Created: c.CreatedAt, Updated: c.UpdatedAt}
}

func (a API) ReviewComments(ctx context.Context, repo Repo, number int) ([]ReviewComment, error) {
	var list []restReviewComment
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d/comments?per_page=100", number)), nil, &list); err != nil {
		return nil, err
	}
	out := make([]ReviewComment, 0, len(list))
	for _, c := range list {
		out = append(out, c.comment())
	}
	return out, nil
}

func (a API) Reviews(ctx context.Context, repo Repo, number int) ([]Review, error) {
	var list []struct {
		ID          int64     `json:"id"`
		User        restUser  `json:"user"`
		Association string    `json:"author_association"`
		State       string    `json:"state"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		SubmittedAt time.Time `json:"submitted_at"`
	}
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d/reviews?per_page=100", number)), nil, &list); err != nil {
		return nil, err
	}
	out := make([]Review, 0, len(list))
	for _, r := range list {
		out = append(out, Review{ID: r.ID, User: r.User.Login, Association: r.Association, State: r.State, Body: r.Body, URL: r.HTMLURL, Submitted: r.SubmittedAt})
	}
	return out, nil
}

func (a API) IssueComments(ctx context.Context, repo Repo, number int) ([]IssueComment, error) {
	var list []restIssueComment
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("issues/%d/comments?per_page=100", number)), nil, &list); err != nil {
		return nil, err
	}
	out := make([]IssueComment, 0, len(list))
	for _, c := range list {
		out = append(out, c.comment())
	}
	return out, nil
}

// resolvedQuery reads whether each review thread is resolved, which REST doesn't say.
const resolvedQuery = `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { reviewThreads(first: 100) { nodes { isResolved comments(first: 1) { nodes { databaseId } } } } } } }`

func (a API) ResolvedThreads(ctx context.Context, repo Repo, number int) (map[int64]bool, error) {
	var out struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					ReviewThreads struct {
						Nodes []struct {
							IsResolved bool `json:"isResolved"`
							Comments   struct {
								Nodes []struct {
									DatabaseID int64 `json:"databaseId"`
								} `json:"nodes"`
							} `json:"comments"`
						} `json:"nodes"`
					} `json:"reviewThreads"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": resolvedQuery, "variables": map[string]any{"owner": repo.Owner, "name": repo.Name, "number": number}}
	if err := a.T.Do(ctx, "POST", "graphql", in, &out); err != nil {
		return nil, err
	}
	if len(out.Errors) > 0 {
		return nil, &Error{Status: 422, Message: out.Errors[0].Message}
	}
	res := map[int64]bool{}
	for _, n := range out.Data.Repository.PullRequest.ReviewThreads.Nodes {
		if len(n.Comments.Nodes) > 0 {
			res[n.Comments.Nodes[0].DatabaseID] = n.IsResolved
		}
	}
	return res, nil
}

func (a API) ReplyReview(ctx context.Context, repo Repo, number int, to int64, body string) (ReviewComment, error) {
	var out restReviewComment
	err := a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/comments/%d/replies", number, to)), map[string]any{"body": body}, &out)
	return out.comment(), err
}

func (a API) Comment(ctx context.Context, repo Repo, number int, body string) (IssueComment, error) {
	var out restIssueComment
	err := a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("issues/%d/comments", number)), map[string]any{"body": body}, &out)
	return out.comment(), err
}

func (a API) OrgMember(ctx context.Context, org, login string) (bool, error) {
	err := a.T.Do(ctx, "GET", "orgs/"+url.PathEscape(org)+"/members/"+url.PathEscape(login), nil, nil)
	switch {
	case err == nil:
		return true, nil
	case NotFound(err):
		return false, nil
	}
	return false, err
}
