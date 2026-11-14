package kit

import (
	"context"
	"errors"

	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/registry"
)

// GitHubService is what GitHub's code host offers beyond the code host contract.
const GitHubService = "github"

// GitHub is GitHub's own API, as the user is signed in to it.
type GitHub interface {
	// SearchPRs finds pull requests across github.com: gh.SearchMine, gh.SearchReview.
	SearchPRs(ctx context.Context, query string) ([]gh.Found, error)
}

// GitHubOf is the window's GitHub, refusing a window without it.
func GitHubOf(r registry.Registry) (GitHub, error) {
	if x, ok := registry.ServiceOf[GitHub](r, GitHubService); ok {
		return x, nil
	}
	return nil, errors.New("GitHub isn't a code host in this window")
}
