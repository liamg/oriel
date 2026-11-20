package gitlab

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/liamg/oriel/registry"
)

// emojiNames are GitLab's names for emoji. The first 8 are the reactions Oriel offers, GitHub's own; the rest are
// often awarded, so they show as emoji rather than as :name:.
var emojiNames = []struct{ name, emoji string }{
	{"thumbsup", "👍"}, {"thumbsdown", "👎"}, {"smile", "😄"}, {"tada", "🎉"}, {"confused", "😕"}, {"heart", "❤️"},
	{"rocket", "🚀"}, {"eyes", "👀"},
	{"fire", "🔥"}, {"100", "💯"}, {"clap", "👏"}, {"pray", "🙏"}, {"joy", "😂"}, {"white_check_mark", "✅"},
	{"heavy_check_mark", "✔️"}, {"ok_hand", "👌"}, {"sparkles", "✨"}, {"thinking", "🤔"}, {"raised_hands", "🙌"},
	{"muscle", "💪"}, {"star", "⭐"}, {"warning", "⚠️"}, {"x", "❌"}, {"slight_smile", "🙂"}, {"smiley", "😃"},
	{"laughing", "😆"}, {"wave", "👋"}, {"bulb", "💡"}, {"bug", "🐛"},
}

// Reactions are the emoji Oriel offers on a note.
func Reactions() []string {
	var out []string
	for _, e := range emojiNames[:8] {
		out = append(out, e.emoji)
	}
	return out
}

// emojiOf is the emoji GitLab names name; one with none here, a custom emoji among them, is ":name:".
func emojiOf(name string) string {
	for _, e := range emojiNames {
		if e.name == name {
			return e.emoji
		}
	}
	return ":" + name + ":"
}

// nameOf is GitLab's name for emoji: emojiOf's inverse, "" for one it doesn't know.
func nameOf(emoji string) string {
	for _, e := range emojiNames {
		if e.emoji == emoji {
			return e.name
		}
	}
	if n, ok := strings.CutPrefix(emoji, ":"); ok && len(n) > 1 && strings.HasSuffix(n, ":") {
		return strings.TrimSuffix(n, ":")
	}
	return ""
}

// Award is an emoji a user awarded a note.
type Award struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	User User   `json:"user"`
}

// Awards are a merge request note's award emoji, the first 100.
func (c Client) Awards(ctx context.Context, path string, iid, note int) ([]Award, error) {
	var as []Award
	return as, c.call(ctx, "GET", fmt.Sprintf("/projects/%s/merge_requests/%d/notes/%d/award_emoji?per_page=100", project(path), iid, note), nil, &as)
}

// noteAwardsQuery reads a page of an MR's notes' award emoji, with who the token's owner is.
const noteAwardsQuery = `query($path: ID!, $iid: String!, $after: String) { currentUser { username } ` +
	`project(fullPath: $path) { mergeRequest(iid: $iid) { notes(first: 100, after: $after) { ` +
	`nodes { id awardEmoji { nodes { name user { username } } } } pageInfo { hasNextPage endCursor } } } } }`

// NoteAwards are every note's award emoji on a merge request, by note id, read 100 notes at a time; and the token's
// owner's username, to tell theirs.
func (c Client) NoteAwards(ctx context.Context, path string, iid int) (map[int][]Award, string, error) {
	out := map[int][]Award{}
	vars := map[string]any{"path": path, "iid": strconv.Itoa(iid)}
	for {
		var res struct {
			Data struct {
				CurrentUser User `json:"currentUser"`
				Project     *struct {
					MergeRequest *struct {
						Notes struct {
							Nodes []struct {
								ID         string `json:"id"`
								AwardEmoji struct {
									Nodes []Award `json:"nodes"`
								} `json:"awardEmoji"`
							} `json:"nodes"`
							PageInfo struct {
								Next   bool   `json:"hasNextPage"`
								Cursor string `json:"endCursor"`
							} `json:"pageInfo"`
						} `json:"notes"`
					} `json:"mergeRequest"`
				} `json:"project"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}
		if err := (Client{Do: c.GraphQL}).call(ctx, "POST", "", map[string]any{"query": noteAwardsQuery, "variables": vars}, &res); err != nil {
			return nil, "", err
		}
		if len(res.Errors) > 0 {
			return nil, "", &Error{Status: 422, Message: res.Errors[0].Message}
		}
		if res.Data.Project == nil || res.Data.Project.MergeRequest == nil {
			return nil, "", &Error{Status: 404, Message: fmt.Sprintf("no merge request !%d in %s", iid, path)}
		}
		notes := res.Data.Project.MergeRequest.Notes
		for _, n := range notes.Nodes {
			// A note's global id: gid://gitlab/DiffNote/123.
			if id, err := strconv.Atoi(n.ID[strings.LastIndex(n.ID, "/")+1:]); err == nil {
				out[id] = n.AwardEmoji.Nodes
			}
		}
		if !notes.PageInfo.Next {
			return out, res.Data.CurrentUser.Username, nil
		}
		vars["after"] = notes.PageInfo.Cursor
	}
}

// reactions are a note's awards by emoji: those Oriel offers in their order, then the rest as they were first awarded.
func reactions(as []Award, me string) []registry.PRReaction {
	var out []registry.PRReaction
	for _, a := range as {
		e := emojiOf(a.Name)
		i := slices.IndexFunc(out, func(r registry.PRReaction) bool { return r.Emoji == e })
		if i < 0 {
			out = append(out, registry.PRReaction{Emoji: e})
			i = len(out) - 1
		}
		out[i].Count++
		out[i].Mine = out[i].Mine || a.User.Username == me
		out[i].Who = append(out[i].Who, a.User.Username)
	}
	offered := Reactions()
	slices.SortStableFunc(out, func(a, b registry.PRReaction) int {
		return rank(offered, a.Emoji) - rank(offered, b.Emoji)
	})
	return out
}

func rank(offered []string, emoji string) int {
	if i := slices.Index(offered, emoji); i >= 0 {
		return i
	}
	return len(offered)
}

// ReactNote awards a merge request's note emoji as the user, or with on false takes theirs back. Having it already,
// or not having it to take back, is no error.
func (c Client) ReactNote(ctx context.Context, path string, pr registry.PR, note, emoji string, on bool) error {
	name := nameOf(emoji)
	if name == "" {
		return fmt.Errorf("Oriel has no GitLab name for %s", emoji)
	}
	n, err := strconv.Atoi(note)
	if err != nil {
		return fmt.Errorf("no note %q", note)
	}
	me, err := c.Me(ctx)
	if err != nil {
		return err
	}
	as, err := c.Awards(ctx, path, pr.Number, n)
	if err != nil {
		return err
	}
	i := slices.IndexFunc(as, func(a Award) bool { return a.Name == name && a.User.Username == me.Username })
	where := fmt.Sprintf("/projects/%s/merge_requests/%d/notes/%d/award_emoji", project(path), pr.Number, n)
	switch {
	case on && i < 0:
		return c.call(ctx, "POST", where, map[string]string{"name": name}, nil)
	case !on && i >= 0:
		return c.call(ctx, "DELETE", fmt.Sprintf("%s/%d", where, as[i].ID), nil, nil)
	}
	return nil
}
