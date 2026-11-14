//oriel:kind review

package review

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/watch"
)

// reviewsSoon is how old a source's list, or a PR's detail, may be when the Reviews page shows it before it's asked
// again; an older one is shown while it's asked.
const reviewsSoon = 2 * time.Minute

var tRailBadgeReview = theme.Declare("rail.badge.review", "violet")

// Reviews is everything about review requests that isn't a source's own, built once on every
// registry.HostSite: the Reviews page, the sidebar's pill, a notice on a new ask, the omnibox's rows, Start and
// Continue review, and the assistant's review_requests.
type Reviews struct {
	kit.Disposer
	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc
	subs   kit.Subs // the lists' poll, from the app's manager

	mu      sync.Mutex
	lists   map[string]*reviewList   // by source
	details map[string]*reviewDetail // by URL, as the page asked
	page    reviewsPage
	clock   clock.Clock // when lists and details were read; the system's if nil
}

func (x *Reviews) now() time.Time { return clock.Or(x.clock).Now() }

// reviewList is what a source last said of the pull requests waiting on you.
type reviewList struct {
	reqs     []registry.ReviewRequest
	err      string
	answered bool
	at       time.Time    // when it answered; zero to ask again
	ask      work.Refetch // the ask under way
	since    time.Time    // when the ask under way started
}

// reviewRow is a row of the page and the omnibox: a request, or what a source needs first.
type reviewRow struct {
	src    registry.HostSite
	req    *registry.ReviewRequest
	se     core.Session // the session reviewing it, if any
	action string       // "key" or "refresh" when there's no request
	err    string
}

// waiting says the row is yours to review, and no session has it yet.
func (r reviewRow) waiting() bool { return r.req != nil && r.req.Waiting() && r.se == nil }

func (x *Reviews) Manifest() plugin.Manifest { return plugin.Manifest{ID: "reviews"} }

func (x *Reviews) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.lists, x.details = h, map[string]*reviewList{}, map[string]*reviewDetail{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	x.Add(h.Contribute(registry.Busy{Name: "reviews", Busy: x.asking}))
	for _, o := range x.options() {
		x.Add(h.Option(o))
	}
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	for _, k := range x.keymaps() {
		x.Add(h.Keymap(k))
	}
	x.Add(h.Picker(x.source()))
	x.Add(h.View(kit.OverlayPrefix+"reviews", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return &ui.Func{Key: "reviews", Fn: x.pageView} })))
	shown := func(string) bool { return x.any() }
	x.Add(h.Contribute(kit.PagePlace("reviews", "Reviews", "reviews.open", &x.page.open)))
	x.Add(h.Contribute(uireg.RailEntry{Name: "reviews", Icon: "pullrequest", Title: "Reviews", Command: "reviews.open", Scope: uireg.ScopeGlobalEntry,
		Order: 6, Badge: x.count, BadgeTone: "rail.badge.review", Active: func(string) bool { return x.page.open }, Shown: shown}))
	x.Add(h.Contribute(uireg.StartEntry{Name: "reviews", Icon: "pullrequest", Title: "Reviews", Desc: "Pull requests that ask for your review",
		Command: "reviews.open", Order: 26, Shown: x.any, Note: func() string {
			if n := x.count(); n > 0 {
				return fmt.Sprintf("%d waiting on you", n)
			}
			return ""
		}}))
	for _, it := range x.menuItems() {
		x.Add(h.Contribute(it))
	}
	x.Add(h.Contribute(registry.AgentTool{Name: "review_requests", For: []string{kit.AssistantAgent}, ReadOnly: true, Reads: "reviews",
		Run:  x.toolReviewRequests,
		Desc: "Pull requests waiting on the user's review, from every code host (GitHub…), oldest ask first, then those they're part way through: number, title, repo, url, author, source, state (new, re-requested, changes, commented), the team asked if not them, draft, when asked, and the session on it if there is one."}))
	x.poll()
	return nil
}

// requestSubject is what a Reviews row stands for, before its pull request's URL.
const requestSubject = "review-request:"

