//oriel:kind review

package review

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui/layout"
)

const (
	prReviewHelp     = "agent/reviews#session"
	prReviewKeysHelp = "agent/reviews#triage"
)

// PRReviewKeyMode is the keymap mode of a review session's pull request tab, for triaging its comments; the preview
// of the review about to be posted has its own.
const (
	PRReviewKeyMode        = "review.pr"
	PRReviewPreviewKeyMode = "review.pr.preview"
)

// The options a review session's review is written and kept up to date with.
const (
	optSeverityBadges = "review.severity_badges"
	optAutoRereview   = "review.auto_rereview"
)

// PRReview is a review session's pull request tab: the PR as its host has it, the comments the review has found
// to triage, reword and send, the user's verdict, posted to the host; and keeping up with what the author pushes.
type PRReview struct {
	kit.Disposer
	h    plugin.Host
	busy kit.Busy

	mu       sync.Mutex
	details  map[string]*reviewDetail // by URL
	fetching map[core.SessionID]bool
	saved    map[core.SessionID]int // the last draft saved, by its number

	forms  map[core.SessionID]*reviewForm       // on the loop
	drafts map[core.SessionID]*core.ReviewDraft // the latest set, on the loop
	seq    int                                  // on the loop
	asking map[core.SessionID]bool              // the review's request was open, as last seen; on the loop
	heard  map[core.SessionID]int               // the replies from the PR's author and others, as last counted; on the loop
}

// reviewForm is the tab's own state: the summary as typed, the comment selected and being edited, and the last fetch.
type reviewForm struct {
	body    string    // the summary, as typed
	typed   time.Time // when it last changed, for saving it once typing stops
	seen    string    // the summary as last drawn
	auto    string    // the summary autoSummary last wrote, which follows the comments until the user changes it
	moved   host.PRMove
	sel     string // the item selected
	editing string // the item whose words are being edited
	edit    string
	asking  string // the item a rework is being asked for in the user's words
	ask     string
	preview string // the verdict whose preview is open
	focused bool   // the preview has been given focus
	focus   bool   // the selected comment takes focus when next drawn: the tab's just come into view
}

// reviewItem is a comment the review can post: the agent's thread on a line, the AI review's finding (or both, where
// they found the same thing), or the user's own.
type reviewItem struct {
	id      string        // its pick's key: the thread's id, else the finding's
	thread  core.ThreadID // the agent's or the user's thread; "" for a finding alone
	finding string        // the AI review's finding, alone or found on the thread's line too
	mine    bool
	sev     string // the reviewer's rating
	title   string
	body    string // the reviewer's words
	at      core.Anchor
	pick    core.ReviewPick
}

// severity is the item's rating: the user's, else the reviewer's.
func (it reviewItem) severity() string { return cmp.Or(it.pick.Severity, it.sev) }

// words are what the author gets: the user's, else the reviewer's.
func (it reviewItem) words() string { return cmp.Or(it.pick.Body, it.body) }

// kept says it goes in the review: as the user decided, else when it's the user's own or worse than low.
func (it reviewItem) kept() bool {
	if it.pick.State != "" {
		return it.pick.State == core.PickKeep
	}
	return it.mine || core.SeverityRank(it.severity()) <= core.SeverityRank("medium")
}

func (it reviewItem) both() bool { return it.thread != "" && it.finding != "" }

func (x *PRReview) Manifest() plugin.Manifest { return plugin.Manifest{ID: "pr-review"} }

