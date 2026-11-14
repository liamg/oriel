package review

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// reworkAsks are the rewordings a comment can be asked for in a word.
var reworkAsks = map[string]string{
	"shorter":    "Make it shorter: the point in a sentence or two.",
	"friendlier": "Make it friendlier: suggest rather than instruct, as a colleague would.",
	"suggestion": "Add a GitHub suggestion block with the fix, if it's small and you're sure of it.",
	"detail":     "Add the detail the author needs: why it matters and where it bites.",
}

// rework asks the agent to reword an item for the author: in its own thread, else in one opened on the item's line.
func (x *PRReview) rework(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
	how := strings.TrimSpace(a.String("ask"))
	if how == "" {
		var ok bool
		if how, ok = reworkAsks[a.String("how")]; !ok {
			return fmt.Errorf("rework is shorter, friendlier, suggestion or detail, or an ask in your words; not %q", a.String("how"))
		}
	}
	body := "This is a rework request: reword this review comment before it goes to the PR's author. " + how +
		" Reply with the new comment only, no preamble, and leave this thread open.\n\n" + quote(it.words())
	w, err := kit.SessionCalls(x.h)
	if err != nil {
		return err
	}
	req := host.CommentReq{Session: se.ID, Body: body}
	at := 0
	if it.thread != "" && !it.mine {
		req.Thread = it.thread
		if t, ok := se.Thread(it.thread); ok {
			at = len(t.Comments) + 1
		}
	} else {
		an := it.at
		req.Anchor, req.Title, at = &an, "Reword: "+it.title, 1
	}
	f := x.form(se.ID)
	f.sel, f.asking, f.ask = it.id, "", ""
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.rework." + it.id, Doing: "Asking the agent"},
		func(ctx context.Context) (core.ThreadID, error) { return w.Comment(ctx, req) },
		func(_ registry.Ctx, t core.ThreadID) error {
			x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Rework, p.ReworkAt = t, at })
			return nil
		})
}

// endRework resolves the thread a rewording was asked for in, when it was opened for it.
func (x *PRReview) endRework(ctx registry.Ctx, se core.SessionState, it reviewItem) error {
	if it.pick.Rework == "" || it.pick.Rework == it.thread {
		return nil
	}
	return x.h.Run(ctx, "request.resolve", registry.Args{"id": string(se.ID), "thread": string(it.pick.Rework)})
}

// reworded is the agent's rewording of a pick, once it has answered.
func reworded(se core.SessionState, p core.ReviewPick) (string, bool) {
	t, ok := se.Thread(p.Rework)
	if p.Rework == "" || !ok {
		return "", false
	}
	for _, c := range t.Comments[min(p.ReworkAt, len(t.Comments)):] {
		if c.Author == core.AuthorAgent && !c.Progress {
			return strings.TrimSpace(c.Body), true
		}
	}
	return "", false
}

func quote(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i, l := range lines {
		lines[i] = "> " + l
	}
	return strings.Join(lines, "\n")
}

// askSummary asks the agent to draft the summary (the review pack's summary request); watch fills the box with it.
func (x *PRReview) askSummary(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	var lines []string
	for _, it := range x.items(se) {
		if it.kept() {
			lines = append(lines, fmt.Sprintf("- %s `%s:%d`: %s", it.severity(), it.at.File, it.at.Line, kit.FirstLine(it.words())))
		}
	}
	body := "This is a summary request: the review about to go to the author of " + kit.Label(se) + " has these comments:\n\n" +
		cmp.Or(strings.Join(lines, "\n"), "(none: it approves)") + "\n\nReply with the summary only, and resolve this thread."
	w, err := kit.SessionCalls(x.h)
	if err != nil {
		return err
	}
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.summary." + string(se.ID), Doing: "Asking the agent for a summary"},
		func(ctx context.Context) (core.ThreadID, error) {
			return w.Comment(ctx, host.CommentReq{Session: se.ID, Title: "Draft the review summary", Body: body})
		},
		func(_ registry.Ctx, t core.ThreadID) error {
			x.setDraft(se.ID, func(d *core.ReviewDraft) { d.SummaryAsk = t })
			return nil
		})
}