// menuItems are a Reviews row's right-click menu: start its review as ↵ does, or in the background on the defaults.
func (x *Reviews) menuItems() []registry.MenuItem {
	onRow := func(fn func(ctx registry.Ctx, r reviewRow) error) func(registry.Ctx, registry.MenuCtx) error {
		return func(ctx registry.Ctx, at registry.MenuCtx) error {
			url := strings.TrimPrefix(at.Subject, requestSubject)
			for _, r := range x.rows() {
				if r.req != nil && r.req.URL == url {
					return fn(ctx, r)
				}
			}
			return fmt.Errorf("no review request %s", url)
		}
	}
	reviewing := func(at registry.MenuCtx) string {
		if se, ok := prSession(x.h, strings.TrimPrefix(at.Subject, requestSubject)); ok {
			return se.Name() + " has it"
		}
		return ""
	}
	when := registry.MenuWhen{Subject: requestSubject}
	return []registry.MenuItem{
		{Name: "reviews.start", Label: "Start review", Key: "↵", Group: "review", Order: 10, When: when, Source: "builtin:reviews",
			Run: onRow(func(_ registry.Ctx, r reviewRow) error { x.act(r); return nil })},
		{Name: "reviews.start-background", Label: "Start review in background", Group: "review", Order: 20, When: when, Disabled: reviewing,
			Source: "builtin:reviews", Run: onRow(func(ctx registry.Ctx, r reviewRow) error { return x.startBehind(ctx, r.req.URL) })},
	}
}

// options say how often the sources are asked, and what counts.
func (x *Reviews) options() []registry.OptionSpec {
	return []registry.OptionSpec{
		{Name: "reviews.poll", Section: "git", Type: registry.Duration, Default: 5 * time.Minute, Scopes: registry.ScopeUser,
			Help: "How often every review source is asked for the pull requests waiting on your review, while Oriel is open."},
		{Name: "reviews.count_teams", Section: "git", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
			Help: "Count a review asked of a team you're on toward the Reviews pill, as well as those asked of you."},
		{Name: "reviews.notify", Section: "git", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
			Help: "Tell you when someone asks for your review."},
		{Name: "reviews.max_age", Section: "git", Type: registry.Duration, Default: 30 * 24 * time.Hour, Scopes: registry.ScopeUser,
			Help: "Leave out review requests asked longer ago than this, everywhere they're listed or counted; 0 shows them all."},
	}
}

// commands open the Reviews page and act on what it lists.
func (x *Reviews) commands() []registry.Command {
	has := func(registry.Args) bool { return x.any() }
	open := func(registry.Args) bool { return x.page.open }
	out := []registry.Command{
		{ID: "Reviews", Title: "The Reviews page: pull requests waiting on your review; :Reviews github for one source's", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("args"))
		}},
		{ID: "reviews.open", Title: "Open the Reviews page: every source's pull requests waiting on your review, or one {source}'s", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.openPage(a.String("source"))
		}},
		{ID: "reviews.refresh", Title: "Ask every review source for the pull requests waiting on you again now", Run: func(registry.Ctx, registry.Args) error {
			x.refreshAll()
			return nil
		}},
		{ID: "reviews.start", Title: "Start a review of the pull request selected on the Reviews page, or continue its session", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("start") }},
		{ID: "reviews.start-background", Title: "Start a review of pull request {url} behind the session in front, on the default agent",
			Available: func(registry.Args) bool { return kit.HasSessions(x.h) },
			Run:       func(ctx registry.Ctx, a registry.Args) error { return x.startBehind(ctx, a.String("url")) }},
		{ID: "reviews.browse", Title: "Open the pull request selected on the Reviews page in the browser", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("browse") }},
		{ID: "reviews.filter", Title: "Filter the Reviews page's list", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.page.focusFilter = true; return nil }},
		{ID: "reviews.scope", Title: "Narrow the Reviews page to {scope}: all, me (asked of you) or teams (asked of a team of yours); with none, the next", Available: open,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.setScope(a.String("scope")) }},
	}
	for i := range out {
		c := &out[i]
		c.Source = "builtin:reviews"
		if c.Available == nil {
			c.Available = has
		}
	}
	return out
}