func (x *PRReview) Activate(_ context.Context, h plugin.Host) error {
	x.Add(h.Contribute(registry.Busy{Name: "prreview", Busy: x.busy.Busy, Source: "builtin:prreview"}))
	x.h, x.details, x.forms = h, map[string]*reviewDetail{}, map[core.SessionID]*reviewForm{}
	x.fetching, x.saved = map[core.SessionID]bool{}, map[core.SessionID]int{}
	x.drafts, x.asking, x.heard = map[core.SessionID]*core.ReviewDraft{}, map[core.SessionID]bool{}, map[core.SessionID]int{}
	x.contributeKind()
	for _, o := range x.options() {
		x.Add(h.Option(o))
	}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "react to a PR as its review starts"}, func(_ registry.Ctx, ev *kit.SessionStart) (registry.Verdict, error) {
		x.reactOnStart(core.SessionID(ev.Session))
		return registry.Verdict{}, nil
	}))
	x.Add(registry.On(h, registry.Hook{Kind: registry.Observe, Label: "fetch a reviewed PR its author pushed to"}, func(_ registry.Ctx, ev *kit.PrPushed) (registry.Verdict, error) {
		x.pushed(core.SessionID(ev.Session), ev.Head)
		return registry.Verdict{}, nil
	}))
	x.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if _, said := ev.(core.TranscriptAppended); said { // an agent's words move no thread, draft or state watch reads
			return
		}
		id := ev.Scope()
		if ref, ok := h.Session(id); id == "" || !ok || reviewPR(ref.Snapshot()) == "" {
			return
		}
		h.Do(func() { x.watch(id) })
	}))
	// A review's own entry in place of Raise PR's checklist: its tab, and how many comments are still to triage.
	front := func() (core.SessionState, bool) {
		se, err := x.session(nil)
		return se, err == nil && reviewPR(se) != ""
	}
	x.Add(h.Contribute(uireg.RailEntry{Name: "review", Icon: "pullrequest", Title: "Review", Command: "ship.pr.open", Scope: uireg.ScopeProjectEntry,
		Order: 40, Kinds: []core.Kind{core.KindReview}, Source: "builtin:pr-review",
		Shown: func(project string) bool {
			se, ok := front()
			p, _ := prProject(se)
			return ok && (project == "" || project == p.Name)
		},
		Active: func(string) bool {
			return slices.ContainsFunc(h.Layout().Visible(), func(v layout.ViewRef) bool { return v.ID == kit.PRView })
		},
		Badge: func() int {
			se, ok := front()
			if !ok {
				return 0
			}
			n := 0
			for _, it := range x.items(se) {
				if it.pick.State == "" {
					n++
				}
			}
			return n
		}}))
	x.Add(h.Contribute(uireg.ThreadFoot{Name: "review", Draw: x.foot, Source: "builtin:pr-review"}))
	x.Add(registry.DisposeFunc(h.Layout().OnShow(func(v layout.ViewRef) {
		if se, ok := front(); ok && v.ID == kit.PRView {
			x.form(se.ID).focus = true
		}
	})))
	return nil
}

// options say how a review's comments are posted and kept up with.
func (x *PRReview) options() []registry.OptionSpec {
	out := []registry.OptionSpec{
		{Name: optSeverityBadges, Type: registry.Bool, Default: true,
			Help: "Head each comment a review posts with a badge of its severity, so the author sees how much it matters."},
		{Name: optAutoRereview, Type: registry.Bool, Default: true,
			Help: "When the author pushes to a pull request under review, fetch it and ask the agent to review what changed."},
		{Name: optStartReaction, Type: registry.String, Default: "👀",
			Help: "React to a pull request with this emoji when a review session starts on it, where its host takes reactions, and take it back once you send your review. Empty for none."},
	}
	for i := range out {
		out[i].Section, out[i].Scopes = "review", registry.ScopeUser
	}
	return out
}

// keymaps triage the selected comment, in the review tab's key mode.
func (x *PRReview) keymaps() []registry.Keymap {
	out := []registry.Keymap{
		{Keys: "j", Command: "review.pr.next", Desc: "Next comment"},
		{Keys: "<Down>", Command: "review.pr.next", Desc: "Next comment"},
		{Keys: "k", Command: "review.pr.next", Args: registry.Args{"back": true}, Desc: "Comment before"},
		{Keys: "<Up>", Command: "review.pr.next", Args: registry.Args{"back": true}, Desc: "Comment before"},
		{Keys: "y", Command: "review.pr.pick", Args: registry.Args{"state": core.PickKeep}, Desc: "Keep it"},
		{Keys: "x", Command: "review.pr.pick", Args: registry.Args{"state": core.PickDrop}, Desc: "Drop it"},
		{Keys: "u", Command: "review.pr.pick", Args: registry.Args{"state": ""}, Desc: "Undecide it"},
		{Keys: "e", Command: "review.pr.edit", Desc: "Edit its words"},
		{Keys: "r", Command: "review.pr.rework", Args: registry.Args{"how": "shorter"}, Desc: "Ask the agent to shorten it"},
		{Keys: "<CR>", Command: "review.pr.show", Desc: "Show it in the diff"},
		{Keys: "o", Command: "review.pr.show", Desc: "Show it in the diff"},
		{Keys: "S", Command: "review.pr.summary", Desc: "Draft the summary"},
	}
	for i := range out {
		out[i].Mode = PRReviewKeyMode
	}
	out = append(out, registry.Keymap{Mode: PRReviewPreviewKeyMode, Keys: "<Esc>", Command: "review.pr.preview", Args: registry.Args{"verdict": ""}, Desc: "Back"})
	for i, s := range core.Severities {
		out = append(out, registry.Keymap{Mode: PRReviewKeyMode, Keys: fmt.Sprint(i + 1), Command: "review.pr.severity", Args: registry.Args{"severity": s},
			Desc: "Rate it " + s})
	}
	return out
}

