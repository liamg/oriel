package github

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

// Reaction is how many people reacted to a comment with one of ReactionNames, Mine when the user is one of them.
// Who are the first 10 of them, by login: enough to name in a tooltip.
type Reaction struct {
	Name  string
	Count int
	Mine  bool
	Who   []string
}

// ReactedNode is a comment's GraphQL node, which a reaction is added to, and its reactions in ReactionNames' order.
type ReactedNode struct {
	Node      string
	Reactions []Reaction
}

// CommentReactions are the reactions on a PR's comments, by their REST ids: its line comments, its conversation's
// and its reviews' bodies. Each kind numbers its own, so they're kept apart.
type CommentReactions struct {
	Line, Issue, Review map[int64]ReactedNode
}

// reactionEnums are GraphQL's names for ReactionNames.
var reactionEnums = map[string]string{"+1": "THUMBS_UP", "-1": "THUMBS_DOWN", "laugh": "LAUGH", "hooray": "HOORAY", "confused": "CONFUSED",
	"heart": "HEART", "rocket": "ROCKET", "eyes": "EYES"}

const reactedFields = `databaseId id reactionGroups { content viewerHasReacted reactors(first: 10) { totalCount nodes { ... on Actor { login } } } }`

// The first 100 of each, as resolvedQuery reads its threads.
const commentReactionsQuery = `query($owner: String!, $name: String!, $number: Int!) { repository(owner: $owner, name: $name) { pullRequest(number: $number) { ` +
	`comments(first: 100) { nodes { ` + reactedFields + ` } } ` +
	`reviews(first: 100) { nodes { ` + reactedFields + ` comments(first: 100) { nodes { ` + reactedFields + ` } } } } } } }`

type reactedJSON struct {
	DatabaseID int64  `json:"databaseId"`
	ID         string `json:"id"`
	Groups     []struct {
		Content  string `json:"content"`
		Mine     bool   `json:"viewerHasReacted"`
		Reactors struct {
			Total int `json:"totalCount"`
			Nodes []struct {
				Login string `json:"login"`
			} `json:"nodes"`
		} `json:"reactors"`
	} `json:"reactionGroups"`
}

func (r reactedJSON) node() ReactedNode {
	n := ReactedNode{Node: r.ID}
	for _, name := range ReactionNames {
		for _, g := range r.Groups {
			if g.Content != reactionEnums[name] || g.Reactors.Total == 0 {
				continue
			}
			r := Reaction{Name: name, Count: g.Reactors.Total, Mine: g.Mine}
			for _, a := range g.Reactors.Nodes {
				r.Who = append(r.Who, a.Login)
			}
			n.Reactions = append(n.Reactions, r)
		}
	}
	return n
}

func (a API) CommentReactions(ctx context.Context, repo Repo, number int) (CommentReactions, error) {
	type nodes[T any] struct {
		Nodes []T `json:"nodes"`
	}
	var out struct {
		Data struct {
			Repository struct {
				PullRequest struct {
					Comments nodes[reactedJSON] `json:"comments"`
					Reviews  nodes[struct {
						reactedJSON
						Comments nodes[reactedJSON] `json:"comments"`
					}] `json:"reviews"`
				} `json:"pullRequest"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": commentReactionsQuery, "variables": map[string]any{"owner": repo.Owner, "name": repo.Name, "number": number}}
	if err := a.T.Do(ctx, "POST", "graphql", in, &out); err != nil {
		return CommentReactions{}, err
	}
	if len(out.Errors) > 0 {
		return CommentReactions{}, &Error{Status: 422, Message: out.Errors[0].Message}
	}
	res := CommentReactions{Line: map[int64]ReactedNode{}, Issue: map[int64]ReactedNode{}, Review: map[int64]ReactedNode{}}
	pr := out.Data.Repository.PullRequest
	for _, c := range pr.Comments.Nodes {
		res.Issue[c.DatabaseID] = c.node()
	}
	for _, rv := range pr.Reviews.Nodes {
		res.Review[rv.DatabaseID] = rv.node()
		for _, c := range rv.Comments.Nodes {
			res.Line[c.DatabaseID] = c.node()
		}
	}
	return res, nil
}

// ReactNode adds the user's name reaction (ReactionNames) to a comment's node, or with on false takes it back.
func (a API) ReactNode(ctx context.Context, node, name string, on bool) error {
	enum, ok := reactionEnums[name]
	if !ok {
		return fmt.Errorf("GitHub has no %q reaction", name)
	}
	op := "addReaction"
	if !on {
		op = "removeReaction"
	}
	var out struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	in := map[string]any{"query": "mutation($id: ID!, $content: ReactionContent!) { " + op + "(input: {subjectId: $id, content: $content}) { reaction { content } } }",
		"variables": map[string]any{"id": node, "content": enum}}
	if err := a.T.Do(ctx, "POST", "graphql", in, &out); err != nil {
		return err
	}
	if len(out.Errors) > 0 {
		return &Error{Status: 422, Message: out.Errors[0].Message}
	}
	return nil
}
