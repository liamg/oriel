package gh

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// What reviewing someone else's PR on their asking needs (crit's gh, for agentbus): who the user is, a PR's size and
// head, its files, whether its checks passed, and approving it at a commit.

// PRDetails is a pull request as a review request's card and the auto-approve rules read it.
type PRDetails struct {
	Title        string `json:"title"`
	State        string `json:"state"` // open or closed
	Author       string `json:"author"`
	Base         string `json:"base"`
	HeadSHA      string `json:"headSha"`
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	ChangedFiles int    `json:"changedFiles"`
	Body         string `json:"body,omitempty"`
	Head         string `json:"head,omitempty"` // its branch
	URL          string `json:"url,omitempty"`
	Draft        bool   `json:"draft,omitempty"`
	// Mergeable is nil until GitHub has worked it out; MergeState is its mergeable_state: clean, behind, dirty…
	Mergeable  *bool    `json:"mergeable,omitempty"`
	MergeState string   `json:"mergeState,omitempty"`
	Labels     []string `json:"labels,omitempty"`
	Reviewers  []string `json:"reviewers,omitempty"` // asked for and not yet answered: logins, and org/team for a team
}

// PRFile is one changed file; Previous is the old path of a rename.
type PRFile struct {
	Path      string `json:"filename"`
	Previous  string `json:"previous_filename,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`
}

// ParsePRURL reads https://github.com/owner/name/pull/123.
func ParsePRURL(raw string) (Repo, int, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err == nil && u.Host == "github.com" {
		p := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(p) >= 4 && p[2] == "pull" {
			if n, err := strconv.Atoi(p[3]); err == nil && n > 0 {
				return Repo{Owner: p[0], Name: p[1]}, n, nil
			}
		}
	}
	return Repo{}, 0, fmt.Errorf("not a GitHub pull request: %q", raw)
}

// Me is the login the API is signed in as.
func (a API) Me(ctx context.Context) (string, error) {
	var u restUser
	if err := a.T.Do(ctx, "GET", "user", nil, &u); err != nil {
		return "", err
	}
	if u.Login == "" {
		return "", errors.New("GitHub didn't say who you are")
	}
	return u.Login, nil
}

func (a API) PRDetails(ctx context.Context, repo Repo, number int) (PRDetails, error) {
	var p struct {
		restPR
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Additions    int    `json:"additions"`
		Deletions    int    `json:"deletions"`
		ChangedFiles int    `json:"changed_files"`
		Mergeable    *bool  `json:"mergeable"`
		MergeState   string `json:"mergeable_state"`
		Labels       []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d", number)), nil, &p); err != nil {
		return PRDetails{}, err
	}
	pr := p.pr(repo.Owner)
	out := PRDetails{Title: p.Title, State: p.State, Author: p.User.Login, Base: p.Base.Ref, HeadSHA: p.Head.SHA, Additions: p.Additions,
		Deletions: p.Deletions, ChangedFiles: p.ChangedFiles, Body: p.Body, Head: p.Head.Ref, URL: p.HTMLURL, Draft: p.Draft,
		Mergeable: p.Mergeable, MergeState: p.MergeState, Reviewers: pr.Reviewers}
	for _, l := range p.Labels {
		out.Labels = append(out.Labels, l.Name)
	}
	return out, nil
}

// PRFiles are the files a pull request changes: GitHub's first 100.
func (a API) PRFiles(ctx context.Context, repo Repo, number int) ([]PRFile, error) {
	var out []PRFile
	err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d/files?per_page=100", number)), nil, &out)
	return out, err
}

// ApproveAt approves a pull request at commit sha, so a later push isn't approved with it. The review has no body.
func (a API) ApproveAt(ctx context.Context, repo Repo, number int, sha string) error {
	return a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("pulls/%d/reviews", number)), map[string]string{"event": "APPROVE", "commit_id": sha}, nil)
}

// Green says whether every check passed (or was skipped); none at all isn't green.
func Green(cs []Check) bool {
	if len(cs) == 0 {
		return false
	}
	for _, c := range cs {
		if s := c.State(); s != CheckPass && s != CheckSkipped {
			return false
		}
	}
	return true
}
