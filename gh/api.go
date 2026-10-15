package gh

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Transport makes one GitHub REST call: path is relative to the API's root ("repos/o/n/pulls?state=all"); in is
// sent as JSON (nil for none) and the answer decoded into out, or kept whole when out is a *string.
type Transport interface {
	Do(ctx context.Context, method, path string, in, out any) error
}

// API is a Client over GitHub's REST API (and one GraphQL mutation REST lacks).
type API struct{ T Transport }

var _ Client = API{}

// The REST shapes read, cut to what Oriel uses.
type restPR struct {
	Number  int    `json:"number"`
	NodeID  string `json:"node_id"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	State   string `json:"state"`
	Draft   bool   `json:"draft"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	MergedAt  *time.Time `json:"merged_at"`
	CreatedAt time.Time  `json:"created_at"`
	Head      struct {
		Ref string `json:"ref"`
	} `json:"head"`
	Base struct {
		Ref string `json:"ref"`
	} `json:"base"`
	Reviewers []struct {
		Login string `json:"login"`
	} `json:"requested_reviewers"`
	Teams []struct {
		Slug string `json:"slug"`
	} `json:"requested_teams"`
}

func (p restPR) pr(owner string) *PR {
	out := &PR{Number: p.Number, NodeID: p.NodeID, Title: p.Title, Author: p.User.Login, Body: p.Body, URL: p.HTMLURL, State: p.State, Draft: p.Draft,
		Merged: p.MergedAt != nil, Head: p.Head.Ref, Base: p.Base.Ref, Created: p.CreatedAt}
	for _, r := range p.Reviewers {
		out.Reviewers = append(out.Reviewers, r.Login)
	}
	for _, t := range p.Teams {
		out.Reviewers = append(out.Reviewers, owner+"/"+t.Slug)
	}
	return out
}

func repoPath(r Repo, rest string) string {
	return "repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name) + "/" + rest
}

func (a API) PRForBranch(ctx context.Context, repo Repo, branch string) (*PR, error) {
	var list []restPR
	q := url.Values{"head": {repo.Owner + ":" + branch}, "state": {"all"}, "per_page": {"20"}}
	if err := a.T.Do(ctx, "GET", repoPath(repo, "pulls?"+q.Encode()), nil, &list); err != nil {
		return nil, err
	}
	var best *restPR
	for i, p := range list {
		if p.Head.Ref != branch {
			continue
		}
		switch {
		case best == nil,
			p.State == "open" && best.State != "open",
			p.State == best.State && p.CreatedAt.After(best.CreatedAt):
			best = &list[i]
		}
	}
	if best == nil {
		return nil, nil
	}
	return best.pr(repo.Owner), nil
}

func (a API) CreatePR(ctx context.Context, repo Repo, pr NewPR) (*PR, error) {
	var out restPR
	in := map[string]any{"title": pr.Title, "body": pr.Body, "head": pr.Head, "base": pr.Base, "draft": pr.Draft}
	if err := a.T.Do(ctx, "POST", repoPath(repo, "pulls"), in, &out); err != nil {
		return nil, err
	}
	return out.pr(repo.Owner), nil
}

func (a API) EditPR(ctx context.Context, repo Repo, number int, title, body string) (*PR, error) {
	var out restPR
	if err := a.T.Do(ctx, "PATCH", repoPath(repo, fmt.Sprintf("pulls/%d", number)), map[string]any{"title": title, "body": body}, &out); err != nil {
		return nil, err
	}
	return out.pr(repo.Owner), nil
}

func (a API) MarkReady(ctx context.Context, _ Repo, pr *PR) error {
	if pr == nil || pr.NodeID == "" {
		return fmt.Errorf("no pull request to mark ready")
	}
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{
		"query":     `mutation($id: ID!) { markPullRequestReadyForReview(input: {pullRequestId: $id}) { pullRequest { isDraft } } }`,
		"variables": map[string]any{"id": pr.NodeID},
	}
	if err := a.T.Do(ctx, "POST", "graphql", in, &out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return &Error{Status: 422, Message: out.Errors[0].Message}
	}
	return nil
}

func (a API) RequestReviewers(ctx context.Context, repo Repo, number int, reviewers []string) error {
	users, teams := []string{}, []string{}
	for _, r := range reviewers {
		r = strings.TrimPrefix(r, "@")
		if _, team, ok := strings.Cut(r, "/"); ok {
			teams = append(teams, team)
		} else if r != "" {
			users = append(users, r)
		}
	}
	if len(users)+len(teams) == 0 {
		return nil
	}
	in := map[string]any{"reviewers": users, "team_reviewers": teams}
	return a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/requested_reviewers", number)), in, nil)
}

func (a API) Checks(ctx context.Context, repo Repo, ref string) ([]Check, error) {
	var runs struct {
		CheckRuns []struct {
			ID          int64      `json:"id"`
			Name        string     `json:"name"`
			Status      string     `json:"status"`
			Conclusion  string     `json:"conclusion"`
			HTMLURL     string     `json:"html_url"`
			StartedAt   *time.Time `json:"started_at"`
			CompletedAt *time.Time `json:"completed_at"`
			App         struct {
				Slug string `json:"slug"`
			} `json:"app"`
		} `json:"check_runs"`
	}
	ref = url.PathEscape(ref)
	if err := a.T.Do(ctx, "GET", repoPath(repo, "commits/"+ref+"/check-runs?per_page=100"), nil, &runs); err != nil {
		return nil, err
	}
	var out []Check
	for _, r := range runs.CheckRuns {
		c := Check{ID: r.ID, Name: r.Name, Status: r.Status, Conclusion: r.Conclusion, URL: r.HTMLURL, Actions: r.App.Slug == "github-actions"}
		if r.StartedAt != nil {
			c.Started = *r.StartedAt
		}
		if r.CompletedAt != nil {
			c.Completed = *r.CompletedAt
		}
		out = append(out, c)
	}
	var status struct {
		Statuses []struct {
			Context   string    `json:"context"`
			State     string    `json:"state"`
			TargetURL string    `json:"target_url"`
			CreatedAt time.Time `json:"created_at"`
			UpdatedAt time.Time `json:"updated_at"`
		} `json:"statuses"`
	}
	if err := a.T.Do(ctx, "GET", repoPath(repo, "commits/"+ref+"/status"), nil, &status); err != nil && !NotFound(err) {
		return nil, err
	}
	seen := map[string]bool{}
	for _, s := range status.Statuses { // newest first: a context's latest state wins
		if seen[s.Context] {
			continue
		}
		seen[s.Context] = true
		c := Check{Name: s.Context, URL: s.TargetURL, Started: s.CreatedAt, Status: "completed"}
		switch s.State {
		case "pending":
			c.Status = "in_progress"
		case "success":
			c.Conclusion, c.Completed = "success", s.UpdatedAt
		default:
			c.Conclusion, c.Completed = "failure", s.UpdatedAt
		}
		out = append(out, c)
	}
	return out, nil
}

func (a API) Log(ctx context.Context, repo Repo, c Check) (string, error) {
	if !c.Actions || c.ID == 0 {
		return "", fmt.Errorf("%s isn't an Actions job: its log is at %s", c.Name, c.URL)
	}
	var log string
	err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("actions/jobs/%d/logs", c.ID)), nil, &log)
	return log, err
}
