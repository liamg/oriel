package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// reviewsSoon is how old a source's list may be when the Reviews page opens before it's asked again.
const reviewsSoon = time.Minute

var tRailBadgeReview = theme.Declare("rail.badge.review", "violet")

// reviewsPlugin is everything about review requests that isn't a source's own, built once on every
// registry.HostSite: the Reviews page, the sidebar's pill, a notice on a new ask, the omnibox's rows, Start and
// Continue review, and the assistant's review_requests.
type reviewsPlugin struct {
	disposer
	sh     *shell
	h      plugin.Host
	ctx    context.Context
	cancel context.CancelFunc
	poller sync.WaitGroup

	mu      sync.Mutex
	lists   map[string]*reviewList   // by source
	details map[string]*reviewDetail // by URL, as the page asked
	page    reviewsPage
}

// reviewList is what a source last said of the pull requests waiting on you.
type reviewList struct {
	reqs     []registry.ReviewRequest
	err      string
	answered bool
	at       time.Time     // when it answered; zero to ask again
	busy     chan struct{} // closed when the ask under way answers
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

func (x *reviewsPlugin) Manifest() plugin.Manifest { return plugin.Manifest{ID: "reviews"} }

func (x *reviewsPlugin) Activate(_ context.Context, h plugin.Host) error {
	x.h, x.lists, x.details = h, map[string]*reviewList{}, map[string]*reviewDetail{}
	x.ctx, x.cancel = context.WithCancel(context.Background())
	for _, o := range []registry.OptionSpec{
		{Name: "reviews.poll", Section: "git", Type: registry.Duration, Default: 5 * time.Minute, Scopes: registry.ScopeUser,
			Help: "How often every review source is asked for the pull requests waiting on your review, while Oriel is open."},
		{Name: "reviews.count_teams", Section: "git", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
			Help: "Count a review asked of a team you're on toward the Reviews pill, as well as those asked of you."},
		{Name: "reviews.notify", Section: "git", Type: registry.Bool, Default: true, Scopes: registry.ScopeUser,
			Help: "Tell you when someone asks for your review."},
	} {
		x.add(h.Option(o))
	}
	x.add(h.Picker(x.source()))
	has := func(registry.Args) bool { return x.any() }
	open := func(registry.Args) bool { return x.page.open }
	for _, c := range []registry.Command{
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
		{ID: "reviews.browse", Title: "Open the pull request selected on the Reviews page in the browser", Available: open,
			Run: func(registry.Ctx, registry.Args) error { return x.pageCommand("browse") }},
		{ID: "reviews.filter", Title: "Filter the Reviews page's list", Available: open,
			Run: func(registry.Ctx, registry.Args) error { x.page.focusFilter = true; return nil }},
		{ID: "reviews.scope", Title: "Narrow the Reviews page to {scope}: all, me (asked of you) or teams (asked of a team of yours); with none, the next", Available: open,
			Run: func(_ registry.Ctx, a registry.Args) error { return x.setScope(a.String("scope")) }},
	} {
		c.Source = "builtin:reviews"
		if c.Available == nil {
			c.Available = has
		}
		x.add(h.Command(c))
	}
	for _, k := range []registry.Keymap{
		{Keys: "<CR>", Command: "reviews.start", Desc: "Start review, or continue"},
		{Keys: "o", Command: "reviews.browse", Desc: "Open in the browser"},
		{Keys: "r", Command: "reviews.refresh", Desc: "Refresh"},
		{Keys: "/", Command: "reviews.filter", Desc: "Filter"},
		{Keys: "t", Command: "reviews.scope", Desc: "All, to me, my teams"},
	} {
		k.Mode = ReviewsKeyMode
		x.add(h.Keymap(k))
	}
	x.add(h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-V>", Command: "reviews.open", Desc: "Reviews"}))
	x.add(h.View(OverlayPrefix+"reviews", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "reviews", Fn: x.pageView} })))
	shown := func(string) bool { return x.any() }
	x.add(h.Contribute(registry.RailEntry{Name: "reviews", Icon: "pullrequest", Title: "Reviews", Command: "reviews.open", Scope: registry.ScopeGlobalEntry,
		Order: 6, Badge: x.count, BadgeTone: "rail.badge.review", Active: func(string) bool { return x.page.open }, Shown: shown, Source: "builtin:reviews"}))
	x.add(h.Contribute(registry.StartEntry{Name: "reviews", Icon: "pullrequest", Title: "Reviews", Command: "reviews.open", Order: 15, Link: true,
		Shown: x.any, Source: "builtin:reviews", Note: func() string {
			if n := x.count(); n > 0 {
				return fmt.Sprintf("%d waiting on you", n)
			}
			return ""
		}}))
	x.add(h.Contribute(registry.AgentTool{Name: "review_requests", For: []string{assistantAgent}, ReadOnly: true, Reads: "reviews",
		Source: "builtin:reviews", Run: x.toolReviewRequests,
		Desc: "Pull requests waiting on the user's review, from every code host (GitHub…), oldest ask first, then those they're part way through: number, title, repo, url, author, source, state (new, re-requested, changes, commented), the team asked if not them, draft, when asked, and the session on it if there is one."}))
	x.poller.Add(1)
	go x.poll()
	return nil
}

