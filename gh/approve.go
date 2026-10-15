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
}

// PRFile is one changed file; Previous is the old path of a rename.
type PRFile struct {
	Path     string `json:"filename"`
	Previous string `json:"previous_filename,omitempty"`
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
		Title string   `json:"title"`
		State string   `json:"state"`
		User  restUser `json:"user"`
		Head  struct {
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
		Additions    int `json:"additions"`
		Deletions    int `json:"deletions"`
		ChangedFiles int `json:"changed_files"`
	}
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d", number)), nil, &p); err != nil {
		return PRDetails{}, err
	}
	return PRDetails{Title: p.Title, State: p.State, Author: p.User.Login, Base: p.Base.Ref, HeadSHA: p.Head.SHA, Additions: p.Additions,
		Deletions: p.Deletions, ChangedFiles: p.ChangedFiles}, nil
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