// keymaps are the Reviews page's keys, and the global one that opens it.
func (x *Reviews) keymaps() []registry.Keymap {
	out := []registry.Keymap{
		{Keys: "<CR>", Command: "reviews.start", Desc: "Start review, or continue"},
		{Keys: "o", Command: "reviews.browse", Desc: "Open in the browser"},
		{Keys: "r", Command: "reviews.refresh", Desc: "Refresh"},
		{Keys: "/", Command: "reviews.filter", Desc: "Filter"},
		{Keys: "t", Command: "reviews.scope", Desc: "All, to me, my teams"},
	}
	for i := range out {
		out[i].Mode = ReviewsKeyMode
	}
	return append(out, registry.Keymap{Mode: "global", Keys: "<D-V>", Command: "reviews.open", Desc: "Reviews"})
}

func (x *Reviews) Deactivate(ctx context.Context) error {
	x.cancel()
	x.subs.Close()
	return x.Disposer.Deactivate(ctx)
}

// reviewsCheck is how often the sources' lists are checked for being older than reviews.poll; behind another app,
// every kit.UnfocusedPoll.
const reviewsCheck = 15 * time.Second

// poll keeps the sources' lists fresh: a subscription to the app's manager whose read asks each source again once
// its list is older than reviews.poll.
func (x *Reviews) poll() {
	m := kit.ManagerFor(x.h, "")
	if m == nil {
		return
	}
	need := watch.Need{MaxAge: reviewsCheck, Background: watch.Slow, Slow: kit.UnfocusedPoll}
	x.subs.Hold("reviews", m, need, func() kit.Closer {
		src := kit.Every("reviews.requested", func(ctx context.Context) error {
			for _, s := range kit.SitesOf(x.h).ReviewSources() {
				kit.SitesOf(x.h).Warm(ctx, s)
				x.fresh(s, x.pollEvery())
			}
			return nil
		}, nil)
		return watch.Subscribe(m, src, need, "reviews: requested")
	})
}

// tooOld says r was asked longer ago than reviews.max_age; one with no time asked never is.
func (x *Reviews) tooOld(r registry.ReviewRequest) bool {
	d, _ := registry.Option[time.Duration](x.h.Options(), "reviews.max_age", registry.At{})
	if d <= 0 || r.Asked.IsZero() {
		return false
	}
	now := time.Now()
	if u := x.h.UI(); u != nil {
		now = u.Now()
	}
	return now.Sub(r.Asked) > d
}

func (x *Reviews) pollEvery() time.Duration {
	if d, ok := registry.Option[time.Duration](x.h.Options(), "reviews.poll", registry.At{}); ok && d > 0 {
		return d
	}
	return 5 * time.Minute
}

// optBool is a boolean option, true unless it's set false.
func (x *Reviews) optBool(name string) bool {
	b, ok := registry.Option[bool](x.h.Options(), name, registry.At{})
	return b || !ok
}

// any says whether a source is registered.
func (x *Reviews) any() bool { return len(kit.SitesOf(x.h).ReviewSources()) > 0 }

// needsKey says the source can't answer until its secret is set.
func (x *Reviews) needsKey(s registry.HostSite) bool { return !kit.SitesOf(x.h).Keyed(s) }

// prSession is the session reviewing url: a live one before an archived one, then the newest.
func prSession(h plugin.Host, url string) (core.Session, bool) {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	if url == "" {
		return nil, false
	}
	return kit.NewestSession(h, func(st core.SessionState) bool { return strings.EqualFold(strings.TrimSuffix(reviewPR(st), "/"), url) })
}

// asking says a code host's been asked for its requests and hasn't answered.
func (x *Reviews) asking() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	for _, l := range x.lists {
		if l.ask.Busy() != nil {
			return true
		}
	}
	return false
}