func (x *PRReview) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.Disposer.Deactivate(ctx)
}

func (x *PRReview) wait() { x.busy.Wait() }

func (x *PRReview) session(a registry.Args) (core.SessionState, error) {
	return kit.SessionFrom(x.h, a)
}

func (x *PRReview) form(id core.SessionID) *reviewForm {
	f := x.forms[id]
	if f == nil {
		f = &reviewForm{}
		if se, ok := x.h.Session(id); ok {
			f.body = x.draft(se.Snapshot()).Summary
			f.seen = f.body
		}
		x.forms[id] = f
	}
	return f
}

// draft is the session's review draft: the latest this plugin set, else the session's.
func (x *PRReview) draft(se core.SessionState) core.ReviewDraft {
	if d := x.drafts[se.ID]; d != nil {
		return *d
	}
	if reviewDraft(se) != nil {
		return *reviewDraft(se)
	}
	return core.ReviewDraft{}
}

// setDraft changes the session's review draft, and keeps it in the session off the loop; a later change's save wins.
// The channel closes once the save is done.
func (x *PRReview) setDraft(id core.SessionID, fn func(d *core.ReviewDraft)) <-chan struct{} {
	done := make(chan struct{})
	se, ok := x.h.Session(id)
	if !ok {
		close(done)
		return done
	}
	d := x.draft(se.Snapshot()).Clone()
	fn(&d)
	x.drafts[id] = &d
	x.seq++
	n := x.seq
	w, err := kit.ShipCalls(x.h)
	if err != nil {
		close(done)
		return done
	}
	x.busy.Go(func() {
		defer close(done)
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.saved[id] > n {
			return
		}
		x.saved[id] = n
		if err := w.SetReviewDraft(core.ReviewDrafted{Session: id, Draft: d}); err != nil {
			x.h.Notify(core.Notice{ID: "review.pr.draft", Severity: core.SevError, Text: err.Error()})
		}
	})
	return done
}

func (x *PRReview) setPick(id core.SessionID, item string, fn func(p *core.ReviewPick)) {
	x.setDraft(id, func(d *core.ReviewDraft) {
		p := d.Picks[item]
		fn(&p)
		if p == (core.ReviewPick{}) {
			delete(d.Picks, item)
		} else {
			d.Picks[item] = p
		}
	})
}

// site is the code host the session's pull request is on.
func (x *PRReview) site(se core.SessionState) (registry.HostSite, bool) {
	return kit.SitesOf(x.h).URL(reviewPR(se))
}

// detail is the pull request as its host last said, asked for the first time it's wanted, and again once forgotten.
func (x *PRReview) detail(s registry.HostSite, url string) *reviewDetail {
	x.mu.Lock()
	defer x.mu.Unlock()
	if d := x.details[url]; d != nil {
		return d
	}
	d := &reviewDetail{since: kit.WindowNow(x.h)}
	x.details[url] = d
	if s.Lookup == nil {
		d.done = true
		return d
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, s, url)
		x.mu.Lock()
		d.d, d.done, d.read = got, true, time.Now()
		if err != nil {
			d.err = err.Error()
		}
		again := d.again
		d.again = false
		x.mu.Unlock()
		x.h.Redraw()
		if again {
			x.refresh(url)
		}
	})
	return d
}

