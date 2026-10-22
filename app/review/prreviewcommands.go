package review

import (
	"cmp"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// commands triage, reword, sum up and post a review session's comments.
func (x *PRReview) commands() []registry.Command {
	return slices.Concat(x.fetchCommands(), x.triageCommands(), x.reworkCommands(), x.moveCommands(), x.postCommands())
}

// inReview is whether the session is reviewing a pull request.
func (x *PRReview) inReview(a registry.Args) bool {
	se, err := x.session(a)
	return err == nil && reviewPR(se) != ""
}

// onItem runs fn on the review's {item}, the selected one by default.
func (x *PRReview) onItem(fn func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error) func(registry.Ctx, registry.Args) error {
	return func(ctx registry.Ctx, a registry.Args) error {
		se, err := x.session(a)
		if err != nil {
			return err
		}
		id := cmp.Or(a.String("item"), x.form(se.ID).sel)
		for _, it := range x.items(se) {
			if it.id == id {
				return fn(ctx, se, it, a)
			}
		}
		return fmt.Errorf("no comment %q in the review", id)
	}
}

// fetchCommands fetch what the author has pushed and ask for a review of it.
func (x *PRReview) fetchCommands() []registry.Command {
	return []registry.Command{
		{ID: "review.pr.fetch", Title: "Fetch what the author has pushed to session {id}'s pull request (the one in front by default)", Available: x.inReview,
			Run: x.fetch},
		{ID: "review.pr.recheck", Title: "Ask session {id}'s agent to review the commits the last fetch brought, and where its earlier comments stand",
			Available: x.inReview, Run: x.recheck},
	}
}

// triageCommands keep, drop, rate and edit the review's comments.
func (x *PRReview) triageCommands() []registry.Command {
	return []registry.Command{
		{ID: "review.pr.pick", Title: "Keep the review's {item} (the selected one by default) for the review you submit, or drop it: {state} keep, drop, or '' undecided",
			Available: x.inReview, Run: x.onItem(func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				s := a.String("state")
				if !slices.Contains([]string{core.PickKeep, core.PickDrop, ""}, s) {
					return fmt.Errorf("a comment is kept or dropped, not %q", s)
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.State = s })
				x.advance(se, it.id)
				return x.settle(ctx, se, it, s)
			})},
		{ID: "review.pr.include", Title: "Put the review's {item} in the review you submit, or leave it out with {on} false", Available: x.inReview,
			Run: x.onItem(func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				s := core.PickKeep
				if v, ok := a["on"].(bool); ok && !v {
					s = core.PickDrop
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.State = s })
				return x.settle(ctx, se, it, s)
			})},
		{ID: "review.pr.severity", Title: "Rate the review's {item}: {severity} critical, high, medium, low or nit; '' for the reviewer's", Available: x.inReview,
			Run: x.onItem(func(_ registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				s := a.String("severity")
				if s != "" && !slices.Contains(core.Severities, s) {
					return fmt.Errorf("no severity %q", s)
				}
				if s == it.sev {
					s = ""
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Severity = s })
				return nil
			})},
		{ID: "review.pr.edit", Title: "Edit what the author gets of the review's {item}: {body}, or '' for the reviewer's words; without a body, open it to edit",
			Available: x.inReview, Run: x.onItem(func(_ registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				if _, ok := a["body"]; !ok {
					f := x.form(se.ID)
					f.sel, f.editing, f.edit = it.id, it.id, it.words()
					return nil
				}
				body := strings.TrimSpace(a.String("body"))
				if body == strings.TrimSpace(it.body) {
					body = ""
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Body = body })
				x.form(se.ID).editing = ""
				return nil
			})},
	}
}

// reworkCommands ask the agent to reword a comment or draft the summary.
func (x *PRReview) reworkCommands() []registry.Command {
	return []registry.Command{
		{ID: "review.pr.rework", Title: "Ask the agent to reword the review's {item} for the author: {how} shorter, friendlier, suggestion or detail, or {ask} in your words",
			Available: x.inReview, Run: x.onItem(x.rework)},
		{ID: "review.pr.rework.use", Title: "Use the agent's rewording of the review's {item}", Available: x.inReview,
			Run: x.onItem(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				text, ok := reworded(se, it.pick)
				if !ok {
					return errors.New("the agent hasn't reworded it yet")
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Body, p.Rework, p.ReworkAt = text, "", 0 })
				return x.endRework(ctx, se, it)
			})},
		{ID: "review.pr.rework.drop", Title: "Keep your words for the review's {item}, not the agent's rewording", Available: x.inReview,
			Run: x.onItem(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Rework, p.ReworkAt = "", 0 })
				return x.endRework(ctx, se, it)
			})},
		{ID: "review.pr.summary", Title: "Ask session {id}'s agent to draft the review's summary from the comments kept; it fills the summary box",
			Available: x.inReview, Run: x.askSummary},
	}
}

// moveCommands select a comment and show it in the diff.
func (x *PRReview) moveCommands() []registry.Command {
	return []registry.Command{
		{ID: "review.pr.next", Title: "Select the review's next comment, or the one before with {back}", Available: x.inReview,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, err := x.session(a)
				if err != nil {
					return err
				}
				x.step(se, a.Bool("back"))
				return nil
			}},
		{ID: "review.pr.show", Title: "Show the review's {item} (the selected one by default) at its line in the diff", Available: x.inReview,
			Run: x.onItem(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				x.form(se.ID).sel = it.id
				return x.h.Run(ctx, "diff.open", registry.Args{"path": filepath.Join(reviewDir(se, it.at.Project), filepath.FromSlash(it.at.File)), "line": it.at.Line})
			})},
	}
}

// postCommands preview and submit the review.
func (x *PRReview) postCommands() []registry.Command {
	return []registry.Command{
		{ID: "review.pr.preview", Title: "Preview the review as it would be posted with {verdict}: approve, changes or comment; '' closes it", Available: x.inReview,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, err := x.session(a)
				if err != nil {
					return err
				}
				v := a.String("verdict")
				if v != "" && prVerdictWord(v) == "" {
					return fmt.Errorf("a review is approve, changes or comment, not %q", v)
				}
				f := x.form(se.ID)
				f.preview, f.focused = v, false
				return nil
			}},
		{ID: "review.pr.submit", Title: "Submit your review of session {id}'s pull request: {verdict} approve, changes or comment, with {body} or the summary, and the comments kept",
			Available: x.inReview, Run: x.submit},
	}
}
