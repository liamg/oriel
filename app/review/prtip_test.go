package review_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/script"
)

// hoverTip rests the pointer on the row whose title starts with title until its tooltip is up, and answers the
// frame's text, its lines run together.
func hoverTip(s *apptest.Scene, title string) string {
	s.T.Helper()
	at, ok := s.TextStart(title)
	if !ok {
		s.T.Fatalf("no row %q:\n%s", title, s.Text())
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: at.X + 8, Y: at.Y - 4}})
	s.Frame()
	s.Now = s.Now.Add(time.Second)
	s.Frame()
	return strings.ReplaceAll(s.Text(), "\n", " ")
}

// Resting the pointer on a pull request in the list says what the row hasn't room for: its whole title, where it
// is, who opened it, who's asked and who has reviewed it, and which branch goes into which.
func TestReviewsRowTooltipSaysWhoAndWhere(t *testing.T) {
	s, _, hub, prs := reviewScene(t)
	n := prs["ratelimit"].Number
	hub.AddReview("acme/api", n, gh.Review{User: "jsato", State: gh.ReviewApproved})
	s.Run("reviews.open", nil)
	s.Run("reviews.refresh", nil)
	s.Quiet()
	s.Until("the requests", func() bool { return strings.Contains(s.Text(), "Retry flaky S3") })
	text := hoverTip(s, "Rate-limit the search")
	for _, want := range []string{"acme/api#" + strconv.Itoa(n), "Rate-limit the search endpoint per API key", "opened by mkhan",
		"mk/search-ratelimit → main", "+214 −38", "Asked: you", "Reviewed: jsato approved"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tooltip lacks %q:\n%s", want, text)
		}
	}
	s.Golden("reviews-row-tooltip")
}

// A code host of a plugin's own says who's asked and its branches in the same way.
func TestReviewsRowTooltipFromAPluginsHost(t *testing.T) {
	s, _, _, _ := reviewScene(t)
	m, err := plugin.ParseManifest([]byte("id = \"acme/gitlab\"\nversion = \"0.1.0\"\ndescription = \"A GitLab-like source\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	g := plugin.NewGrants()
	g.Grant(m.ID, m.Permissions)
	vm := s.PluginVM(plugin.Caller{ID: m.ID, Dir: t.TempDir(), Guard: g})
	if err := vm.Load(script.Chunk{Name: "init.lua", Source: []byte(gitlabLua)}); err != nil {
		t.Fatal(err)
	}
	s.Frame()
	s.Run("reviews.open", nil)
	s.Until("GitLab's", func() bool { return strings.Contains(s.Text(), "Drop the legacy v1 webhooks") })
	text := hoverTip(s, "Drop the legacy")
	for _, want := range []string{"acme/gateway#57", "opened by rlopez", "rl/drop-v1 → main", "Asked: you, @acme/platform", "Reviewed: ana changes requested"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tooltip lacks %q:\n%s", want, text)
		}
	}
}

// The list's right edge is dragged wider to read long titles whole, and no narrower than its rows need.
func TestReviewsListIsResized(t *testing.T) {
	s, _, hub, _ := reviewScene(t)
	long := "Rework the rate limiter so that per-key buckets refill continuously instead of once a minute, with burst"
	hub.AddPR("acme/api", gh.PR{Title: long, Author: "mkhan", Reviewers: []string{"you"}})
	s.Run("reviews.open", nil)
	s.Run("reviews.refresh", nil)
	s.Until("the long one", func() bool { return strings.Contains(s.Text(), "Rework the rate limiter") })
	if strings.Contains(s.Text(), long) {
		t.Fatalf("the long title fits before the list is widened:\n%s", s.Text())
	}
	edge := s.Find("Resize the list")
	if edge == nil {
		t.Fatal("no edge to resize the list by")
	}
	from := apptest.Centre(edge.Bounds)
	s.Drag(from, render.Point{X: from.X + 500, Y: from.Y})
	s.Quiet()
	if !strings.Contains(s.Text(), long) {
		t.Errorf("widened, the list still cuts the long title:\n%s", s.Text())
	}
	s.Golden("reviews-list-widened")
	from = apptest.Centre(s.Find("Resize the list").Bounds)
	s.Drag(from, render.Point{X: 0, Y: from.Y})
	s.Quiet()
	if x := s.Find("Resize the list").Bounds.Min.X; x < 300 {
		t.Errorf("dragged to the window's edge, the list is %v wide: narrower than its rows need", x)
	}
}
