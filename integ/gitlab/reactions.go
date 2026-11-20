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