// refresh asks the host for the pull request at url again, keeping what it said last until it answers. Asked while
// a read is under way, it reads once more after it: that read may have begun before what it's asked again for.
func (x *PRReview) refresh(url string) {
	x.mu.Lock()
	d := x.details[url]
	busy := d == nil || !d.done || d.refreshing
	if d != nil && busy {
		d.again = true
	}
	if !busy {
		d.refreshing = true
	}
	x.mu.Unlock()
	src, ok := kit.SitesOf(x.h).URL(url)
	if busy || !ok || src.Lookup == nil {
		return
	}
	x.busy.Go(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, src, url)
		x.mu.Lock()
		d.refreshing = false
		if err == nil {
			d.d, d.err, d.read = got, "", time.Now()
		}
		again := d.again
		d.again = false
		x.mu.Unlock()
		x.h.Redraw()
		if again {
			x.refresh(url)
		}
	})
}

// ended says the pull request at url is closed or merged, as its host last said.
func (x *PRReview) ended(url string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	d := x.details[url]
	return d != nil && d.done && (d.d.State == "closed" || d.d.State == "merged")
}

func (x *PRReview) forget(url string) {
	x.mu.Lock()
	delete(x.details, url)
	x.mu.Unlock()
}

// prProject is the session's project the pull request is checked out in.
func prProject(se core.SessionState) (core.Project, bool) {
	for _, p := range se.Projects {
		if p.Detached && strings.HasPrefix(p.Base, "refs/oriel/pr/") {
			return p, true
		}
	}
	return se.Primary()
}

// reviewDir is the directory of the session's project named, or of the PR's when it's "".
func reviewDir(se core.SessionState, name string) string {
	for _, p := range se.Projects {
		if p.Name == name {
			return p.Dir
		}
	}
	p, _ := prProject(se)
	return p.Dir
}

func (x *PRReview) items(se core.SessionState) []reviewItem {
	return reviewItems(se, x.draft(se))
}

// reviewItems are the comments the review can still post, worst first: the agent's open threads on lines of the PR's
// project, the user's own, and the AI review's open findings that aren't threads, each folded into the agent's thread
// on its line when there is one.
func reviewItems(se core.SessionState, d core.ReviewDraft) []reviewItem {
	p, _ := prProject(se)
	ours := func(project string) bool { return project == "" || project == p.Name }
	pending := func(id string) bool { return d.Picks[id].Sent == "" }
	var out []reviewItem
	for _, t := range se.Threads {
		dropped := t.Status == core.ThreadResolved && d.Picks[string(t.ID)].State == core.PickDrop
		if t.Anchor == nil || !t.Open() && !dropped || t.Hosted != nil || len(t.Comments) == 0 || !ours(t.Anchor.Project) || !pending(string(t.ID)) {
			continue
		}
		c := t.Comments[0]
		if c.Author != core.AuthorAgent && !t.Outgoing {
			continue
		}
		out = append(out, reviewItem{id: string(t.ID), thread: t.ID, mine: t.Outgoing, sev: c.Severity, title: cmp.Or(t.Title, kit.FirstLine(c.Body)),
			body: c.Body, at: *t.Anchor, pick: d.Picks[string(t.ID)]})
	}
	for _, f := range se.Findings {
		if f.Thread != "" || !se.FindingOpen(f) || f.File == "" || f.Line == 0 || f.Repo != "" || !ours(f.Project) || !pending(f.ID) {
			continue
		}
		body := "**" + f.Title + "**"
		if b := strings.TrimSpace(f.Body); b != "" {
			body += "\n\n" + b
		}
		if i := slices.IndexFunc(out, func(it reviewItem) bool { return !it.mine && it.finding == "" && sameLines(it.at, f) }); i >= 0 {
			it := &out[i]
			it.finding = f.ID
			if core.SeverityRank(f.Severity) < core.SeverityRank(it.sev) {
				it.sev = f.Severity
			}
			continue
		}
		out = append(out, reviewItem{id: f.ID, finding: f.ID, sev: f.Severity, title: f.Title, body: body, at: core.Anchor{File: f.File, Line: f.Line},
			pick: d.Picks[f.ID]})
	}
	slices.SortStableFunc(out, func(a, b reviewItem) int {
		return cmp.Or(cmp.Compare(core.SeverityRank(a.severity()), core.SeverityRank(b.severity())), strings.Compare(a.at.File, b.at.File),
			cmp.Compare(a.at.Line, b.at.Line))
	})
	return out
}

