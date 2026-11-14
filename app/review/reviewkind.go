//oriel:kind review

package review

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// reviewPR is the pull request a review session reads; "" for any other session.
func reviewPR(se core.SessionState) string {
	d, _ := core.DataOf[core.ReviewData](se)
	return d.PR
}

// reviewDraft is the review the user is writing of a review session's pull request; nil for none.
func reviewDraft(se core.SessionState) *core.ReviewDraft {
	d, _ := core.DataOf[core.ReviewData](se)
	return d.Draft
}

// contributeKind registers the review kind: what it starts from, its card's PR, and its own PR tab in place of
// Raise PR's.
func (x *PRReview) contributeKind() {
	h := x.h
	x.Add(h.Contribute(registry.KindEntry{SessionKind: registry.Kind{KindSpec: host.ReviewKind,
		PrepareFunc: x.prepare,
		ChipsFunc: func(se core.SessionState) []registry.Chip {
			if pr := reviewPR(se); pr != "" {
				return []registry.Chip{{Text: kit.PRLabel(pr), Help: kit.TabsHelp, Link: pr}}
			}
			return nil
		}}}))
	x.Add(h.Contribute(registry.SessionDone{Name: "review.approved", Done: approved}))
	x.Add(h.Contribute(registry.SessionUnsaved{Name: "review.unsent", Unsaved: x.unsent}))
	x.Add(h.Contribute(registry.Service{Name: kit.ReviewsService, Impl: reviewsService{x}}))
	x.Add(h.Contribute(uireg.SlotView{Name: "review.pr", Slot: kit.PRView, Kinds: []core.Kind{core.KindReview}, View: reviewTab{x}}))
}

// approved says a review is done once the last review the user sent of its PR approved it.
func approved(se core.SessionState) string {
	if d := reviewDraft(se); d != nil && len(d.Sent) > 0 && d.Sent[len(d.Sent)-1].Verdict == registry.VerdictApprove {
		return "You approved " + kit.Label(se) + "."
	}
	return ""
}

// unsent says a review has comments kept, or a summary of the user's, not yet posted to a pull request still open.
func (x *PRReview) unsent(se core.SessionState) string {
	pr := reviewPR(se)
	if pr == "" || x.ended(pr) {
		return ""
	}
	d := x.draft(se)
	n := 0
	for _, it := range reviewItems(se, d) {
		if it.kept() {
			n++
		}
	}
	summary := strings.TrimSpace(d.Summary) != "" && !isAutoSummary(d.Summary)
	switch {
	case n > 0 && summary:
		return fmt.Sprintf("Your review isn't sent: %s and its summary, lost.", kit.Counted(n, "comment"))
	case n > 0:
		return fmt.Sprintf("Your review isn't sent: %s, lost.", kit.Counted(n, "comment"))
	case summary:
		return "Your review isn't sent: its summary, lost."
	}
	return ""
}

// reviewsService is the review sessions' pull requests, for other features.
type reviewsService struct{ x *PRReview }

func (r reviewsService) PR(se core.SessionState) string { return reviewPR(se) }

func (r reviewsService) Session(url string) (core.Session, bool) { return prSession(r.x.h, url) }

// prepare finds where a review's pull request is fetched from, and names it and tells its agent about it, as its
// host has it.
func (x *PRReview) prepare(ctx registry.Ctx, spec *core.SessionSpec) error {
	if err := x.prCheckout(ctx, spec); err != nil {
		return err
	}
	x.prName(spec)
	x.prContext(ctx, spec)
	return nil
}

// reviewTab is a review session's pr tab: its pull request to triage and review.
type reviewTab struct{ x *PRReview }

func (v reviewTab) Build(uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "pr", Fn: func(c *ui.Ctx) {
		se, err := v.x.session(nil)
		c.Column("prtab", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: c.Theme().C(theme.BgBase)}, func(c *ui.Ctx) {
			if err == nil {
				v.x.page(c, se)
			}
		})
	}}
}

// Title is "Review · " and the session's name.
func (v reviewTab) Title(string) string {
	se, err := v.x.session(nil)
	if err != nil {
		return ""
	}
	return "Review · " + kit.Label(se)
}

// prCheckout asks a review's source where its PR is fetched from, unless the spec says already.
func (x *PRReview) prCheckout(ctx context.Context, spec *core.SessionSpec) error {
	if spec.PR == "" || spec.PRRef != "" {
		return nil
	}
	src, ok := kit.SitesOf(x.h).URL(spec.PR)
	if !ok || src.Checkout == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	c, err := registry.CheckoutFor(ctx, src, spec.PR)
	if err != nil {
		return fmt.Errorf("%s couldn't say where to fetch %s from: %w", src.Label(), spec.PR, err)
	}
	spec.PRRemote, spec.PRRef = c.Remote, c.Ref
	return nil
}

// prName names a review that has no name after its pull request, as its host writes one: "api#812".
func (x *PRReview) prName(spec *core.SessionSpec) {
	if spec.PR == "" || spec.Name != "" || spec.Title != "" || spec.PRRemote == "" {
		return
	}
	src, ok := kit.SitesOf(x.h).URL(spec.PR)
	if !ok {
		return
	}
	_, p, _ := registry.ParseRemote(spec.PR)
	n, ok := src.PRNumber(p)
	if !ok {
		return
	}
	repo := path.Base(strings.Trim(spec.PRRemote, "/"))
	spec.Name, spec.Title = fmt.Sprintf("%s-%d", repo, n), src.Ref(repo, n)
}

// prContext tells a review's agent what its pull request is, as its host says: the title, who wrote it, and the
// branch it merges into, which its diff and Changed are against.
func (x *PRReview) prContext(ctx context.Context, spec *core.SessionSpec) {
	if spec.PR == "" || spec.Context != "" && spec.PRBase != "" {
		return
	}
	src, ok := kit.SitesOf(x.h).URL(spec.PR)
	if !ok || src.Lookup == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	d, err := registry.LookupReview(ctx, src, spec.PR)
	if err != nil {
		x.h.Log().Warn("review lookup", "pr", spec.PR, "err", err)
		return
	}
	spec.PRBase = cmp.Or(spec.PRBase, d.Base)
	if spec.Context != "" {
		return
	}
	var lines []string
	if t := strings.TrimSpace(d.Request.Title); t != "" {
		lines = append(lines, fmt.Sprintf("%q, by %s.", t, cmp.Or(d.Request.Author, "someone")))
	}
	if d.Base != "" {
		lines = append(lines, fmt.Sprintf("It merges %s into %s: diff against origin/%s.", cmp.Or(d.Head, "its branch"), d.Base, d.Base))
	}
	spec.Context = strings.Join(lines, " ")
}
