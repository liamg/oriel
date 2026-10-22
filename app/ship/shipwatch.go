package ship

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// Ship's reads are subscriptions to its sessions' managers (registry.Watches):
//
//   - the session in front's git, read again when one of its work trees' refs or files change, at most every shipPoll;
//   - its pull requests and checks, polled every prTTL, less often while the window is behind another app;
//   - every other open session's open pull requests, polled every prBackground behind or in front, the polls together
//     kept to prBudget so many sessions don't run into the code host's rate limit.

// prBackground is how often an open pull request not in front is read: for the PrMerged event, and approvals.
const prBackground = 2 * time.Minute

// prRequests is about how many requests a pull request's read makes: the PR, its checks and reviews.
const prRequests = 5

// prBudget is the requests an hour background PR polls may make: under a third of GitHub's 5000, leaving the rest
// for what's on screen and what the user does.
const prBudget = 1500

// follow subscribes to what Ship reads now: the session in front's git and PRs, and the other sessions' open PRs.
func (x *Ship) follow() {
	st := x.h.State()
	keep := map[string]bool{}
	for _, se := range st.Sessions {
		if se.Archived {
			continue
		}
		m := kit.ManagerFor(x.h, se.ID)
		if m == nil {
			continue
		}
		id, front := se.ID, se.ID == st.Focus
		if front {
			local := x.localSource(se)
			keep[local.Key] = true
			x.subs.Hold(local.Key, m, watch.Need{MaxAge: shipPoll}, func() kit.Closer {
				return watch.Subscribe(m, local, watch.Need{MaxAge: shipPoll}, "ship: git")
			})
		}
		if !front && !x.openPRs(id) {
			continue
		}
		need := watch.Need{MaxAge: prTTL, Background: watch.Slow, Slow: prBackground}
		if !front {
			need = watch.Need{MaxAge: prBackground, Background: watch.Keep}
		}
		prs := x.prSource(id)
		keep["prs:"+string(id)] = true
		x.subs.Hold("prs:"+string(id), m, need, func() kit.Closer {
			return budgeted{watch.Subscribe(m, prs, need, "ship: pull requests"), x.budget.Use(prs.Key, prRequests)}
		})
	}
	x.subs.Only(keep)
}

// openPRs says the session's last read found one of its projects with a PR open.
func (x *Ship) openPRs(id core.SessionID) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	r := x.reports[id]
	return r != nil && slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.PR.Open() })
}

// localSource is a session's git as Ship reads it: read again when a writable project's refs or files change.
func (x *Ship) localSource(se core.SessionState) watch.Source[time.Time] {
	var deps []watch.AnySource
	var dirs []string
	if w, ok := registry.WatchesOf(x.h); ok {
		for _, p := range se.Projects {
			if p.ReadOnly || p.Dir == "" {
				continue
			}
			t := w.Tree(se.ID, p.Dir)
			deps = append(deps, t.Refs(), t.Work())
			dirs = append(dirs, p.Dir)
		}
	}
	id := se.ID
	return watch.Source[time.Time]{Key: "ship.git:" + string(id) + ":" + strings.Join(dirs, ":"), Deps: deps,
		Read: func(watch.Reader) (time.Time, error) {
			x.read(id, false)
			return time.Now(), nil
		}}
}

// prSource is a session's pull requests and checks, polled within the budget.
func (x *Ship) prSource(id core.SessionID) watch.Source[time.Time] {
	key := "ship.prs:" + string(id)
	return watch.Source[time.Time]{Key: key, Poll: true, Interval: func(maxAge time.Duration) time.Duration {
		if maxAge < prBackground {
			return maxAge // on screen: as asked
		}
		return x.budget.Interval(maxAge)
	}, Read: func(r watch.Reader) (time.Time, error) {
		x.readPRs(r.Context(), id)
		return time.Now(), nil
	}}
}

// readPRs asks the code hosts again for the PRs and checks of a session's projects as last read, without reading
// their git; a session not read yet is read whole.
func (x *Ship) readPRs(ctx context.Context, id core.SessionID) {
	x.mu.Lock()
	r := x.reports[id]
	var base []host.Ship
	if r != nil {
		for _, s := range r.ships {
			base = append(base, s.Ship)
		}
	}
	x.mu.Unlock()
	if len(base) == 0 {
		x.read(id, false)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ships := x.withPRs(ctx, id, base, false)
	for _, s := range ships {
		if strings.Contains(strings.ToLower(s.HostError), "rate limit") {
			x.budget.Limited()
		}
	}
	x.mirrorReviews(ctx, id, ships, false)
	x.mu.Lock()
	r = x.reports[id]
	if r == nil || r.loading || r.fixed {
		x.mu.Unlock()
		return // a whole read is under way, with the PRs too; or a fixture's in place
	}
	r.ships, r.read = ships, time.Now()
	seen, merged, asks := x.newPRs(id, ships)
	x.mu.Unlock()
	x.fire(id, seen, merged, asks)
	x.h.Redraw()
}

// budgeted is a PR subscription, counted against the budget until it's closed.
type budgeted struct {
	watch.Sub[time.Time]
	release func()
}

func (b budgeted) Close() {
	b.Sub.Close()
	b.release()
}