// sameLines says a finding is about the lines a thread is on: its file, within two lines of them.
func sameLines(a core.Anchor, f core.Finding) bool {
	return a.File == f.File && f.Line >= a.Line-2 && f.Line <= max(a.EndLine, a.Line)+2
}

// firstWords is a posted comment's first line of words, past its badges.
func firstWords(s string) string {
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "![") {
			return l
		}
	}
	return ""
}

// sentItem is a comment the review has posted, and the thread it's become.
type sentItem struct {
	thread core.Thread
	posted string
	linked bool // mirrored back from the host
}

// sentItems are the comments the review has posted, by place.
func sentItems(se core.SessionState) []sentItem {
	if reviewDraft(se) == nil {
		return nil
	}
	var out []sentItem
	for id, pk := range reviewDraft(se).Picks {
		if pk.Sent == "" {
			continue
		}
		if t, ok := se.Thread(core.ThreadID(id)); ok && t.Anchor != nil {
			out = append(out, sentItem{thread: t, posted: pk.Sent, linked: t.Hosted != nil})
			continue
		}
		if f, ok := se.Finding(id); ok {
			out = append(out, sentItem{thread: core.Thread{ID: core.ThreadID(id), Anchor: &core.Anchor{File: f.File, Line: f.Line}, Title: f.Title}, posted: pk.Sent})
		}
	}
	slices.SortFunc(out, func(a, b sentItem) int {
		return cmp.Or(strings.Compare(a.thread.Anchor.File, b.thread.Anchor.File), cmp.Compare(a.thread.Anchor.Line, b.thread.Anchor.Line),
			strings.Compare(string(a.thread.ID), string(b.thread.ID)))
	})
	return out
}

// sentState is where a posted comment stands: resolved, its code changed, the author's answered, or waiting on them.
func sentState(s sentItem) string {
	t := s.thread
	switch {
	case !s.linked:
		return "posted"
	case t.Status == core.ThreadResolved || t.Hosted.Resolved:
		return "resolved"
	case t.Hosted.Outdated:
		return "code changed"
	}
	for _, c := range slices.Backward(t.Comments) {
		if c.Author == core.AuthorHost {
			if c.Name != t.Hosted.Reviewer {
				return "author replied"
			}
			break
		}
	}
	return "waiting on author"
}

// settle resolves a dropped comment's thread, so Requests holds only what's left, and opens it again when it's taken
// back.
func (x *PRReview) settle(ctx registry.Ctx, se core.SessionState, it reviewItem, state string) error {
	t, ok := se.Thread(it.thread)
	switch {
	case !ok:
		return nil
	case state == core.PickDrop && t.Open():
		return x.h.Run(ctx, "request.resolve", registry.Args{"id": string(se.ID), "thread": string(t.ID)})
	case state != core.PickDrop && t.Status == core.ThreadResolved:
		return x.h.Run(ctx, "request.resolve", registry.Args{"id": string(se.ID), "thread": string(t.ID), "status": string(core.ThreadOpen)})
	}
	return nil
}

// advance selects the next comment not decided on, after item.
func (x *PRReview) advance(se core.SessionState, item string) {
	items := x.items(se)
	i := slices.IndexFunc(items, func(it reviewItem) bool { return it.id == item })
	for j := i + 1; j < len(items); j++ {
		if items[j].pick.State == "" {
			x.form(se.ID).sel = items[j].id
			return
		}
	}
}

func (x *PRReview) step(se core.SessionState, back bool) {
	items := x.items(se)
	if len(items) == 0 {
		return
	}
	f := x.form(se.ID)
	i := slices.IndexFunc(items, func(it reviewItem) bool { return it.id == f.sel })
	switch {
	case i < 0:
		i = 0
	case back:
		i = max(i-1, 0)
	default:
		i = min(i+1, len(items)-1)
	}
	f.sel = items[i].id
}

