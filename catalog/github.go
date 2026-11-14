package catalog

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/liamg/oriel/gh"
)

// GitHub is the little of GitHub's REST API the registry needs: org membership, release assets, a repo's tags.
// Token reads a private repo; none is needed for public ones.
type GitHub struct {
	Base   string // https://api.github.com if ""
	Token  string
	Auth   func(ctx context.Context) string // the token, asked for each request, when Token is ""
	Client *http.Client
}

// rest is GitHub's REST API with the registry's token, by gh's rules.
func (g GitHub) rest() gh.REST {
	return gh.REST{Base: g.Base, Client: g.Client, Token: func(ctx context.Context) (string, error) {
		if g.Token == "" && g.Auth != nil {
			return g.Auth(ctx), nil
		}
		return g.Token, nil
	}}
}

// Member says whether user is org itself or one of its members.
func (g GitHub) Member(ctx context.Context, org, user string) (bool, error) {
	if strings.EqualFold(org, user) {
		return true, nil
	}
	r, err := g.rest().Get(ctx, "/orgs/"+org+"/members/"+user, "application/vnd.github+json")
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

// Asset reads the asset name of repo's release for tag.
func (g GitHub) Asset(ctx context.Context, repo, tag, name string) ([]byte, error) {
	rel, err := g.rest().ReleaseByTag(ctx, repo, tag)
	if err != nil {
		return nil, notExist(err)
	}
	return g.asset(ctx, rel, name)
}

// LatestAsset reads the asset name of repo's newest release that has it: the registry's index. With no such
// release, or no repo to see, the error is fs.ErrNotExist.
func (g GitHub) LatestAsset(ctx context.Context, repo, name string) ([]byte, error) {
	rels, err := g.rest().Releases(ctx, repo, 20)
	if err != nil {
		return nil, notExist(err)
	}
	for _, rel := range rels {
		if _, ok := rel.Asset(name); ok && !rel.Draft {
			return g.asset(ctx, rel, name)
		}
	}
	return nil, fmt.Errorf("%s has no release with %s: %w", repo, name, fs.ErrNotExist)
}

func (g GitHub) asset(ctx context.Context, rel gh.Release, name string) ([]byte, error) {
	a, ok := rel.Asset(name)
	if !ok {
		return nil, fmt.Errorf("release %s has no %s", rel.Tag, name)
	}
	b, err := g.rest().ReadAsset(ctx, a, 64<<20)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return b, nil
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
	if err := g.rest().JSON(ctx, url, v); err != nil {
		return fmt.Errorf("%s: %w", url, notExist(err))
	}
	return nil
}

// notExist is GitHub's 404 as fs.ErrNotExist too, which the registry's callers ask for.
func notExist(err error) error {
	if gh.NotFound(err) {
		return fmt.Errorf("%w: %w", err, fs.ErrNotExist)
	}
	return err
}
