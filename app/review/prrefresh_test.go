package review_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/review"
	"github.com/liamg/oriel/registry"
)

// A refresh asked for while the pull request is still being read is read once more after: the read under way may
// have begun before what changed (the PR merged), and its answer would stand.
func TestARefreshAskedMidReadReadsAgain(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	answers := make(chan string)
	s.Registry().Contribute(registry.CodeHost{Name: "slowhub", Title: "Slow hub", Sites: []registry.Site{{Host: "slow.example"}},
		Takes: regexp.MustCompile(`/pull/\d+$`),
		Lookup: func(ctx context.Context, _ registry.Site, _ string) (registry.ReviewDetail, error) {
			select {
			case st := <-answers:
				return registry.ReviewDetail{State: st}, nil
			case <-ctx.Done():
				return registry.ReviewDetail{}, ctx.Err()
			}
		}})
	x := apptest.PluginOf[*review.PRReview](s, "pr-review")
	url := "https://slow.example/acme/api/pull/1"
	x.PRDetail(url)   // the first read begins
	x.RefreshPR(url)  // asked again while it runs
	answers <- "open" // the first read's answer, from before the merge
	sent := make(chan struct{})
	go func() { answers <- "merged"; close(sent) }() // the read asked for after
	apptest.Recv(t, "the refresh asked mid-read to read again", sent)
	apptest.Poll(t, "the pull request to read what the second read said", func() bool { st, _ := x.PRDetail(url); return st == "merged" })
}