// watch keeps up with a review session's threads: the summary the agent drafted goes in the box, and the user's told
// when the agent's review is done.
func (x *PRReview) watch(id core.SessionID) {
	ref, ok := x.h.Session(id)
	if !ok {
		return
	}
	se := ref.Snapshot()
	if reviewPR(se) == "" {
		return
	}
	if d := x.draft(se); d.SummaryAsk != "" {
		if t, ok := se.Thread(d.SummaryAsk); ok {
			if c, ok := t.Last(); ok && len(t.Comments) > 1 && c.Author == core.AuthorAgent && !c.Progress {
				text := strings.TrimSpace(c.Body)
				x.setDraft(id, func(d *core.ReviewDraft) { d.SummaryAsk, d.Summary = "", text })
				f := x.form(id)
				f.body, f.seen = text, text
			}
		}
	}
	x.hear(se)
	t, ok := reviewRequest(se)
	if !ok {
		return
	}
	asking := reviewing(se, t)
	was, seen := x.asking[id]
	x.asking[id] = asking
	if !seen || !was || asking {
		return
	}
	text := fmt.Sprintf("The agent has reviewed %s: %s to triage.", kit.Label(se), kit.Counted(len(x.items(se)), "comment"))
	x.h.Notify(core.Notice{ID: "review.pr.done." + string(id), Severity: core.SevInfo, Session: id, Title: "Review done", Text: text,
		Actions: []core.Action{openReview(id)}})
}

// reviewing says the agent is still on its review: its request open, and it hasn't answered it and stopped.
func reviewing(se core.SessionState, t core.Thread) bool {
	return t.Open() && !(t.AgentAnswered() && !se.Agent.Working)
}

// hear tells the user when someone other than them answers on the review's threads on its code host: the author, as a
// rule.
func (x *PRReview) hear(se core.SessionState) {
	n := 0
	var last core.Comment
	var at core.Thread
	for _, t := range se.Threads {
		if t.Hosted == nil || t.Hosted.Root == 0 && t.Hosted.Key == "" {
			continue
		}
		for _, c := range t.Comments {
			if c.Author == core.AuthorHost && c.Name != t.Hosted.Reviewer {
				n++
				if c.Time.After(last.Time) {
					last, at = c, t
				}
			}
		}
	}
	was, seen := x.heard[se.ID]
	x.heard[se.ID] = n
	if !seen || n <= was {
		return
	}
	x.h.Notify(core.Notice{ID: "review.pr.reply." + string(se.ID), Severity: core.SevInfo, Session: se.ID, Title: last.Name + " replied",
		Text: fmt.Sprintf("On %s, %s:%d: %s", kit.Label(se), at.Anchor.File, at.Anchor.Line, kit.FirstLine(last.Body)),
		Actions: []core.Action{{Title: "Open the thread", Command: "session.open", Args: map[string]any{"id": string(se.ID),
			"then": kit.Then(kit.Call{Command: "request.open", Args: registry.Args{"thread": string(at.ID)}})}}}})
}

func openReview(id core.SessionID) core.Action {
	return core.Action{Title: "Open the review", Command: "session.open",
		Args: map[string]any{"id": string(id), "then": kit.Then(kit.Call{Command: "ship.pr.open"})}}
}

// saveSummary keeps the summary as typed in the session, once typing has stopped for a moment.
func (x *PRReview) saveSummary(se core.SessionState, f *reviewForm, now time.Time) (wake time.Time) {
	if f.body != f.seen {
		f.seen, f.typed = f.body, now
	}
	if f.body == x.draft(se).Summary {
		return time.Time{}
	}
	if due := f.typed.Add(800 * time.Millisecond); now.Before(due) {
		return due
	}
	body := f.body
	x.setDraft(se.ID, func(d *core.ReviewDraft) { d.Summary = body })
	return time.Time{}
}

// reviewRequest is the request the review session's agent was given: its first thread on no line.
func reviewRequest(se core.SessionState) (core.Thread, bool) {
	for _, t := range se.Threads {
		if t.Anchor == nil && t.Hosted == nil {
			return t, true
		}
	}
	return core.Thread{}, false
}

func (x *PRReview) run(id string, a registry.Args) {
	kit.RunOrNotify(x.h, "review.pr", id, a)
}
