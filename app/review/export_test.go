package review

import (
	"context"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// What the package's outside tests read of its plugins.

func (x *Reviews) Count() int     { return x.count() }
func (x *Reviews) PageOpen() bool { return x.page.open }
func (x *Reviews) ClosePage()     { x.page.open = false }

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
