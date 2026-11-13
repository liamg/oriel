package ship

import (
	"context"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/watch"
)

// Ship's reads are subscriptions to its sessions' managers (registry.Watches):
//
//   - the git of each session with a tab, each project read again when its work tree's refs or files change, at
//     most every shipPoll: the others' readings stand, and a session coming to the front has nothing to read;
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

// follow subscribes to what Ship reads now: the git of the sessions with tabs, the session in front's PRs, and the
// other sessions' open PRs. It returns the sessions whose git it has started watching, which want reading whole.
func (x *Ship) follow() (started []core.SessionID) {
	st := x.h.State()
	keep := map[string]bool{}
	watching := map[core.SessionID]bool{}
	for _, se := range st.Sessions {
		if se.Archived {
			continue
		}
		m := kit.ManagerFor(x.h, se.ID)
		if m == nil {
			continue
		}
		id, front := se.ID, se.ID == st.Focus
		if front || slices.Contains(st.Tabs, se.ID) {
			watching[id] = true
			for _, local := range x.localSources(se) {
				keep[local.Key] = true
				x.subs.Hold(local.Key, m, watch.Need{MaxAge: shipPoll}, func() kit.Closer {
					return watch.Subscribe(m, local, watch.Need{MaxAge: shipPoll}, "ship: git")
				})
			}
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
	x.subs.KeepOnly(keep)
	x.mu.Lock()
	x.projects = map[core.SessionID][]core.Project{}
	for _, se := range st.Sessions {
		x.projects[se.ID] = se.Projects
	}
	for id := range watching {
		if !x.watching[id] {
			started = append(started, id)
		}
	}
	x.watching = watching
	x.mu.Unlock()
	return started
}

// openPRs says the session's last read found one of its projects with a PR open.
func (x *Ship) openPRs(id core.SessionID) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	r := x.reports[id]
	return r != nil && slices.ContainsFunc(r.ships, func(s shipRead) bool { return s.PR.Open() })
}

// localSources are a session's git as Ship reads it: each writable project read again when its refs or files change.
func (x *Ship) localSources(se core.SessionState) []watch.Source[time.Time] {
	w, ok := registry.WatchesOf(x.h)
	if !ok {
		return nil
	}
	var out []watch.Source[time.Time]
	for _, p := range se.Projects {
		if p.ReadOnly || p.Dir == "" {
			continue
		}
		t := w.Tree(se.ID, p.Dir)
		id, name := se.ID, p.Name
		var looked atomic.Bool
		out = append(out, watch.Source[time.Time]{Key: "ship.git:" + string(id) + ":" + p.Dir, Deps: []watch.AnySource{t.Refs(), t.Work()},
			Read: func(watch.Reader) (time.Time, error) {
				x.busy.Add(1) // a read the watch runs is work under way like any other (Busy)
				defer x.busy.Done()
				x.readProject(id, name, !looked.Swap(true))
				return time.Now(), nil
			}})
	}
	return out
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
		x.busy.Add(1)
		defer x.busy.Done()
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
	var gitRead []time.Time
	if r != nil {
		for _, s := range r.ships {
			base, gitRead = append(base, s.Ship), append(gitRead, s.GitRead)
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
	for i := range ships {
		ships[i].GitRead = gitRead[i]
	}
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
