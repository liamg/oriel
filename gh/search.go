package gh

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	prURL = regexp.MustCompile(`github\.com[/:]([^/]+)/([^/]+?)(?:\.git)?/pull/(\d+)`)
	prKey = regexp.MustCompile(`^([\w.-]+)/([\w.-]+)#(\d+)$`)
)

// ParsePR reads a pull request named by its URL or as owner/name#N.
func ParsePR(ref string) (Repo, int, error) {
	ref = strings.TrimSpace(ref)
	m := prURL.FindStringSubmatch(ref)
	if m == nil {
		m = prKey.FindStringSubmatch(ref)
	}
	if m == nil {
		return Repo{}, 0, fmt.Errorf("%q is not a GitHub pull request: give its URL or owner/name#N", ref)
	}
	n, _ := strconv.Atoi(m[3])
	return Repo{Owner: m[1], Name: m[2]}, n, nil
}

// PRURL is a pull request's URL on github.com.
func PRURL(r Repo, number int) string { return fmt.Sprintf("https://github.com/%s/pull/%d", r, number) }

// Found is a pull request a search found, in its repository.
type Found struct {
	PR
	Repo    string    `json:"repo"` // owner/name
	Updated time.Time `json:"updated,omitzero"`
}

// The searches of the user's pull requests, in GitHub's search syntax (@me is whoever gh is signed in as).
const (
	SearchMine   = "is:pr is:open author:@me"
	SearchReview = "is:pr is:open review-requested:@me"
)

type restFound struct {
	Number  int       `json:"number"`
	Title   string    `json:"title"`
	HTMLURL string    `json:"html_url"`
	State   string    `json:"state"`
	Draft   bool      `json:"draft"`
	Updated time.Time `json:"updated_at"`
	Created time.Time `json:"created_at"`
	User    struct {
		Login string `json:"login"`
	} `json:"user"`
	RepoURL string `json:"repository_url"`
}

// SearchPRs finds pull requests across GitHub, the most recently updated first: SearchMine, SearchReview, or any
// query of GitHub's issue search.
func (a API) SearchPRs(ctx context.Context, query string) ([]Found, error) {
	var out struct {
		Items []restFound `json:"items"`
	}
	path := "search/issues?q=" + url.QueryEscape(query) + "&sort=updated&order=desc&per_page=50"
	if err := a.T.Do(ctx, "GET", path, nil, &out); err != nil {
		return nil, err
	}
	var found []Found
	for _, it := range out.Items {
		repo := it.RepoURL
		if i := strings.Index(repo, "/repos/"); i >= 0 {
			repo = repo[i+len("/repos/"):]
		}
		found = append(found, Found{Repo: repo, Updated: it.Updated, PR: PR{Number: it.Number, Title: it.Title, URL: it.HTMLURL,
			State: it.State, Draft: it.Draft, Author: it.User.Login, Created: it.Created}})
	}
	return found, nil
}
