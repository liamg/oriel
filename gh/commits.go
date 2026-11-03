package gh

import (
	"context"
	"fmt"
	"time"
)

// PRCommit is a commit on a pull request's branch.
type PRCommit struct {
	SHA     string    `json:"sha"`
	Message string    `json:"message"`
	Author  string    `json:"author,omitempty"` // its GitHub login, else the git author's name
	Date    time.Time `json:"date,omitzero"`
}

type restPRCommit struct {
	SHA    string `json:"sha"`
	Commit struct {
		Message string `json:"message"`
		Author  struct {
			Name string    `json:"name"`
			Date time.Time `json:"date"`
		} `json:"author"`
	} `json:"commit"`
	Author *restUser `json:"author"`
}

// PRCommits are a pull request's commits, oldest first: its first hundred.
func (a API) PRCommits(ctx context.Context, repo Repo, number int) ([]PRCommit, error) {
	var raw []restPRCommit
	if err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("pulls/%d/commits?per_page=100", number)), nil, &raw); err != nil {
		return nil, err
	}
	out := make([]PRCommit, 0, len(raw))
	for _, c := range raw {
		who := c.Commit.Author.Name
		if c.Author != nil && c.Author.Login != "" {
			who = c.Author.Login
		}
		out = append(out, PRCommit{SHA: c.SHA, Message: c.Commit.Message, Author: who, Date: c.Commit.Author.Date})
	}
	return out, nil
}