// fresh asks a source for its list when it has none or it's older than stale, and gives the ask under way, if any.
func (x *Reviews) fresh(s registry.HostSite, stale time.Duration) <-chan struct{} {
	if s.Requested == nil || x.needsKey(s) {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[s.Key()]
	if l == nil {
		l = &reviewList{}
		x.lists[s.Key()] = l
	}
	if busy := l.ask.Busy(); busy != nil {
		l.ask.Begin(stale == 0) // a refresh: the ask under way may have started before what it's for
		return busy
	}
	if !l.at.IsZero() && x.now().Sub(l.at) < stale {
		return nil
	}
	done, _ := l.ask.Begin(false)
	l.since = kit.WindowNow(x.h)
	plugin.Go(x.h, "reviews: requests from "+s.Key(), func() {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		defer cancel()
		var rs []registry.ReviewRequest
		var err error
		for {
			rs, err = registry.RequestedReviews(ctx, s)
			x.mu.Lock()
			again := l.ask.Again() && err == nil && ctx.Err() == nil
			x.mu.Unlock()
			if !again {
				break
			}
		}
		refused := kit.SitesOf(x.h).Refuse(s, err)
		x.mu.Lock()
		before, had := l.reqs, l.answered
		l.ask.End()
		l.at, l.answered = x.now(), true
		if refused {
			l.at = time.Time{}
		}
		if err != nil {
			l.err = err.Error()
		} else {
			l.reqs, l.err = rs, ""
		}
		x.mu.Unlock()
		close(done)
		if err == nil && had {
			x.notifyNew(s, before, rs)
		}
		x.h.Redraw()
	})
	return done
}

// notifyNew tells the user of each ask that counts and wasn't in the source's last answer.
func (x *Reviews) notifyNew(s registry.HostSite, before, now []registry.ReviewRequest) {
	if !x.optBool("reviews.notify") {
		return
	}
	teams := x.optBool("reviews.count_teams")
	was := map[string]string{}
	for _, r := range before {
		was[r.URL] = r.State
	}
	for _, r := range now {
		if !r.Counts(teams) || x.tooOld(r) {
			continue
		}
		if st, ok := was[r.URL]; ok && (st == r.State || st == registry.ReviewNew) {
			continue
		}
		id := "reviews.asked:" + r.URL
		x.h.Notify(core.Notice{ID: id, Severity: core.SevQuestion, Source: s.Source, Title: "Review requested", Text: askedText(r),
			Actions: []core.Action{
				{Title: "Start review in background", Command: "reviews.start-background", Args: map[string]any{"url": r.URL}},
				{Title: "Ignore", Command: "notice.dismiss", Args: map[string]any{"id": id}},
			}})
	}
}

// askedText is a new ask as a notice says it: "mkhan asked you to review api#812".
func askedText(r registry.ReviewRequest) string {
	who := cmp.Or(r.Author, "Someone")
	whom := "you"
	if r.Via != "" {
		whom = r.Via
	}
	text := who + " asked " + whom + " to review " + shortKey(r)
	if r.State == registry.ReviewReRequested {
		text += " again"
	}
	return text
}

// shortKey is a request named by its repo's last part: "api#812".
func shortKey(r registry.ReviewRequest) string {
	repo := r.Repo
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		repo = repo[i+1:]
	}
	return fmt.Sprintf("%s#%d", repo, r.Number)
}

// stale marks a source's list to be asked for again.
func (x *Reviews) stale(name string) {
	x.mu.Lock()
	if l := x.lists[name]; l != nil {
		l.at = time.Time{}
	}
	x.mu.Unlock()
}

// refreshAll asks every source for its list again now, and for each PR's detail when it's next shown.
func (x *Reviews) refreshAll() {
	x.mu.Lock()
	for _, d := range x.details {
		d.stale = true
	}
	x.mu.Unlock()
	for _, s := range kit.SitesOf(x.h).ReviewSources() {
		x.stale(s.Key())
		x.fresh(s, 0)
	}
}

// listState is a source's list as the loading widgets read it.
func (x *Reviews) listState(name string) widget.LoadState {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	switch {
	case l == nil:
		return widget.LoadState{}
	case l.ask.Busy() != nil:
		return widget.LoadState{Phase: widget.Pending, Since: l.since, Stale: l.answered}
	case l.err != "" && l.reqs == nil:
		return widget.LoadState{Phase: widget.Failed, Err: errors.New(l.err)}
	}
	return widget.LoadState{Phase: widget.Ready}
}

// list is a source's requests as last told, why there are none, and whether it has answered.
func (x *Reviews) list(name string) ([]registry.ReviewRequest, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	if l == nil {
		return nil, "", false
	}
	return l.reqs, l.err, l.answered
}

// listAt is when a source last answered; zero if it hasn't.
func (x *Reviews) listAt(name string) time.Time {
	x.mu.Lock()
	defer x.mu.Unlock()
	if l := x.lists[name]; l != nil && l.answered {
		return l.at
	}
	return time.Time{}
}