func (x *reviewsPlugin) Deactivate(ctx context.Context) error {
	x.cancel()
	x.poller.Wait()
	return x.disposer.Deactivate(ctx)
}

// poll asks each source again once its list is older than reviews.poll.
func (x *reviewsPlugin) poll() {
	defer x.poller.Done()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		for _, s := range x.sh.sites.reviewSources() {
			x.sh.sites.warm(x.ctx, s)
			x.fresh(s, x.pollEvery())
		}
		select {
		case <-x.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (x *reviewsPlugin) pollEvery() time.Duration {
	v, _, _ := x.h.Options().Get("reviews.poll", registry.At{})
	if d, ok := v.(time.Duration); ok && d > 0 {
		return d
	}
	return 5 * time.Minute
}

func (x *reviewsPlugin) optBool(name string) bool {
	v, _, _ := x.h.Options().Get(name, registry.At{})
	b, ok := v.(bool)
	return b || !ok
}

// any says whether a source is registered.
func (x *reviewsPlugin) any() bool { return len(x.sh.sites.reviewSources()) > 0 }

// needsKey says the source can't answer until its secret is set.
func (x *reviewsPlugin) needsKey(s registry.HostSite) bool { return !x.sh.sites.keyed(s) }

// owns says url is a pull request on s.
func (x *reviewsPlugin) owns(s registry.HostSite, url string) bool {
	hs, ok := x.sh.sites.url(url)
	return ok && hs.Key() == s.Key()
}

// prSession is the session reviewing url: a live one before an archived one, then the newest.
func prSession(h plugin.Host, url string) (core.Session, bool) {
	url = strings.TrimSuffix(strings.TrimSpace(url), "/")
	if url == "" {
		return nil, false
	}
	return newestSession(h, func(st core.SessionState) bool { return strings.EqualFold(strings.TrimSuffix(st.PR, "/"), url) })
}

// fresh asks a source for its list when it has none or it's older than stale, and gives the ask under way, if any.
func (x *reviewsPlugin) fresh(s registry.HostSite, stale time.Duration) <-chan struct{} {
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
	if l.busy != nil {
		return l.busy
	}
	if !l.at.IsZero() && time.Since(l.at) < stale {
		return nil
	}
	done := make(chan struct{})
	l.busy = done
	go func() {
		ctx, cancel := context.WithTimeout(x.ctx, 2*time.Minute)
		defer cancel()
		rs, err := registry.RequestedReviews(ctx, s)
		refused := x.sh.sites.refuse(s, err)
		x.mu.Lock()
		before, had := l.reqs, l.answered
		l.busy, l.at, l.answered = nil, time.Now(), true
		if refused {
			l.at = time.Time{}
		}
		if err != nil {
			l.err = err.Error()
		} else {
			l.reqs, l.err = rs, ""
			for url := range x.details {
				if x.owns(s, url) {
					delete(x.details, url)
				}
			}
		}
		x.mu.Unlock()
		close(done)
		if err == nil && had {
			x.notifyNew(s, before, rs)
		}
		x.h.Redraw()
	}()
	return done
}

// notifyNew tells the user of each ask that counts and wasn't in the source's last answer.
func (x *reviewsPlugin) notifyNew(s registry.HostSite, before, now []registry.ReviewRequest) {
	if !x.optBool("reviews.notify") {
		return
	}
	teams := x.optBool("reviews.count_teams")
	was := map[string]string{}
	for _, r := range before {
		was[r.URL] = r.State
	}
	for _, r := range now {
		if !r.Counts(teams) {
			continue
		}
		if st, ok := was[r.URL]; ok && (st == r.State || st == registry.ReviewNew) {
			continue
		}
		x.h.Notify(core.Notice{ID: "reviews.asked:" + r.URL, Severity: core.SevInfo, Source: s.Source, Text: askedText(r), Time: time.Now(),
			Actions: []core.Action{{Title: "Reviews", Command: "reviews.open"}}})
	}
}

// askedText is a new ask as a notice says it: "mkhan asked you to review api#812".
func askedText(r registry.ReviewRequest) string {
	who := cmpOr(r.Author, "Someone")
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
func (x *reviewsPlugin) stale(name string) {
	x.mu.Lock()
	if l := x.lists[name]; l != nil {
		l.at = time.Time{}
	}
	x.mu.Unlock()
}

func (x *reviewsPlugin) refreshAll() {
	for _, s := range x.sh.sites.reviewSources() {
		x.stale(s.Key())
		x.fresh(s, 0)
	}
}

// list is a source's requests as last told, why there are none, and whether it has answered.
func (x *reviewsPlugin) list(name string) ([]registry.ReviewRequest, string, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	l := x.lists[name]
	if l == nil {
		return nil, "", false
	}
	return l.reqs, l.err, l.answered
}

// listAt is when a source last answered; zero if it hasn't.
func (x *reviewsPlugin) listAt(name string) time.Time {
	x.mu.Lock()
	defer x.mu.Unlock()
	if l := x.lists[name]; l != nil && l.answered {
		return l.at
	}
	return time.Time{}
}

// rows are every source's requests in one order, those you've approved left out, then each source's key ask or
// failure.
func (x *reviewsPlugin) rows() []reviewRow {
	var reqs []registry.ReviewRequest
	var foot []reviewRow
	sources := map[string]registry.HostSite{}
	for _, s := range x.sh.sites.reviewSources() {
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
			if r.State != registry.ReviewApproved {
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
func (x *reviewsPlugin) count() int {
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
		parts = append(parts, counted(r.Since, "commit")+" since yours")
	case !r.Waiting() && r.Since == 0:
		parts = append(parts, "no commits since")
	case !r.Waiting():
		parts = append(parts, counted(r.Since, "commit")+" since")
	}
	return strings.Join(parts, " · ")
}

// start starts a review of the row's pull request, or brings its session back; a source's row sets its key or asks
// again.
func (x *reviewsPlugin) start(ctx context.Context, r reviewRow) error {
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
	return x.h.Run(ctx, "session.new", registry.Args{"kind": string(core.KindReview), "pr": r.req.URL})
}

func (x *reviewsPlugin) source() registry.PickerSource {
	return registry.PickerSource{ID: "reviews", Title: "Reviews", Order: 42, Available: x.any, Source: "builtin:reviews",
		Items: func(registry.Ctx, string) []registry.PickerItem {
			var out []registry.PickerItem
			for _, s := range x.sh.sites.reviewSources() {
				x.fresh(s, x.pollEvery())
			}
			for _, r := range x.rows() {
				it := registry.PickerItem{Icon: "⇄", IconStyle: "status.permission", Data: r}
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
		Preview: func(it registry.PickerItem) ui.Node {
			r, _ := it.Data.(reviewRow)
			return &ui.Func{Key: "review", Fn: func(c *ui.Ctx) {
				th := c.Theme()
				c.Column("p", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					if r.req == nil {
						c.Text("t", it.Label, uiFont(12, th.C(theme.FgMuted)))
						return
					}
					q := r.req
					tone := ui.TextStyle{Color: tokenColour(th, stateTone(r), th.C(theme.Accent))}
					previewHead(c, strings.ToUpper(stateLabel(r)), tone, q.Title)
					previewField(c, "pr", q.Key())
					previewField(c, "author", q.Author)
					previewField(c, "asked", askedWhom(*q))
					hint := "↵ starts a review: the launcher on Review PR"
					if r.se != nil {
						previewField(c, "session", r.se.Name())
						hint = "↵ continues it in " + r.se.Name()
					}
					c.Text("hint", hint, uiFont(11, th.C(theme.FgFaint)))
				})
			}}
		},
		Run: func(ctx registry.Ctx, it registry.PickerItem) error {
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
func (x *reviewsPlugin) toolReviewRequests(ctx registry.Ctx, _ map[string]any) (any, error) {
	sources := x.sh.sites.reviewSources()
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
