package review

import (
	"context"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// What the package's outside tests read of its plugins.

func (x *Reviews) Count() int     { return x.count() }
func (x *Reviews) PageOpen() bool { return x.page.open }
func (x *Reviews) ClosePage()     { x.page.open = false }

// SetClock has the page judge how old lists and details are by now.
func (x *Reviews) SetClock(now func() time.Time) {
	x.mu.Lock()
	x.now = now
	x.mu.Unlock()
}

// Fresh asks s for its list now, and gives the ask under way.
func (x *Reviews) Fresh(s registry.HostSite) <-chan struct{} { return x.fresh(s, 0) }

// Titles are the titles of the requests listed from site.
func (x *Reviews) Titles(site string) (out []string) {
	for _, r := range x.rows() {
		if r.req != nil && strings.Contains(r.req.URL, site) {
			out = append(out, r.req.Title)
		}
	}
	return out
}

type ReviewLine = reviewLine

func (x *PRReview) Checkout(ctx context.Context, spec *core.SessionSpec) error {
	return x.prCheckout(ctx, spec)
}
func (x *PRReview) Name(spec *core.SessionSpec) { x.prName(spec) }

var (
	ReviewDraft = reviewDraft
	PRProject   = prProject
	PRSession   = prSession
)

// PRDetail is the pull request at url as the review page last read it: asked for the first time it's wanted.
func (x *PRReview) PRDetail(url string) (state string, done bool) {
	s, _ := kitSites(x).URL(url)
	d := x.detail(s, url)
	x.mu.Lock()
	defer x.mu.Unlock()
	return d.d.State, d.done
}

// RefreshPR asks the host for it again.
func (x *PRReview) RefreshPR(url string) { x.refresh(url) }

func kitSites(x *PRReview) registry.CodeSites { return kit.SitesOf(x.h) }
