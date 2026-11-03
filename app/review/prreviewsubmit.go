package review

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// severityColors run from grey for a nit to red for a critical finding.
var severityColors = map[string]string{"nit": "9E9E9E", "low": "2E7D32", "medium": "F9A825", "high": "EF6C00", "critical": "C62828"}

// severityBadge is a shields.io badge of a comment's severity, as markdown; "" for one it doesn't know.
func severityBadge(sev string) string {
	color, ok := severityColors[sev]
	if !ok {
		return ""
	}
	return "![" + sev + "](https://img.shields.io/badge/severity-" + sev + "-" + color + ")"
}

// posted is the text a kept item is posted with: its words, headed by its severity's badge when badges are on.
func (x *PRReview) posted(it reviewItem) string {
	body := strings.TrimSpace(it.words())
	if on, _, _ := x.h.Options().Get(optSeverityBadges, registry.At{}); on != true {
		return body
	}
	if b := severityBadge(it.severity()); b != "" && !strings.HasPrefix(body, b) {
		return b + "\n\n" + body
	}
	return body
}

// suggestedVerdict is the verdict the kept comments call for: changes for any high or worse, a comment for any at
// all, else approval.
func suggestedVerdict(items []reviewItem) string {
	v := registry.VerdictApprove
	for _, it := range items {
		if !it.kept() {
			continue
		}
		if core.SeverityRank(it.severity()) <= core.SeverityRank("high") {
			return registry.VerdictChanges
		}
		v = registry.VerdictComment
	}
	return v
}

// toPost are the comments a review would post: the kept items, as posted.
func (x *PRReview) toPost(se core.SessionState) ([]reviewItem, []registry.ReviewComment) {
	var items []reviewItem
	var out []registry.ReviewComment
	for _, it := range x.items(se) {
		if !it.kept() {
			continue
		}
		c := registry.ReviewComment{Path: it.at.File, Line: it.at.Line, Old: it.at.Side == "old", Body: x.posted(it)}
		if it.at.EndLine > it.at.Line {
			c.StartLine, c.Line = it.at.Line, it.at.EndLine
		}
		items, out = append(items, it), append(out, c)
	}
	return items, out
}

func (x *PRReview) submit(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	src, ok := x.site(se)
	if !ok {
		return fmt.Errorf("no code host has %s", reviewPR(se))
	}
	verdict := a.String("verdict")
	if prVerdictWord(verdict) == "" {
		return fmt.Errorf("a review is approve, changes or comment, not %q", verdict)
	}
	f := x.form(se.ID)
	r := registry.ReviewSubmit{Event: verdict, Body: strings.TrimSpace(cmp.Or(a.String("body"), f.body))}
	if p, ok := prProject(se); ok {
		r.Commit = p.Commit
	}
	sent, comments := x.toPost(se)
	r.Comments = comments
	if verdict != registry.VerdictApprove && r.Body == "" && len(r.Comments) == 0 {
		return errors.New("say something: a summary, or a comment kept")
	}
	f.preview = ""
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.submit." + string(se.ID), Doing: prVerdictWord(verdict) + " " + kit.Label(se)},
		func(ctx context.Context) (struct{}, error) {
			return struct{}{}, registry.SubmitReview(ctx, src, reviewPR(se), r)
		},
		func(ctx registry.Ctx, _ struct{}) error {
			f.body, f.seen, f.auto = "", "", ""
			x.forget(reviewPR(se))
			saved := x.setDraft(se.ID, func(d *core.ReviewDraft) {
				for i, it := range sent {
					p := d.Picks[it.id]
					p.State, p.Sent = core.PickKeep, comments[i].Body
					d.Picks[it.id] = p
				}
				d.Summary = ""
				d.Sent = append(d.Sent, core.SentReview{Verdict: verdict, Commit: r.Commit, Comments: len(sent), At: kit.WindowNow(x.h).UTC()})
			})
			var errs []error
			for _, it := range sent {
				if it.finding != "" {
					errs = append(errs, x.h.Run(ctx, "review.ai.dismiss", registry.Args{"id": string(se.ID), "finding": it.finding}))
				}
			}
			// The sync links the posted comments to their threads by the sent bodies the draft keeps, so it waits for them.
			x.busy.Add(1)
			go func() {
				defer x.busy.Done()
				<-saved
				x.h.Do(func() {
					if a := (registry.Args{"id": string(se.ID)}); x.h.Available("github.sync", a) {
						x.run("github.sync", a)
					}
				})
			}()
			x.h.Notify(core.Notice{ID: "review.pr.submit", Severity: core.SevInfo,
				Text: fmt.Sprintf("%s on %s, with %s.", sentWord(verdict), src.Label(), kit.Counted(len(sent), "comment"))})
			return errors.Join(errs...)
		})
}

func prVerdictWord(v string) string {
	return map[string]string{registry.VerdictApprove: "Approving", registry.VerdictChanges: "Requesting changes on", registry.VerdictComment: "Commenting on"}[v]
}

func sentWord(v string) string {
	return map[string]string{registry.VerdictApprove: "Approved", registry.VerdictChanges: "Changes requested", registry.VerdictComment: "Review posted"}[v]
}

// autoSummary is the summary a review starts with, until the user writes their own: "LGTM" with no comment kept, else
// how many issues of each severity, worst first ("1 high, 2 medium issues.").
func autoSummary(items []reviewItem) string {
	counts := map[string]int{}
	n := 0
	for _, it := range items {
		if it.kept() {
			counts[cmp.Or(it.severity(), "other")]++
			n++
		}
	}
	if n == 0 {
		return "LGTM"
	}
	var parts []string
	for _, sev := range append(slices.Clone(core.Severities), "other") {
		if counts[sev] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[sev], sev))
		}
	}
	return strings.Join(parts, ", ") + map[bool]string{true: " issue.", false: " issues."}[n == 1]
}

var autoSummaryRe = regexp.MustCompile(`^(LGTM|(\d+ [a-z]+, )*\d+ [a-z]+ issues?\.)$`)

// isAutoSummary says s is one autoSummary wrote, which follows the comments kept until the user changes it.
func isAutoSummary(s string) bool { return autoSummaryRe.MatchString(strings.TrimSpace(s)) }
