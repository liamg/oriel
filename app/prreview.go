package app

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
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

// reviewPoll is how often a review session not in front is synced with its pull request.
const reviewPoll = 2 * time.Minute

// prReviewPlugin is a review session's pull request tab: the PR as its host has it, the comments the review has found
// to triage, reword and send, the user's verdict, posted to the host; and keeping up with what the author pushes.
type prReviewPlugin struct {
	disposer
	sh   *shell
	h    plugin.Host
	busy sync.WaitGroup

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
func (it reviewItem) severity() string { return cmpOr(it.pick.Severity, it.sev) }

// words are what the author gets: the user's, else the reviewer's.
func (it reviewItem) words() string { return cmpOr(it.pick.Body, it.body) }

// kept says it goes in the review: as the user decided, else when it's the user's own or worse than low.
func (it reviewItem) kept() bool {
	if it.pick.State != "" {
		return it.pick.State == core.PickKeep
	}
	return it.mine || core.SeverityRank(it.severity()) <= core.SeverityRank("medium")
}

func (it reviewItem) both() bool { return it.thread != "" && it.finding != "" }

func (x *prReviewPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "pr-review"} }

func (x *prReviewPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.details, x.forms = h, map[string]*reviewDetail{}, map[core.SessionID]*reviewForm{}
	x.fetching, x.saved = map[core.SessionID]bool{}, map[core.SessionID]int{}
	x.drafts, x.asking, x.heard = map[core.SessionID]*core.ReviewDraft{}, map[core.SessionID]bool{}, map[core.SessionID]int{}
	x.sh.prReview = x
	for _, o := range []registry.OptionSpec{
		{Name: optSeverityBadges, Type: registry.Bool, Default: true,
			Help: "Head each comment a review posts with a badge of its severity, so the author sees how much it matters."},
		{Name: optAutoRereview, Type: registry.Bool, Default: true,
			Help: "When the author pushes to a pull request under review, fetch it and ask the agent to review what changed."},
	} {
		o.Section, o.Scopes = "review", registry.ScopeUser
		x.add(h.Option(o))
	}
	x.add(onDispose(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if id := ev.Scope(); id != "" {
			h.Do(func() { x.watch(id) })
		}
	})))
	reviewing := func(a registry.Args) bool {
		se, err := x.session(a)
		return err == nil && se.PR != ""
	}
	item := func(fn func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error) func(registry.Ctx, registry.Args) error {
		return func(ctx registry.Ctx, a registry.Args) error {
			se, err := x.session(a)
			if err != nil {
				return err
			}
			id := cmpOr(a.String("item"), x.form(se.ID).sel)
			for _, it := range x.items(se) {
				if it.id == id {
					return fn(ctx, se, it, a)
				}
			}
			return fmt.Errorf("no comment %q in the review", id)
		}
	}
	for _, c := range []registry.Command{
		{ID: "review.pr.fetch", Title: "Fetch what the author has pushed to session {id}'s pull request (the one in front by default)", Available: reviewing,
			Run: x.fetch},
		{ID: "review.pr.recheck", Title: "Ask session {id}'s agent to review the commits the last fetch brought, and where its earlier comments stand",
			Available: reviewing, Run: x.recheck},
		{ID: "review.pr.pick", Title: "Keep the review's {item} (the selected one by default) for the review you submit, or drop it: {state} keep, drop, or '' undecided",
			Available: reviewing, Run: item(func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				s := a.String("state")
				if !slices.Contains([]string{core.PickKeep, core.PickDrop, ""}, s) {
					return fmt.Errorf("a comment is kept or dropped, not %q", s)
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.State = s })
				x.advance(se, it.id)
				return x.settle(ctx, se, it, s)
			})},
		{ID: "review.pr.include", Title: "Put the review's {item} in the review you submit, or leave it out with {on} false", Available: reviewing,
			Run: item(func(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
				s := core.PickKeep
				if v, ok := a["on"].(bool); ok && !v {
					s = core.PickDrop
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.State = s })
				return x.settle(ctx, se, it, s)
			})},
		{ID: "review.pr.severity", Title: "Rate the review's {item}: {severity} critical, high, medium, low or nit; '' for the reviewer's", Available: reviewing,
			Run: item(func(_ registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
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
			Available: reviewing, Run: item(func(_ registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
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
		{ID: "review.pr.rework", Title: "Ask the agent to reword the review's {item} for the author: {how} shorter, friendlier, suggestion or detail, or {ask} in your words",
			Available: reviewing, Run: item(x.rework)},
		{ID: "review.pr.rework.use", Title: "Use the agent's rewording of the review's {item}", Available: reviewing,
			Run: item(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				text, ok := reworded(se, it.pick)
				if !ok {
					return errors.New("the agent hasn't reworded it yet")
				}
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Body, p.Rework, p.ReworkAt = text, "", 0 })
				return x.endRework(ctx, se, it)
			})},
		{ID: "review.pr.rework.drop", Title: "Keep your words for the review's {item}, not the agent's rewording", Available: reviewing,
			Run: item(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				x.setPick(se.ID, it.id, func(p *core.ReviewPick) { p.Rework, p.ReworkAt = "", 0 })
				return x.endRework(ctx, se, it)
			})},
		{ID: "review.pr.summary", Title: "Ask session {id}'s agent to draft the review's summary from the comments kept; it fills the summary box",
			Available: reviewing, Run: x.askSummary},
		{ID: "review.pr.next", Title: "Select the review's next comment, or the one before with {back}", Available: reviewing,
			Run: func(_ registry.Ctx, a registry.Args) error {
				se, err := x.session(a)
				if err != nil {
					return err
				}
				x.step(se, a.Bool("back"))
				return nil
			}},
		{ID: "review.pr.show", Title: "Show the review's {item} (the selected one by default) at its line in the diff", Available: reviewing,
			Run: item(func(ctx registry.Ctx, se core.SessionState, it reviewItem, _ registry.Args) error {
				x.form(se.ID).sel = it.id
				return x.h.Run(ctx, "diff.open", registry.Args{"path": filepath.Join(reviewDir(se, it.at.Project), filepath.FromSlash(it.at.File)), "line": it.at.Line})
			})},
		{ID: "review.pr.preview", Title: "Preview the review as it would be posted with {verdict}: approve, changes or comment; '' closes it", Available: reviewing,
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
			Available: reviewing, Run: x.submit},
	} {
		x.add(h.Command(c))
	}
	// A review's own entry in place of Raise PR's checklist: its tab, and how many comments are still to triage.
	front := func() (core.SessionState, bool) {
		se, err := x.session(nil)
		return se, err == nil && se.PR != ""
	}
	x.add(h.Contribute(registry.RailEntry{Name: "review", Icon: "pullrequest", Title: "Review", Command: "ship.pr.open", Scope: registry.ScopeProjectEntry,
		Order: 40, Source: "builtin:pr-review",
		Shown: func(project string) bool {
			se, ok := front()
			p, _ := prProject(se)
			return ok && (project == "" || project == p.Name)
		},
		Active: func(string) bool {
			return slices.ContainsFunc(h.Layout().Visible(), func(v layout.ViewRef) bool { return v.ID == PRView })
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
	x.add(h.Contribute(registry.ThreadFoot{Name: "review", Draw: x.foot, Source: "builtin:pr-review"}))
	x.add(disposeFn(h.Layout().OnShow(func(v layout.ViewRef) {
		if se, ok := front(); ok && v.ID == PRView {
			x.form(se.ID).focus = true
		}
	})))
	for _, k := range []registry.Keymap{
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
	} {
		k.Mode = PRReviewKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: PRReviewPreviewKeyMode, Keys: "<Esc>", Command: "review.pr.preview", Args: registry.Args{"verdict": ""}, Desc: "Back"}))
	for i, s := range core.Severities {
		x.add(h.Keymap(registry.Keymap{Mode: PRReviewKeyMode, Keys: fmt.Sprint(i + 1), Command: "review.pr.severity", Args: registry.Args{"severity": s},
			Desc: "Rate it " + s}))
	}
	return nil
}

func (x *prReviewPlugin) Deactivate(ctx context.Context) error {
	x.busy.Wait()
	return x.disposer.Deactivate(ctx)
}

func (x *prReviewPlugin) wait() { x.busy.Wait() }

func (x *prReviewPlugin) session(a registry.Args) (core.SessionState, error) {
	id := core.SessionID(a.String("id"))
	if id == "" {
		id = x.h.State().Focus
	}
	se, ok := x.h.Session(id)
	if !ok {
		return core.SessionState{}, fmt.Errorf("no session %q", id)
	}
	return se.Snapshot(), nil
}

func (x *prReviewPlugin) form(id core.SessionID) *reviewForm {
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
func (x *prReviewPlugin) draft(se core.SessionState) core.ReviewDraft {
	if d := x.drafts[se.ID]; d != nil {
		return *d
	}
	if se.Review != nil {
		return *se.Review
	}
	return core.ReviewDraft{}
}

// setDraft changes the session's review draft, and keeps it in the session off the loop; a later change's save wins.
func (x *prReviewPlugin) setDraft(id core.SessionID, fn func(d *core.ReviewDraft)) {
	se, ok := x.h.Session(id)
	if !ok {
		return
	}
	d := x.draft(se.Snapshot()).Clone()
	fn(&d)
	x.drafts[id] = &d
	x.seq++
	n := x.seq
	w, err := x.sh.sessions.host()
	if err != nil {
		return
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		x.mu.Lock()
		defer x.mu.Unlock()
		if x.saved[id] > n {
			return
		}
		x.saved[id] = n
		if err := w.SetReviewDraft(core.ReviewDrafted{Session: id, Draft: d}); err != nil {
			x.h.Notify(core.Notice{ID: "review.pr.draft", Severity: core.SevError, Text: err.Error()})
		}
	}()
}

func (x *prReviewPlugin) setPick(id core.SessionID, item string, fn func(p *core.ReviewPick)) {
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
func (x *prReviewPlugin) site(se core.SessionState) (registry.HostSite, bool) {
	return x.sh.sites.url(se.PR)
}

// detail is the pull request as its host last said, asked for the first time it's wanted, and again once forgotten.
func (x *prReviewPlugin) detail(s registry.HostSite, url string) *reviewDetail {
	x.mu.Lock()
	defer x.mu.Unlock()
	if d := x.details[url]; d != nil {
		return d
	}
	d := &reviewDetail{since: windowNow(x.h)}
	x.details[url] = d
	if s.Lookup == nil {
		d.done = true
		return d
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, s, url)
		x.mu.Lock()
		d.d, d.done = got, true
		if err != nil {
			d.err = err.Error()
		}
		x.mu.Unlock()
		x.h.Redraw()
	}()
	return d
}

// refresh asks the host for the pull request at url again, keeping what it said last until it answers.
func (x *prReviewPlugin) refresh(url string) {
	x.mu.Lock()
	d := x.details[url]
	busy := d == nil || !d.done || d.refreshing
	if !busy {
		d.refreshing = true
	}
	x.mu.Unlock()
	src, ok := x.sh.sites.url(url)
	if busy || !ok || src.Lookup == nil {
		return
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		got, err := registry.LookupReview(ctx, src, url)
		x.mu.Lock()
		d.refreshing = false
		if err == nil {
			d.d, d.err = got, ""
		}
		x.mu.Unlock()
		x.h.Redraw()
	}()
}

// ended says the pull request at url is closed or merged, as its host last said.
func (x *prReviewPlugin) ended(url string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	d := x.details[url]
	return d != nil && d.done && (d.d.State == "closed" || d.d.State == "merged")
}

func (x *prReviewPlugin) forget(url string) {
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

func (x *prReviewPlugin) items(se core.SessionState) []reviewItem {
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
		out = append(out, reviewItem{id: string(t.ID), thread: t.ID, mine: t.Outgoing, sev: c.Severity, title: cmpOr(t.Title, firstLineOf(c.Body)),
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

func firstLineOf(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return line
}

// sentItem is a comment the review has posted, and the thread it's become.
type sentItem struct {
	thread core.Thread
	posted string
	linked bool // mirrored back from the host
}

// sentItems are the comments the review has posted, by place.
func sentItems(se core.SessionState) []sentItem {
	if se.Review == nil {
		return nil
	}
	var out []sentItem
	for id, pk := range se.Review.Picks {
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
func (x *prReviewPlugin) settle(ctx registry.Ctx, se core.SessionState, it reviewItem, state string) error {
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
func (x *prReviewPlugin) advance(se core.SessionState, item string) {
	items := x.items(se)
	i := slices.IndexFunc(items, func(it reviewItem) bool { return it.id == item })
	for j := i + 1; j < len(items); j++ {
		if items[j].pick.State == "" {
			x.form(se.ID).sel = items[j].id
			return
		}
	}
}

func (x *prReviewPlugin) step(se core.SessionState, back bool) {
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

// reworkAsks are the rewordings a comment can be asked for in a word.
var reworkAsks = map[string]string{
	"shorter":    "Make it shorter: the point in a sentence or two.",
	"friendlier": "Make it friendlier: suggest rather than instruct, as a colleague would.",
	"suggestion": "Add a GitHub suggestion block with the fix, if it's small and you're sure of it.",
	"detail":     "Add the detail the author needs: why it matters and where it bites.",
}

// rework asks the agent to reword an item for the author: in its own thread, else in one opened on the item's line.
func (x *prReviewPlugin) rework(ctx registry.Ctx, se core.SessionState, it reviewItem, a registry.Args) error {
	how := strings.TrimSpace(a.String("ask"))
	if how == "" {
		var ok bool
		if how, ok = reworkAsks[a.String("how")]; !ok {
			return fmt.Errorf("rework is shorter, friendlier, suggestion or detail, or an ask in your words; not %q", a.String("how"))
		}
	}
	body := "This is a rework request: reword this review comment before it goes to the PR's author. " + how +
		" Reply with the new comment only, no preamble, and leave this thread open.\n\n" + quote(it.words())
	w, err := x.sh.sessions.host()
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
func (x *prReviewPlugin) endRework(ctx registry.Ctx, se core.SessionState, it reviewItem) error {
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
func (x *prReviewPlugin) askSummary(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	var lines []string
	for _, it := range x.items(se) {
		if it.kept() {
			lines = append(lines, fmt.Sprintf("- %s `%s:%d`: %s", it.severity(), it.at.File, it.at.Line, firstLineOf(it.words())))
		}
	}
	body := "This is a summary request: the review about to go to the author of " + label(se) + " has these comments:\n\n" +
		cmpOr(strings.Join(lines, "\n"), "(none: it approves)") + "\n\nReply with the summary only, and resolve this thread."
	w, err := x.sh.sessions.host()
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

// watch keeps up with a review session's threads: the summary the agent drafted goes in the box, and the user's told
// when the agent's review is done.
func (x *prReviewPlugin) watch(id core.SessionID) {
	ref, ok := x.h.Session(id)
	if !ok {
		return
	}
	se := ref.Snapshot()
	if se.PR == "" {
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
	text := fmt.Sprintf("The agent has reviewed %s: %s to triage.", label(se), counted(len(x.items(se)), "comment"))
	x.h.Notify(core.Notice{ID: "review.pr.done." + string(id), Severity: core.SevInfo, Session: id, Title: "Review done", Text: text,
		Actions: []core.Action{openReview(id)}})
}

// reviewing says the agent is still on its review: its request open, and it hasn't answered it and stopped.
func reviewing(se core.SessionState, t core.Thread) bool {
	return t.Open() && !(t.AgentAnswered() && !se.Agent.Working)
}

// hear tells the user when someone other than them answers on the review's threads on its code host: the author, as a
// rule.
func (x *prReviewPlugin) hear(se core.SessionState) {
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
		Text: fmt.Sprintf("On %s, %s:%d: %s", label(se), at.Anchor.File, at.Anchor.Line, firstLineOf(last.Body)),
		Actions: []core.Action{{Title: "Open the thread", Command: "session.open", Args: map[string]any{"id": string(se.ID),
			"then": []any{map[string]any{"command": "request.open", "args": map[string]any{"thread": string(at.ID)}}}}}}})
}

func openReview(id core.SessionID) core.Action {
	return core.Action{Title: "Open the review", Command: "session.open",
		Args: map[string]any{"id": string(id), "then": []any{map[string]any{"command": "ship.pr.open"}}}}
}

// saveSummary keeps the summary as typed in the session, once typing has stopped for a moment.
func (x *prReviewPlugin) saveSummary(se core.SessionState, f *reviewForm, now time.Time) (wake time.Time) {
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

func (x *prReviewPlugin) fetch(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	w, err := x.sh.sessions.host()
	if err != nil {
		return err
	}
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.fetch." + string(se.ID), Doing: "Fetching " + label(se)},
		func(ctx context.Context) (map[string]host.PRMove, error) { return w.UpdatePR(ctx, se.ID) },
		func(_ registry.Ctx, moved map[string]host.PRMove) error {
			x.forget(se.PR)
			for _, m := range moved {
				x.form(se.ID).moved = m // the tab's banner says so
			}
			if len(moved) == 0 {
				x.h.Notify(core.Notice{ID: "review.pr.fetch", Severity: core.SevInfo, Text: label(se) + " has nothing new."})
			}
			return nil
		})
}

// pushed is a sync's word that a review's pull request is at head on its host: when that's new it's fetched, and with
// review.auto_rereview on, the agent is asked to review what changed once its last review is done.
func (x *prReviewPlugin) pushed(id core.SessionID, head string) {
	ref, ok := x.h.Session(id)
	if !ok {
		return
	}
	se := ref.Snapshot()
	if se.PR != "" {
		x.refresh(se.PR)
	}
	p, ok := prProject(se)
	if se.PR == "" || !ok || head == "" || p.Commit == "" || head == p.Commit || se.Archived || x.ended(se.PR) {
		return
	}
	if on, _, _ := x.h.Options().Get(optAutoRereview, registry.At{}); on != true {
		x.forget(se.PR) // the tab's bar offers Fetch latest
		return
	}
	x.mu.Lock()
	if x.fetching[id] {
		x.mu.Unlock()
		return
	}
	x.fetching[id] = true
	x.mu.Unlock()
	w, err := x.sh.sessions.host()
	if err != nil {
		return
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		moved, err := w.UpdatePR(ctx, id)
		cancel()
		x.mu.Lock()
		delete(x.fetching, id)
		x.mu.Unlock()
		x.h.Do(func() {
			if err != nil {
				x.h.Notify(core.Notice{ID: "review.pr.fetch", Severity: core.SevWarning, Session: id, Text: "Fetching " + label(se) + ": " + err.Error()})
				return
			}
			x.forget(se.PR)
			for _, m := range moved {
				x.form(id).moved = m
				text := fmt.Sprintf("%s has %s.", label(se), counted(m.Commits, "new commit"))
				if t, ok := reviewRequest(se); ok && !reviewing(se, t) {
					x.run("review.pr.recheck", registry.Args{"id": string(id)})
					text += " The agent is reviewing them."
				}
				x.h.Notify(core.Notice{ID: "review.pr.pushed." + string(id), Severity: core.SevInfo, Session: id, Title: "New commits", Text: text,
					Actions: []core.Action{openReview(id)}})
			}
		})
	}()
}

// recheck asks the agent to review the commits a fetch brought: the review pack's later round, with where each of
// its earlier comments stands and what the user made of the rest.
func (x *prReviewPlugin) recheck(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	m := x.form(se.ID).moved
	if m.To == "" {
		return errors.New("no fetch has brought new commits")
	}
	x.form(se.ID).moved = host.PRMove{}
	var b strings.Builder
	fmt.Fprintf(&b, "The PR was reviewed before: the author has pushed %s since you reviewed %s. Review what changed (`git diff %s %s`) as a later round.",
		counted(m.Commits, "commit"), label(se), shortSHA(m.From), shortSHA(m.To))
	if sent := sentItems(se); len(sent) > 0 {
		b.WriteString("\n\nYour earlier comments, as posted:\n")
		for _, s := range sent {
			fmt.Fprintf(&b, "- `%s:%d` (%s, thread %s): %s\n", s.thread.Anchor.File, s.thread.Anchor.Line, sentState(s), s.thread.ID, firstWords(s.posted))
		}
	}
	var dropped, rated []string
	for _, it := range x.items(se) {
		if it.pick.State == core.PickDrop {
			dropped = append(dropped, fmt.Sprintf("- `%s:%d`: %s", it.at.File, it.at.Line, it.title))
		}
		if it.pick.Severity != "" {
			rated = append(rated, fmt.Sprintf("- `%s:%d`: %s → %s", it.at.File, it.at.Line, it.sev, it.pick.Severity))
		}
	}
	if len(dropped) > 0 {
		b.WriteString("\nThe user dropped:\n" + strings.Join(dropped, "\n") + "\n")
	}
	if len(rated) > 0 {
		b.WriteString("\nThe user re-rated:\n" + strings.Join(rated, "\n") + "\n")
	}
	return x.h.Run(ctx, "request.comment", registry.Args{"id": string(se.ID), "title": "Review the new commits", "body": b.String()})
}

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
func (x *prReviewPlugin) posted(it reviewItem) string {
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
func (x *prReviewPlugin) toPost(se core.SessionState) ([]reviewItem, []registry.ReviewComment) {
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

func (x *prReviewPlugin) submit(ctx registry.Ctx, a registry.Args) error {
	se, err := x.session(a)
	if err != nil {
		return err
	}
	src, ok := x.site(se)
	if !ok {
		return fmt.Errorf("no code host has %s", se.PR)
	}
	verdict := a.String("verdict")
	if prVerdictWord(verdict) == "" {
		return fmt.Errorf("a review is approve, changes or comment, not %q", verdict)
	}
	f := x.form(se.ID)
	r := registry.ReviewSubmit{Event: verdict, Body: strings.TrimSpace(cmpOr(a.String("body"), f.body))}
	if p, ok := prProject(se); ok {
		r.Commit = p.Commit
	}
	sent, comments := x.toPost(se)
	r.Comments = comments
	if verdict != registry.VerdictApprove && r.Body == "" && len(r.Comments) == 0 {
		return errors.New("say something: a summary, or a comment kept")
	}
	f.preview = ""
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "review.pr.submit." + string(se.ID), Doing: prVerdictWord(verdict) + " " + label(se)},
		func(ctx context.Context) (struct{}, error) {
			return struct{}{}, registry.SubmitReview(ctx, src, se.PR, r)
		},
		func(ctx registry.Ctx, _ struct{}) error {
			f.body, f.seen = "", ""
			x.forget(se.PR)
			x.setDraft(se.ID, func(d *core.ReviewDraft) {
				for i, it := range sent {
					p := d.Picks[it.id]
					p.State, p.Sent = core.PickKeep, comments[i].Body
					d.Picks[it.id] = p
				}
				d.Summary = ""
				d.Sent = append(d.Sent, core.SentReview{Verdict: verdict, Commit: r.Commit, Comments: len(sent), At: windowNow(x.h).UTC()})
			})
			var errs []error
			for _, it := range sent {
				if it.finding != "" {
					errs = append(errs, x.h.Run(ctx, "review.ai.dismiss", registry.Args{"id": string(se.ID), "finding": it.finding}))
				}
			}
			if a := (registry.Args{"id": string(se.ID)}); x.h.Available("github.sync", a) {
				errs = append(errs, x.h.Run(ctx, "github.sync", a))
			}
			x.h.Notify(core.Notice{ID: "review.pr.submit", Severity: core.SevInfo,
				Text: fmt.Sprintf("%s on %s, with %s.", sentWord(verdict), src.Label(), counted(len(sent), "comment"))})
			return errors.Join(errs...)
		})
}

func prVerdictWord(v string) string {
	return map[string]string{registry.VerdictApprove: "Approving", registry.VerdictChanges: "Requesting changes on", registry.VerdictComment: "Commenting on"}[v]
}

func sentWord(v string) string {
	return map[string]string{registry.VerdictApprove: "Approved", registry.VerdictChanges: "Changes requested", registry.VerdictComment: "Review posted"}[v]
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

func (x *prReviewPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil && !errors.Is(err, errStopped) {
		x.h.Notify(core.Notice{ID: "review.pr.error", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}
