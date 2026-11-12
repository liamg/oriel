package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// Reactions are the emoji people reacted with, by GitHub's name for each ("+1", "heart"…), and how many.
type Reactions map[string]int

// ReactionNames are GitHub's reactions, in the order it shows them.
var ReactionNames = []string{"+1", "-1", "laugh", "hooray", "confused", "heart", "rocket", "eyes"}

// ReactionEmoji is the emoji GitHub shows for each reaction.
var ReactionEmoji = map[string]string{"+1": "👍", "-1": "👎", "laugh": "😄", "hooray": "🎉", "confused": "😕", "heart": "❤️",
	"rocket": "🚀", "eyes": "👀"}

// UnmarshalJSON keeps GitHub's reaction counts, leaving out its url, its total_count and those at 0.
func (r *Reactions) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := Reactions{}
	for _, name := range ReactionNames {
		if n, ok := raw[name].(float64); ok && n > 0 {
			out[name] = int(n)
		}
	}
	*r = out
	return nil
}

// PRReactions are a pull request's own reactions: its issue's.
func (a API) PRReactions(ctx context.Context, repo Repo, number int) (Reactions, error) {
	var out struct {
		Reactions Reactions `json:"reactions"`
	}
	err := a.T.Do(ctx, "GET", repoPath(repo, fmt.Sprintf("issues/%d", number)), nil, &out)
	return out.Reactions, err
}

// ReactPR reacts to a pull request with content, one of ReactionNames. Reacting again with the same is a no-op.
func (a API) ReactPR(ctx context.Context, repo Repo, number int, content string) error {
	return a.T.Do(ctx, "POST", repoPath(repo, fmt.Sprintf("issues/%d/reactions", number)), map[string]string{"content": content}, nil)
}

// UnreactPR takes back the signed-in user's content reaction to a pull request: GitHub deletes a reaction by its id,
// so the PR's reactions of that kind are listed for the user's own. Having none is no error.
func (a API) UnreactPR(ctx context.Context, repo Repo, number int, content string) error {
	me, err := a.Me(ctx)
	if err != nil {
		return err
	}
	var rs []struct {
		ID   int64 `json:"id"`
		User struct {
			Login string `json:"login"`
		} `json:"user"`
	}
	path := repoPath(repo, fmt.Sprintf("issues/%d/reactions", number))
	if err := a.T.Do(ctx, "GET", path+"?content="+url.QueryEscape(content)+"&per_page=100", nil, &rs); err != nil {
		return err
	}
	for _, r := range rs {
		if r.ID != 0 && strings.EqualFold(r.User.Login, me) {
			if err := a.T.Do(ctx, "DELETE", fmt.Sprintf("%s/%d", path, r.ID), nil, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// ReactionName is GitHub's name for emoji; "" for one GitHub doesn't offer.
func ReactionName(emoji string) string {
	for name, e := range ReactionEmoji {
		if e == emoji {
			return name
		}
	}
	return ""
}