// rows are every source's requests in one order, those you've approved and those older than reviews.max_age left
// out, then each source's key ask or failure.
func (x *Reviews) rows() []reviewRow {
	var reqs []registry.ReviewRequest
	var foot []reviewRow
	sources := map[string]registry.HostSite{}
	for _, s := range kit.SitesOf(x.h).ReviewSources() {
		if s.Requested == nil {
			continue
		}
		sources[s.Label()] = s
		if x.needsKey(s) {
			foot = append(foot, reviewRow{src: s, action: "key"})
			continue
		}
		rs, err, _ := x.list(s.Key())
		if err != "" && rs == nil {
			foot = append(foot, reviewRow{src: s, action: "refresh", err: err})
		}
		for _, r := range rs {
			if r.State != registry.ReviewApproved && !x.tooOld(r) {
				reqs = append(reqs, r)
			}
		}
	}
	var out []reviewRow
	for i := range reqs {
		r := reviewRow{src: sources[reqs[i].Source], req: &reqs[i]}
		r.se, _ = prSession(x.h, reqs[i].URL)
		out = append(out, r)
	}
	slices.SortStableFunc(out, func(a, b reviewRow) int {
		if a.waiting() != b.waiting() {
			if a.waiting() {
				return -1
			}
			return 1
		}
		return registry.CompareReviews(*a.req, *b.req)
	})
	return append(out, foot...)
}

// count is how many pull requests wait on your review: the sidebar's pill.
func (x *Reviews) count() int {
	teams := x.optBool("reviews.count_teams")
	n := 0
	for _, r := range x.rows() {
		if r.waiting() && r.req.Counts(teams) {
			n++
		}
	}
	return n
}

// stateLabel is a row's state as its chip says it.
func stateLabel(r reviewRow) string {
	switch {
	case r.se != nil:
		return "reviewing"
	case r.req.Draft:
		return "draft"
	}
	switch r.req.State {
	case registry.ReviewReRequested:
		return "re-requested"
	case registry.ReviewChanges:
		return "changes requested"
	case registry.ReviewCommented:
		return "commented"
	}
	return "new"
}

// stateTone is a row's state's colour.
func stateTone(r reviewRow) string {
	switch stateLabel(r) {
	case "new":
		return "status.permission"
	case "re-requested":
		return "error"
	case "reviewing":
		return "status.working"
	case "changes requested":
		return "warn"
	}
	return "fg.faint"
}

// reviewNote is the line under a request's title: repo#n, author, size, commits since yours.
func reviewNote(r registry.ReviewRequest) string {
	parts := []string{r.Key()}
	if r.Author != "" {
		parts = append(parts, r.Author)
	}
	if r.Adds+r.Dels > 0 {
		parts = append(parts, fmt.Sprintf("+%d −%d", r.Adds, r.Dels))
	}
	switch {
	case r.State == registry.ReviewReRequested && r.Since > 0:
		parts = append(parts, kit.Counted(r.Since, "commit")+" since yours")
	case !r.Waiting() && r.Since == 0:
		parts = append(parts, "no commits since")
	case !r.Waiting():
		parts = append(parts, kit.Counted(r.Since, "commit")+" since")
	}
	return strings.Join(parts, " · ")
}

// start starts a review of the row's pull request, or brings its session back; a source's row sets its key or asks
// again.
func (x *Reviews) start(ctx context.Context, r reviewRow) error {
	switch r.action {
	case "key":
		return x.h.Run(ctx, "code.setup", registry.Args{"code_host": r.src.Name, "hostname": r.src.Site.Host})
	case "refresh":
		x.stale(r.src.Key())
		x.fresh(r.src, 0)
		return nil
	}
	if r.se != nil {
		return x.h.Run(ctx, "session.open", registry.Args{"id": string(r.se.ID())})
	}
	if r.src.Checkout == nil {
		return fmt.Errorf("%s can't check out %s for a review", r.src.Label(), r.req.Key())
	}
	args := map[string]any{"kind": string(core.KindReview), "pr": r.req.URL}
	if x.cloneFirst(ctx, r, "session.new", args) {
		return nil
	}
	return x.h.Run(ctx, "session.new", registry.Args(args))
}

