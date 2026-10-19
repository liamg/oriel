package gh

import (
	"context"
	"strings"
	"time"
)

// The user's review queue: the pull requests asking for their review, and those they've reviewed that are still
// open, each with where the user stands, in one GraphQL search per list.

// SearchReviewed are the open pull requests the user reviewed and isn't asked to again.
const SearchReviewed = "is:pr is:open reviewed-by:@me -review-requested:@me -author:@me"

// Asked is a pull request on the user's review queue.
type Asked struct {
	Found
	Requested bool      // the user's review is asked for now, of them or a team of theirs
	Via       string    // the team asked ("acme/platform"), when it wasn't the user
	Asked     time.Time // when last asked; its creation when GitHub doesn't say
	Mine      string    // the user's latest review: ReviewApproved…; "" none
	Since     int       // commits since that review
	Additions int
	Deletions int
	Files     int
	CI        string // CheckPass, CheckFail or CheckPending; "" no checks
}

const queueQuery = `query($q: String!) {
  viewer { login }
  search(type: ISSUE, query: $q, first: 50) { nodes { ... on PullRequest {
    number title url isDraft state createdAt updatedAt additions deletions changedFiles
    author { login } repository { nameWithOwner }
    reviewRequests(first: 30) { nodes { requestedReviewer { __typename ... on User { login } ... on Team { combinedSlug } } } }
    timelineItems(last: 30, itemTypes: [REVIEW_REQUESTED_EVENT]) { nodes { ... on ReviewRequestedEvent { createdAt requestedReviewer { __typename ... on User { login } ... on Team { combinedSlug } } } } }
    latestReviews(first: 30) { nodes { author { login } state commit { oid } } }
    commits(last: 100) { nodes { commit { oid statusCheckRollup { state } } } }
  } } }
}`

type gqlReviewer struct {
	Typename     string `json:"__typename"`
	Login        string `json:"login"`
	CombinedSlug string `json:"combinedSlug"`
}

type gqlAsked struct {
	Number       int       `json:"number"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	IsDraft      bool      `json:"isDraft"`
	State        string    `json:"state"`
	CreatedAt    time.Time `json:"createdAt"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Additions    int       `json:"additions"`
	Deletions    int       `json:"deletions"`
	ChangedFiles int       `json:"changedFiles"`
	Author       *restUser `json:"author"`
	Repository   struct {
		NameWithOwner string `json:"nameWithOwner"`
	} `json:"repository"`
	ReviewRequests struct {
		Nodes []struct {
			RequestedReviewer gqlReviewer `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"reviewRequests"`
	TimelineItems struct {
		Nodes []struct {
			CreatedAt         time.Time   `json:"createdAt"`
			RequestedReviewer gqlReviewer `json:"requestedReviewer"`
		} `json:"nodes"`
	} `json:"timelineItems"`
	LatestReviews struct {
		Nodes []struct {
			Author *restUser `json:"author"`
			State  string    `json:"state"`
			Commit *struct {
				OID string `json:"oid"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"latestReviews"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				OID               string `json:"oid"`
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

// ReviewQueue is the user's review queue: SearchReview's pull requests, then SearchReviewed's.
func (a API) ReviewQueue(ctx context.Context) ([]Asked, error) {
	var out []Asked
	for _, q := range []string{SearchReview, SearchReviewed} {
		found, err := a.queue(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, found...)
	}
	return out, nil
}

func (a API) queue(ctx context.Context, query string) ([]Asked, error) {
	var res struct {
		Data struct {
			Viewer restUser `json:"viewer"`
			Search struct {
				Nodes []gqlAsked `json:"nodes"`
			} `json:"search"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": queueQuery, "variables": map[string]any{"q": query}}
	if err := a.T.Do(ctx, "POST", "graphql", in, &res); err != nil {
		return nil, err
	}
	if len(res.Errors) > 0 {
		return nil, &Error{Status: 422, Message: res.Errors[0].Message}
	}
	me := res.Data.Viewer.Login
	var out []Asked
	for _, n := range res.Data.Search.Nodes {
		if n.Number == 0 {
			continue
		}
		out = append(out, n.asked(me))
	}
	return out, nil
}

func (r gqlReviewer) name() string {
	if r.Typename == "Team" {
		return r.CombinedSlug
	}
	return r.Login
}

func (n gqlAsked) asked(me string) Asked {
	out := Asked{Found: Found{Repo: n.Repository.NameWithOwner, Updated: n.UpdatedAt, PR: PR{Number: n.Number, Title: n.Title, URL: n.URL,
		State: strings.ToLower(n.State), Draft: n.IsDraft, Created: n.CreatedAt}}, Additions: n.Additions, Deletions: n.Deletions, Files: n.ChangedFiles,
		Asked: n.CreatedAt}
	if n.Author != nil {
		out.Author = n.Author.Login
	}
	var teams []string
	for _, rr := range n.ReviewRequests.Nodes {
		switch who := rr.RequestedReviewer; {
		case who.Typename == "Team":
			teams = append(teams, who.CombinedSlug)
		case strings.EqualFold(who.Login, me):
			out.Requested = true
		}
	}
	if !out.Requested && len(teams) > 0 {
		out.Requested, out.Via = true, teams[0]
	}
	asked := me
	if out.Via != "" {
		asked = out.Via
	}
	for _, ev := range n.TimelineItems.Nodes {
		if strings.EqualFold(ev.RequestedReviewer.name(), asked) && ev.CreatedAt.After(out.Asked) {
			out.Asked = ev.CreatedAt
		}
	}
	var at string
	for _, rv := range n.LatestReviews.Nodes {
		if rv.Author != nil && strings.EqualFold(rv.Author.Login, me) {
			out.Mine = rv.State
			if rv.Commit != nil {
				at = rv.Commit.OID
			}
		}
	}
	commits := n.Commits.Nodes
	if at != "" {
		for i, c := range commits {
			if c.Commit.OID == at {
				out.Since = len(commits) - 1 - i
			}
		}
	}
	if len(commits) > 0 {
		if roll := commits[len(commits)-1].Commit.StatusCheckRollup; roll != nil {
			switch roll.State {
			case "SUCCESS":
				out.CI = CheckPass
			case "PENDING", "EXPECTED":
				out.CI = CheckPending
			default:
				out.CI = CheckFail
			}
		}
	}
	return out
}
