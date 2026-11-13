package review_test

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/liamg/oriel/registry"
)

// heldHost is a code host whose next ask for your reviews, once hold is set, waits until it's let go: Refresh pressed
// while an ask is under way, as a poll's can be.
type heldHost struct {
	mu      sync.Mutex
	titles  []string
	hold    bool
	entered chan struct{} // closed as the held ask starts
	release chan struct{}
}

func (h *heldHost) requested(context.Context, registry.Site) ([]registry.ReviewRequest, error) {
	h.mu.Lock()
	held := h.hold
	h.hold = false
	titles := append([]string(nil), h.titles...) // what the host had when asked: a change after doesn't reach this answer
	h.mu.Unlock()
	if held {
		close(h.entered)
		<-h.release
	}
	var out []registry.ReviewRequest
	for i, t := range titles {
		n := 50 + i
		out = append(out, registry.ReviewRequest{URL: "https://code.example/acme/gw/-/merge_requests/" + string(rune('0'+n%10)), Repo: "acme/gw",
			Number: n, Title: t, Author: "rlopez"})
	}
	return out, nil
}

// Refresh pressed while the list is being asked for isn't lost: once that ask answers, with what it read before the
// change, the list is asked for again, and shows the change.
func TestRefreshDuringAnAskAsksAgain(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	h := &heldHost{titles: []string{"Drop the legacy v1 webhooks"}, entered: make(chan struct{}), release: make(chan struct{})}
	s.Registry().Contribute(registry.CodeHost{Name: "held", Title: "Held", Sites: []registry.Site{{Host: "code.example"}},
		Takes: regexp.MustCompile(`/-/merge_requests/\d+`), Requested: h.requested})
	s.Run("reviews.open", nil)
	s.Until("the first answer", func() bool { return strings.Contains(s.Text(), "Drop the legacy v1 webhooks") })

	h.mu.Lock()
	h.hold = true // the next ask, which this refresh starts, waits until it's let go
	h.mu.Unlock()
	s.Run("reviews.refresh", nil)
	<-h.entered // the second ask is under way, and answers with the list as it was
	h.mu.Lock()
	h.titles = append(h.titles, "Rotate the signing keys")
	h.mu.Unlock()
	s.Run("reviews.refresh", nil) // pressed again while it's out
	close(h.release)
	s.Until("the change, asked for again", func() bool { return strings.Contains(s.Text(), "Rotate the signing keys") })
}