// cloneFirst offers to clone the PR's repo when no checkout of it is registered: Add repo on its clone URL, which runs
// command with args once the repo's added. It says whether it offered.
func (x *Reviews) cloneFirst(ctx context.Context, r reviewRow, command string, args map[string]any) bool {
	if r.req == nil || r.req.Repo == "" {
		return false
	}
	if name, _ := x.checkout(*r.req); name != "" {
		return false
	}
	h, req := x.h, *r.req
	_ = plugin.RunSlow(ctx, h, plugin.Slow{ID: "reviews.clone:" + req.URL, Doing: "Finding where to clone " + req.Repo + " from"},
		func(ctx context.Context) (string, error) { return x.cloneURL(ctx, r.src, req.URL), nil },
		func(ctx registry.Ctx, url string) error {
			return h.Run(ctx, "repos.new", registry.Args{"mode": "clone", "url": url,
				"note": "You don't have " + req.Repo + " cloned. Clone it to review " + shortKey(req) + "; the review starts once it's added.",
				"then": kit.Then(kit.Call{Command: command, Args: args})})
		})
	return true
}

// cloneURL is the clone URL of the repo of the PR at url: from its detail once read, else asked of its source; "" if
// the source doesn't say.
func (x *Reviews) cloneURL(ctx context.Context, src registry.HostSite, url string) string {
	x.mu.Lock()
	d := x.details[url]
	clone := ""
	if d != nil && d.done {
		clone = d.d.Clone
	}
	x.mu.Unlock()
	if clone != "" {
		return clone
	}
	got, err := registry.LookupReview(ctx, src, url)
	if err != nil {
		return ""
	}
	return got.Clone
}

// startBehind starts a review of url behind the session in front, on the agent the launcher would pick, and says
// so once it's up.
func (x *Reviews) startBehind(ctx registry.Ctx, url string) error {
	if url == "" {
		return errors.New("start a review of which pull request? {url}")
	}
	name, row := url, reviewRow{}
	for _, r := range x.rows() {
		if r.req != nil && r.req.URL == url {
			name, row = shortKey(*r.req), r
		}
	}
	open := func(se core.Session) []core.Action {
		return []core.Action{{Title: "Open", Command: "session.open", Args: map[string]any{"id": string(se.ID())}}}
	}
	if se, ok := prSession(x.h, url); ok {
		x.h.Notify(core.Notice{ID: "reviews.started:" + url, Severity: core.SevInfo, Source: "reviews", Text: se.Name() + " is already reviewing " + name,
			Actions: open(se)})
		return nil
	}
	if x.cloneFirst(ctx, row, "reviews.start-background", map[string]any{"url": url}) {
		return nil
	}
	h, st := x.h, x.h.State()
	w, err := kit.SessionCalls(h)
	if err != nil {
		return err
	}
	v, _, _ := h.Options().Get("sandbox.default", registry.At{})
	sandbox := v == "sandbox"
	return plugin.RunSlow(ctx, h, plugin.Slow{ID: "reviews.start:" + url, Doing: "Starting a review of " + name},
		func(ctx context.Context) (struct{}, error) {
			provider, model := kit.DefaultAgent(st, w.ProviderInfo(ctx))
			a := registry.Args{"kind": string(core.KindReview), "pr": url, "provider": provider, "model": model, "sandbox": sandbox, "background": true}
			if sandbox {
				a["network"] = "ask"
			}
			return struct{}{}, plugin.Await(ctx, func(ctx registry.Ctx) error { return h.Run(ctx, "session.start", a) })
		},
		func(registry.Ctx, struct{}) error {
			if se, ok := prSession(h, url); ok {
				h.Notify(core.Notice{ID: "reviews.started:" + url, Severity: core.SevInfo, Source: "reviews", Text: "Reviewing " + name + " in the background",
					Actions: open(se)})
			}
			return nil
		})
}

