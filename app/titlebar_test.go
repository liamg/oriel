package app

import (
	"context"
	"fmt"
	"image"
	"image/draw"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The 01-ide sessions, and one of each other status.
var tabFixture = []core.SessionOpened{
	{ID: "s1", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusWorking},
	{ID: "s2", Name: "ENG-2231 usage api", Kind: core.KindWork, Status: core.StatusNeedsYou},
	{ID: "s3", Name: "review #4410", Kind: core.KindReview, Status: core.StatusPermission},
	{ID: "s4", Name: "flaky e2e sweep", Kind: core.KindJob, Status: core.StatusCI},
	{ID: "s5", Name: "dash-filters", Kind: core.KindWork, Status: core.StatusReviewer},
	{ID: "s6", Name: "tour: auth flow", Kind: core.KindTour, Status: core.StatusQuiet},
	{ID: "s7", Name: "old spike", Kind: core.KindWork, Status: core.StatusOff},
}

// openTabFixture adds the sessions and opens a tab for each, in order, with the first in front.
func openTabFixture(t *testing.T, store core.Store, evs []core.SessionOpened) {
	t.Helper()
	for _, ev := range evs {
		if err := store.Apply(ev); err != nil {
			t.Fatal(err)
		}
		if err := store.Apply(core.SessionFocused{ID: ev.ID}); err != nil {
			t.Fatal(err)
		}
	}
	store.Apply(core.SessionFocused{ID: evs[0].ID})
}

// A session has a tab once it's started or resumed, not for being on the start view's list.
func TestSessionTabsAreTheOpenSessions(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1600, Y: 120})
	for _, ev := range tabFixture {
		s.store.Apply(ev)
	}
	s.frame()
	for _, ev := range tabFixture {
		if strings.Contains(s.frameText(), ev.Name) {
			t.Errorf("%s has a tab before it was opened", ev.Name)
		}
	}
	s.store.Apply(core.SessionFocused{ID: "s3"})
	s.store.Apply(core.SessionFocused{ID: "s1"})
	s.frame()
	text := s.frameText()
	if !strings.Contains(text, "review #4410") || !strings.Contains(text, "pricing-rounding") || strings.Contains(text, "ENG-2231 usage api") {
		t.Errorf("the tabs aren't the two opened: %q", text)
	}
	if err := s.host.Run(context.Background(), "session.focus", registry.Args{"n": 2}); err != nil {
		t.Fatal(err)
	}
	if f := s.store.State().Focus; f != "s1" {
		t.Errorf("⌘2 focused %s, want the second tab opened, s1", f)
	}
}

// A tab's ✕ closes the tab, not the session, and the front passes to its neighbour; the last one leaves the start view.
func TestSessionTabCloses(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1600, Y: 120})
	openTabFixture(t, s.store, tabFixture[:3])
	s.frame()
	tab := s.within("Sessions", "pricing-rounding")
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(tab.Bounds)})
	s.frame()
	s.click(centre(s.within(tab.Name, "Close pricing-rounding").Bounds))
	s.frame()
	st := s.store.State()
	if _, ok := st.Session("s1"); !ok {
		t.Fatal("closing the tab threw the session away")
	}
	if slices.Contains(st.Tabs, "s1") || st.Focus != "s2" {
		t.Errorf("tabs %v, focus %s; want s1's tab gone and s2 in front", st.Tabs, st.Focus)
	}
	for _, id := range []string{"s2", "s3"} {
		if err := s.host.Run(context.Background(), "session.close-tab", registry.Args{"id": id}); err != nil {
			t.Fatal(err)
		}
	}
	if st := s.store.State(); len(st.Tabs) != 0 || st.Focus != "" {
		t.Errorf("tabs %v, focus %s after closing them all", st.Tabs, st.Focus)
	}
	select {
	case to := <-s.sh.reopen:
		if to.dir != "" {
			t.Errorf("the last tab closed opened %q, not the start view", to.dir)
		}
	default:
		t.Error("the last tab closed didn't go back to the start view")
	}
}

// glyphColour is the colour the frame draws text s in.
func glyphColour(l *render.List, s string) (render.Color, bool) {
	for _, op := range l.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == s {
			return op.Color, true
		}
	}
	return render.Color{}, false
}

// SH-2: "7 statuses render correctly from a fixture core.State".
func TestSessionTabsSnapshot(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1600, Y: 120})
	openTabFixture(t, s.store, tabFixture)
	s.store.Apply(core.Notified{Notice: core.Notice{ID: "lsp.crashed", Severity: core.SevError, Session: "s4", Text: "gopls crashed"}})
	s.golden("session-tabs")
	text := s.frameText()
	for _, want := range []string{"ORIEL", "pricing-rounding", "plan", "needs you", "permission", "ci", "in review", "tour: auth flow", "off", "✕1", "⌘7"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tabs lack %q", want)
		}
	}
	// Each tab's dot is its status colour: find the dot by the box drawn just before the tab's name.
	th := s.ui.Theme()
	for _, ev := range tabFixture {
		want := th.C(statusTokens[ev.Status])
		if !hasDotBefore(s.list, ev.Name, want) {
			t.Errorf("%s (%s): no %s dot", ev.Name, ev.Status, theme.Hex(want))
		}
	}
	if c, _ := glyphColour(s.list, "pricing-rounding"); c != th.C(tTabNameOn) {
		t.Errorf("the focused tab's name is %s", theme.Hex(c))
	}
}

