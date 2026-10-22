package catalog

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

// GitHub is the little of GitHub's REST API the registry needs: org membership, release assets, a repo's tags.
// Token reads a private repo; none is needed for public ones.
type GitHub struct {
	Base   string // https://api.github.com if ""
	Token  string
	Auth   func(ctx context.Context) string // the token, asked for each request, when Token is ""
	Client *http.Client
}

func (g GitHub) get(ctx context.Context, url, accept string) (*http.Response, error) {
	if !strings.HasPrefix(url, "http") {
		url = strings.TrimSuffix(cmpOr(g.Base, "https://api.github.com"), "/") + url
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	tok := g.Token
	if tok == "" && g.Auth != nil {
		tok = g.Auth(ctx)
	}
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	c := g.Client
	if c == nil {
		c = http.DefaultClient
	}
	return c.Do(req)
}

// Member says whether user is org itself or one of its members.
func (g GitHub) Member(ctx context.Context, org, user string) (bool, error) {
	if strings.EqualFold(org, user) {
		return true, nil
	}
	r, err := g.get(ctx, "/orgs/"+org+"/members/"+user, "application/vnd.github+json")
	if err != nil {
		return false, err
	}
	r.Body.Close()
	switch r.StatusCode {
	case http.StatusNoContent:
		return true, nil
	case http.StatusNotFound, http.StatusFound:
		return false, nil
	}
	return false, fmt.Errorf("GitHub answered %s", r.Status)
}

type ghRelease struct {
	TagName string    `json:"tag_name"`
	Draft   bool      `json:"draft"`
	Assets  []ghAsset `json:"assets"`
}

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Asset reads the asset name of repo's release for tag.
func (g GitHub) Asset(ctx context.Context, repo, tag, name string) ([]byte, error) {
	var rel ghRelease
	if err := g.json(ctx, "/repos/"+repo+"/releases/tags/"+tag, &rel); err != nil {
		return nil, err
	}
	return g.asset(ctx, rel, name)
}

// LatestAsset reads the asset name of repo's newest release that has it: the registry's index. With no such
// release, or no repo to see, the error is fs.ErrNotExist.
func (g GitHub) LatestAsset(ctx context.Context, repo, name string) ([]byte, error) {
	var rels []ghRelease
	if err := g.json(ctx, "/repos/"+repo+"/releases?per_page=20", &rels); err != nil {
		return nil, err
	}
	for _, rel := range rels {
		if rel.Draft {
			continue
		}
		for _, a := range rel.Assets {
			if a.Name == name {
				return g.asset(ctx, rel, name)
			}
		}
	}
	return nil, fmt.Errorf("%s has no release with %s: %w", repo, name, fs.ErrNotExist)
}

func (g GitHub) asset(ctx context.Context, rel ghRelease, name string) ([]byte, error) {
	for _, a := range rel.Assets {
		if a.Name != name {
			continue
		}
		r, err := g.get(ctx, a.URL, "application/octet-stream")
		if err != nil {
			return nil, err
		}
		defer r.Body.Close()
		if r.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("%s: GitHub answered %s", name, r.Status)
		}
		return io.ReadAll(io.LimitReader(r.Body, 64<<20))
	}
	return nil, fmt.Errorf("release %s has no %s", rel.TagName, name)
}

// Tags are repo's tags with the commits they point at.
func (g GitHub) Tags(ctx context.Context, repo string) (map[string]string, error) {
	var tags []struct {
		Name   string `json:"name"`
		Commit struct {
			SHA string `json:"sha"`
		} `json:"commit"`
	}
	if err := g.json(ctx, "/repos/"+repo+"/tags?per_page=100", &tags); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, t := range tags {
		out[t.Name] = t.Commit.SHA
	}
	return out, nil
}

// CommitTime is when commit was made: the start of a trusted tag's hold.
func (g GitHub) CommitTime(ctx context.Context, repo, commit string) (time.Time, error) {
	var c struct {
		Commit struct {
			Committer struct {
				Date string `json:"date"`
			} `json:"committer"`
		} `json:"commit"`
	}
	if err := g.json(ctx, "/repos/"+repo+"/commits/"+commit, &c); err != nil {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339, c.Commit.Committer.Date)
}

func (g GitHub) json(ctx context.Context, url string, v any) error {
	r, err := g.get(ctx, url, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer r.Body.Close()
	if r.StatusCode == http.StatusNotFound {
		return fmt.Errorf("%s: GitHub answered %s: %w", url, r.Status, fs.ErrNotExist)
	}
	if r.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: GitHub answered %s", url, r.Status)
	}
	return json.NewDecoder(r.Body).Decode(v)
}

func cmpOr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