func (x *Reviews) source() uireg.PickerSource {
	return uireg.PickerSource{ID: "reviews", Title: "Reviews", Order: 42, Available: x.any, Source: "builtin:reviews",
		Loading: func() bool {
			for _, s := range kit.SitesOf(x.h).ReviewSources() {
				if kit.PendingFirst(x.listState(s.Key())) {
					return true
				}
			}
			return false
		},
		Items: func(registry.Ctx, string) []uireg.PickerItem {
			var out []uireg.PickerItem
			for _, s := range kit.SitesOf(x.h).ReviewSources() {
				x.fresh(s, x.pollEvery())
			}
			for _, r := range x.rows() {
				it := uireg.PickerItem{Icon: "⇄", IconStyle: "status.permission", Data: r}
				switch r.action {
				case "key":
					it.ID, it.Label, it.Detail = "key:"+r.src.Key(), "Set your "+r.src.Label()+" token", "to list the pull requests waiting on your review"
				case "refresh":
					it.ID, it.Label, it.IconStyle = "err:"+r.src.Key(), r.src.Label()+": "+r.err, "warn"
				default:
					it.ID, it.Label, it.Detail, it.Note = r.req.URL, r.req.Title, reviewNote(*r.req), "review"
					it.Badge, it.BadgeStyle = stateLabel(r), stateTone(r)
					if r.se != nil {
						it.Note = "Continue · " + r.se.Name()
					}
				}
				out = append(out, it)
			}
			return out
		},
		Preview: func(it uireg.PickerItem) ui.Node {
			r, _ := it.Data.(reviewRow)
			return &ui.Func{Key: "review", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					if r.req == nil {
						c.Text("t", it.Label, kit.UIFont(12, th.C(theme.FgMuted)))
						return
					}
					q := r.req
					tone := ui.TextStyle{Color: kit.TokenColour(th, stateTone(r), th.C(theme.Accent))}
					kit.PreviewHead(c, strings.ToUpper(stateLabel(r)), tone, q.Title)
					kit.PreviewField(c, "pr", q.Key())
					kit.PreviewField(c, "author", q.Author)
					kit.PreviewField(c, "asked", askedWhom(*q))
					hint := "↵ starts a review: the launcher on Review PR"
					if r.se != nil {
						kit.PreviewField(c, "session", r.se.Name())
						hint = "↵ continues it in " + r.se.Name()
					}
					c.Text("hint", hint, kit.UIFont(11, th.C(theme.FgFaint)))
				})
			}}
		},
		Run: func(ctx registry.Ctx, it uireg.PickerItem) error {
			r, _ := it.Data.(reviewRow)
			return x.start(ctx, r)
		},
	}
}

// askedWhom is who was asked: "you, directly" or "@acme/platform".
func askedWhom(r registry.ReviewRequest) string {
	if r.Via != "" {
		return r.Via
	}
	return "you, directly"
}

// reviewLine is a request as the assistant reads it.
type reviewLine struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Repo    string `json:"repo"`
	URL     string `json:"url"`
	Author  string `json:"author,omitempty"`
	Source  string `json:"source"`
	State   string `json:"state"`
	Via     string `json:"via,omitempty"`
	Draft   bool   `json:"draft,omitempty"`
	Asked   string `json:"asked,omitempty"`
	Session string `json:"session,omitempty"`
}

// toolReviewRequests is the assistant's review_requests: every source's requests as the page lists them, asked for
// first when they're stale, and why a source has none.
func (x *Reviews) toolReviewRequests(ctx registry.Ctx, _ map[string]any) (any, error) {
	sources := kit.SitesOf(x.h).ReviewSources()
	var waits []<-chan struct{}
	for _, s := range sources {
		if w := x.fresh(s, reviewsSoon); w != nil {
			waits = append(waits, w)
		}
	}
	wait, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, w := range waits {
		select {
		case <-w:
		case <-wait.Done():
		}
	}
	if len(sources) == 0 {
		return "No review source is installed: a code host's plugin brings the pull requests waiting on the user's review.", nil
	}
	out := []reviewLine{}
	var notes []string
	for _, r := range x.rows() {
		switch r.action {
		case "key":
			notes = append(notes, "No "+r.src.Label()+" token is set yet: the user sets it in Settings › Secrets.")
			continue
		case "refresh":
			notes = append(notes, r.src.Label()+": "+r.err)
			continue
		}
		q := r.req
		l := reviewLine{Number: q.Number, Title: q.Title, Repo: q.Repo, URL: q.URL, Author: q.Author, Source: q.Source, State: q.State, Via: q.Via, Draft: q.Draft}
		if !q.Asked.IsZero() {
			l.Asked = q.Asked.Format(time.RFC3339)
		}
		if r.se != nil {
			l.Session = string(r.se.ID())
		}
		out = append(out, l)
	}
	if len(notes) == 0 {
		return out, nil
	}
	return map[string]any{"reviews": out, "notes": notes}, nil
}