// hasDotBefore reports whether a rect filled with col is drawn before the text name, with no other text between.
func hasDotBefore(l *render.List, name string, col render.Color) bool {
	seen := false
	for _, op := range l.Ops {
		switch {
		case op.Kind == render.OpRect && op.Style.Fill == col:
			seen = true
		case op.Kind == render.OpGlyphs && op.Run.Text == name:
			return seen
		case op.Kind == render.OpGlyphs:
			seen = false
		}
	}
	return false
}

// SH-2: "keys switch in under one frame": the frame drawn after ⌘3 already shows the third session in front.
func TestSessionKeysSwitchInOneFrame(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	be := headless.New()
	store := eventstore.New()
	openTabFixture(t, store, tabFixture)
	done := make(chan error, 1)
	go func() { done <- Run(ctx, be, Config{Store: store, ConfigDir: t.TempDir(), WorkDir: t.TempDir()}) }()
	win := <-be.Windows()
	defer func() { win.Close(); <-done }()
	first, err := win.WaitFrame(ctx, func(f headless.Frame) bool { return strings.Contains(f.Text(), "review #4410") })
	if err != nil {
		t.Fatal(err)
	}
	front, _ := glyphColour(&first.List, "pricing-rounding")
	before := len(win.Frames())
	win.Send(render.KeyEvent{Key: "3", Mods: render.ModSuper})
	// A frame already being drawn when the key arrives can't show it; the one after it must.
	n := 0
	f, err := win.WaitFrame(ctx, func(f headless.Frame) bool {
		n++
		on, _ := glyphColour(&f.List, "review #4410")
		return n > before && on == front
	})
	if err != nil {
		t.Fatalf("no frame shows the third tab in front: %v", err)
	}
	if late := n - before; late > 2 {
		t.Errorf("the third tab came to the front %d frames after ⌘3; the one after the frame in flight should show it", late)
	}
	if off, _ := glyphColour(&f.List, "pricing-rounding"); off == front {
		t.Errorf("the first tab is still drawn in front")
	}
	if store.State().Focus != "s3" {
		t.Errorf("focus = %s", store.State().Focus)
	}
}

// 01-ide's titlebar, from the logo to the end of its sixth tab, against the mockup. Each tab lands within a few
// pixels of the mockup's; the rest is text measured by our shaper rather than Chrome's, which the pixel count
// magnifies. The bound is just over today's 0.040 and 12.1%.
func TestSessionTabsAgainstTheMockup(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1600, Y: 120})
	openTabFixture(t, s.store, tabFixture[:6])
	s.frame()
	s.frame()
	img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../_design/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	r := image.Rect(0, 0, 1180, titleH-1)
	want, got := image.NewRGBA(r), image.NewRGBA(r)
	draw.Draw(want, r, mock, image.Point{}, draw.Src)
	draw.Draw(got, r, img, image.Point{}, draw.Src)
	d, side := snapshot.Compare(want, got, 0.1)
	t.Logf("titlebar against 01-ide: %s", d)
	if d.Mean > 0.042 || d.Off > 0.125 {
		os.WriteFile("testdata/session-tabs-mockup.failed.png", encodePNG(side), 0o644)
		t.Errorf("the titlebar is %s off the mockup (testdata/session-tabs-mockup.failed.png)", d)
	}
}

// More sessions than fit: the strip scrolls sideways, keeps the one in front in view, and leaves the title bar's
// right side where it is.
func TestSessionTabsOverflow(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 900, Y: 120})
	for i := range 12 {
		id := core.SessionID(fmt.Sprintf("s%02d", i))
		if err := s.store.Apply(core.SessionOpened{ID: id, Name: fmt.Sprintf("job: sweep stale deps %d", i), Kind: core.KindJob, Status: core.StatusQuiet}); err != nil {
			t.Fatal(err)
		}
		s.store.Apply(core.SessionFocused{ID: id})
	}
	s.reg.View(TitleRight+".99-probe", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return ui.TextNode("RIGHT-SIDE") }))
	s.frame()
	s.frame()
	s.frame()
	var last, right render.Op
	for _, op := range s.list.Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "job: sweep stale deps 11" {
			last = op
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == "RIGHT-SIDE" {
			right = op
		}
	}
	if right.Run == nil || right.At.X+right.Run.Advance > s.size.X {
		t.Fatalf("the title bar's right side is pushed off: %+v", right.At)
	}
	if last.Run == nil || last.At.X+last.Run.Advance > right.At.X {
		t.Errorf("the session in front isn't in view: at %v, the right side at %v", last.At, right.At)
	}
}
