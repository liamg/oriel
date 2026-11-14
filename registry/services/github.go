package services

import (
	"context"

	"github.com/liamg/oriel/gh"
)

// GitHubService is what GitHub's code host offers beyond the code host contract.
const GitHubService = "github"

// GitHub is GitHub's own API, as the user is signed in to it.
type GitHub interface {
	// SearchPRs finds pull requests across github.com: gh.SearchMine, gh.SearchReview.
	SearchPRs(ctx context.Context, query string) ([]gh.Found, error)
}
