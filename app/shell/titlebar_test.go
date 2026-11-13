package shell_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The 01-ide sessions, and one of each other status.
var tabFixture = []core.SessionOpened{
	{ID: "s1", Name: "pricing-rounding", Kind: core.KindPlan, Status: core.StatusWorking},
	{ID: "s2", Name: "ENG-2231 search api", Kind: core.KindWork, Status: core.StatusNeedsYou},
	{ID: "s3", Name: "review #4410", Kind: core.KindReview, Status: core.StatusPermission},
	{ID: "s4", Name: "flaky e2e sweep", Kind: core.KindJob, Status: core.StatusCI},
	{ID: "s5", Name: "dash-filters", Kind: core.KindWork, Status: core.StatusReviewer},
	{ID: "s6", Name: "auth flow", Kind: core.KindPlan, Status: core.StatusQuiet},
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
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	for _, ev := range tabFixture {
		s.Store().Apply(ev)
	}
	s.Frame()
	for _, ev := range tabFixture {
		if strings.Contains(s.Text(), ev.Name) {
			t.Errorf("%s has a tab before it was opened", ev.Name)
		}
	}
	s.Store().Apply(core.SessionFocused{ID: "s3"})
	s.Store().Apply(core.SessionFocused{ID: "s1"})
	s.Frame()
	text := s.Text()
	if !strings.Contains(text, "review #4410") || !strings.Contains(text, "pricing-rounding") || strings.Contains(text, "ENG-2231 search api") {
		t.Errorf("the tabs aren't the two opened: %q", text)
	}
	if err := s.Host().Run(context.Background(), "session.focus", registry.Args{"n": 2}); err != nil {
		t.Fatal(err)
	}
	if f := s.Store().State().Focus; f != "s1" {
		t.Errorf("⌘2 focused %s, want the second tab opened, s1", f)
	}
}

// A tab's ✕ closes the tab, not the session, and the front passes to its neighbour; the last one leaves the start view.
func TestSessionTabCloses(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture[:3])
	s.Frame()
	tab := s.Within("Sessions", "pricing-rounding")
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(tab.Bounds)})
	s.Frame()
	s.Click(apptest.Centre(s.Within(tab.Name, "Close pricing-rounding").Bounds), 0)
	s.Frame()
	st := s.Store().State()
	if _, ok := st.Session("s1"); !ok {
		t.Fatal("closing the tab threw the session away")
	}
	if slices.Contains(st.Tabs, "s1") || st.Focus != "s2" {
		t.Errorf("tabs %v, focus %s; want s1's tab gone and s2 in front", st.Tabs, st.Focus)
	}
	for _, id := range []string{"s2", "s3"} {
		if err := s.Host().Run(context.Background(), "session.close-tab", registry.Args{"id": id}); err != nil {
			t.Fatal(err)
		}
	}
	if st := s.Store().State(); len(st.Tabs) != 0 || st.Focus != "" {
		t.Errorf("tabs %v, focus %s after closing them all", st.Tabs, st.Focus)
	}
	if dir, asked := shell.Reopening(s.Headless); !asked {
		t.Error("the last tab closed didn't go back to the start view")
	} else if dir != "" {
		t.Errorf("the last tab closed opened %q, not the start view", dir)
	}
}

// A tab has room for its ✕ before the pointer comes: under the pointer, the ✕ shows and no tab changes width or
// moves.
func TestASessionTabsCloseShowsWithoutResizingIt(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture[:3])
	s.Frame()
	names := []string{"pricing-rounding", "ENG-2231 search api", "review #4410"}
	bounds := func() []render.Rect {
		var out []render.Rect
		for _, n := range names {
			out = append(out, s.Within("Sessions", n).Bounds)
		}
		return out
	}
	before := bounds()
	if s.Find("Close review #4410") != nil {
		t.Fatal("a tab away from the pointer shows its ✕")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(before[2])})
	s.Frame()
	s.Frame()
	if s.Find("Close review #4410") == nil {
		t.Fatal("the tab under the pointer doesn't show its ✕")
	}
	for i, r := range bounds() {
		if r != before[i] {
			t.Errorf("%s moved from %v to %v as the ✕ showed", names[i], before[i], r)
		}
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

// The 7 statuses render correctly from a fixture core.State.
func TestSessionTabsSnapshot(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture)
	s.Store().Apply(core.Notified{Notice: core.Notice{ID: "lsp.crashed", Severity: core.SevError, Session: "s4", Text: "gopls crashed"}})
	s.Golden("session-tabs")
	text := s.Text()
	for _, want := range []string{"ORIEL", "pricing-rounding", "plan", "needs you", "permission", "ci", "in review", "auth flow", "off", "✕1", "⌘7"} {
		if !strings.Contains(text, want) {
			t.Errorf("the tabs lack %q", want)
		}
	}
	// Each tab's mark is its status, by colour and by shape: drawn just before the tab's name, a spinner while it works,
	// else its glyph ("!" needs you, never a failure's ✗).
	th := s.UI().Theme()
	glyphs := map[core.Status]string{core.StatusNeedsYou: "!", core.StatusPermission: "?", core.StatusCI: "◌", core.StatusReviewer: "…",
		core.StatusQuiet: "✓", core.StatusOff: "○"}
	for _, ev := range tabFixture {
		want := th.C(kit.StatusTokens[ev.Status])
		if !hasMarkBefore(s.List(), ev.Name, glyphs[ev.Status], want) {
			t.Errorf("%s (%s): no %q mark in %s", ev.Name, ev.Status, glyphs[ev.Status], theme.Hex(want))
		}
	}
	if c, _ := glyphColour(s.List(), "pricing-rounding"); c != th.C(shell.TabNameOn()) {
		t.Errorf("the focused tab's name is %s", theme.Hex(c))
	}
}

// hasDotBefore reports whether a rect filled with col is drawn before the text name, with no other text between.
// hasMarkBefore says the frame draws a status mark in col just before name: glyph in col, or with no glyph a
// spinner stroked in col.
func hasMarkBefore(l *render.List, name, glyph string, col render.Color) bool {
	seen := false
	for _, op := range l.Ops {
		switch {
		case op.Kind == render.OpGlyphs && op.Run.Text == name:
			return seen
		case op.Kind == render.OpGlyphs:
			seen = glyph != "" && op.Run.Text == glyph && op.Color == col
		case glyph == "" && op.Path != nil && op.Style.Stroke == col:
			seen = true
		}
	}
	return false
}

// 01-ide's titlebar, from the logo to the end of its sixth tab, against the mockup. Each tab lands within a few
// pixels of the mockup's; the rest is text measured by our shaper rather than Chrome's, which the pixel count
// magnifies. The bound is just over today's 0.0344 and 8.95%.
func TestSessionTabsAgainstTheMockup(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture[:6])
	s.Frame()
	s.Frame()
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	mock, err := snapshot.ReadPNG("../../testdata/mockups/01-ide.png")
	if err != nil {
		t.Fatal(err)
	}
	r := image.Rect(0, 0, 1180, kit.TitleH-1)
	want, got := image.NewRGBA(r), image.NewRGBA(r)
	draw.Draw(want, r, mock, image.Point{}, draw.Src)
	draw.Draw(got, r, img, image.Point{}, draw.Src)
	d, side := snapshot.Compare(want, got, 0.1)
	t.Logf("titlebar against 01-ide: %s", d)
	if d.Mean > 0.036 || d.Off > 0.092 {
		os.WriteFile("testdata/session-tabs-mockup.failed.png", encodePNG(side), 0o644)
		t.Errorf("the titlebar is %s off the mockup (testdata/session-tabs-mockup.failed.png)", d)
	}
}

// More sessions than fit: the strip scrolls sideways, keeps the one in front in view, and leaves the title bar's
// right side where it is.
func TestSessionTabsOverflow(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 900, Y: 120}})
	for i := range 12 {
		id := core.SessionID(fmt.Sprintf("s%02d", i))
		if err := s.Store().Apply(core.SessionOpened{ID: id, Name: fmt.Sprintf("job: sweep stale deps %d", i), Kind: core.KindJob, Status: core.StatusQuiet}); err != nil {
			t.Fatal(err)
		}
		s.Store().Apply(core.SessionFocused{ID: id})
	}
	s.Registry().View(kit.TitleRight+".99-probe", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.TextNode("RIGHT-SIDE") }))
	s.Frame()
	s.Frame()
	s.Frame()
	var last, right render.Op
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == "job: sweep stale deps 11" {
			last = op
		}
		if op.Kind == render.OpGlyphs && op.Run.Text == "RIGHT-SIDE" {
			right = op
		}
	}
	if right.Run == nil || right.At.X+right.Run.Advance > s.Size().X {
		t.Fatalf("the title bar's right side is pushed off: %+v", right.At)
	}
	if last.Run == nil || last.At.X+last.Run.Advance > right.At.X {
		t.Errorf("the session in front isn't in view: at %v, the right side at %v", last.At, right.At)
	}
}

// A new session is a click away with sessions open: the + after the tabs opens the launcher.
func TestNewSessionFromTheTabs(t *testing.T) {
	s, _ := apptest.HostScene(t)
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "s1", Name: "pricing-rounding", Kind: core.KindWork, Status: core.StatusQuiet}})
	s.Store().Apply(core.SessionFocused{ID: "s1"})
	s.Until("the tab's status", func() bool { return strings.Contains(s.Text(), "pricing-rounding\noff") })
	s.Frame()
	b := s.NodeRole(render.RoleButton, "New session")
	if tab := s.Node("pricing-rounding").Bounds; b.Bounds.Min.X < tab.Max.X {
		t.Errorf("the + is at %v, not after the tab at %v", b.Bounds, tab)
	}
	s.Click(apptest.Centre(b.Bounds), 0)
	if s.Find("New session") == nil {
		t.Error("the + didn't open the launcher")
	}
}

func encodePNG(img image.Image) []byte {
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

// A session's tab dragged onto another takes its place, the others moving up; ⌘n follows the new order, and a
// click still brings a tab to the front.
func TestSessionTabsReorderByDragging(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture[:3])
	s.Frame()
	tab := func(name string) render.Point { return apptest.Centre(s.Within("Sessions", name).Bounds) }
	s.Drag(tab("review #4410"), tab("pricing-rounding"))
	if st := s.Store().State(); !slices.Equal(st.Tabs, []core.SessionID{"s3", "s1", "s2"}) {
		t.Fatalf("the tabs after review #4410 was dragged onto the first: %v", st.Tabs)
	}
	if st := s.Store().State(); st.Focus != "s1" {
		t.Errorf("the drag brought %s to the front", st.Focus)
	}
	s.Drag(tab("review #4410"), tab("ENG-2231 search api"))
	if st := s.Store().State(); !slices.Equal(st.Tabs, []core.SessionID{"s1", "s2", "s3"}) {
		t.Fatalf("the tabs after it was dragged back onto the last: %v", st.Tabs)
	}
	if err := s.Host().Run(context.Background(), "session.focus", registry.Args{"n": 3}); err != nil || s.Store().State().Focus != "s3" {
		t.Errorf("⌘3 focused %s (%v), want review #4410 in its new place", s.Store().State().Focus, err)
	}
	s.Click(tab("pricing-rounding"), 0)
	s.Frame()
	if f := s.Store().State().Focus; f != "s1" {
		t.Errorf("a click on a tab focused %s", f)
	}
}

// workspaceWithTabs is the window on a project with round.go open in the editor and the first n fixture sessions'
// tabs, the first in front.
func workspaceWithTabs(t *testing.T, n int) *apptest.Scene {
	dir := t.TempDir()
	p := filepath.Join(dir, "internal", "pricing", "round.go")
	os.MkdirAll(filepath.Dir(p), 0o755)
	os.WriteFile(p, []byte(rig.RoundGo()), 0o644)
	s := apptest.New(t, apptest.Options{WorkDir: dir})
	openTabFixture(t, s.Store(), tabFixture[:n])
	s.Run("editor.open", registry.Args{"path": "internal/pricing/round.go"})
	s.Quiet()
	s.Frame()
	s.Frame()
	return s
}

// The session in front is the window under the tabs: its tab is the surface under the title bar, running into it
// with no edge between and flaring out at its foot, on a band a step deeper. The tabs behind have no fill of their own
// until the pointer is on one, so only the one in front stands out, and never as the file tabs below do.
func TestTheTabInFrontJoinsTheWindowUnderIt(t *testing.T) {
	s := workspaceWithTabs(t, 3)
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	at := func(x, y float32) render.Color {
		c := img.At(int(x), int(y)).(color.RGBA)
		return render.Color{R: c.R, G: c.G, B: c.B, A: c.A}
	}
	front := s.Within("Sessions", "pricing-rounding").Bounds
	behind := s.Within("Sessions", "review #4410").Bounds
	x := front.Max.X - 4 // clear of the file tab in front under it, which has an edge of its own
	under := at(x, kit.TitleH+3)
	band := at(x, 2)
	if under == band {
		t.Fatalf("the band is the colour of the window under it, %s", theme.Hex(band))
	}
	for y := front.Min.Y + 2; y < kit.TitleH+4; y++ {
		if c := at(x, y); c != under {
			t.Fatalf("the front tab at y=%v is %s, not the window's %s: there's an edge between them", y, theme.Hex(c), theme.Hex(under))
		}
	}
	if c := at(front.Max.X+2, kit.TitleH-1); c != under {
		t.Errorf("the front tab's foot doesn't flare out into the window: %s beside it", theme.Hex(c))
	}
	if c := at(front.Min.X+1, front.Min.Y+1); c == under {
		t.Errorf("the front tab's top corner isn't rounded")
	}
	if c := at(behind.Min.X+4, behind.Max.Y-2); c != band {
		t.Errorf("a tab behind is %s, not the band's %s", theme.Hex(c), theme.Hex(band))
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(behind)})
	s.Frame()
	s.Frame()
	img = raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	if c := at(behind.Min.X+4, behind.Max.Y-2); c == band {
		t.Errorf("the tab under the pointer doesn't lighten")
	}
}

// A tab is two lines: its name, and under it what it's doing, the asks waiting in it and its key.
func TestASessionTabIsItsNameOverItsState(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1600, Y: 120}})
	openTabFixture(t, s.Store(), tabFixture[:3])
	s.Frame()
	s.Frame()
	for _, tt := range []struct{ name, state string }{{"pricing-rounding", "plan"}, {"ENG-2231 search api", "needs you"}, {"review #4410", "permission"}} {
		tab := s.Within("Sessions", tt.name).Bounds
		var name, state, key render.Op
		for _, op := range s.List().Ops {
			if op.Kind != render.OpGlyphs || op.At.X < tab.Min.X || op.At.X > tab.Max.X {
				continue
			}
			switch op.Run.Text {
			case tt.name:
				name = op
			case tt.state:
				state = op
			case "⌘1", "⌘2", "⌘3":
				key = op
			}
		}
		if name.Run == nil || state.Run == nil || key.Run == nil {
			t.Fatalf("%s's tab lacks its name, %q or its key", tt.name, tt.state)
		}
		if state.At.Y <= name.At.Y+8 || math.Abs(float64(key.At.Y-state.At.Y)) > 1 {
			t.Errorf("%s: the name at y=%v, %q at %v and the key at %v; want the state and key on a line under the name",
				tt.name, name.At.Y, tt.state, state.At.Y, key.At.Y)
		}
	}
}

// The title bar's band stands apart from the window under it in every built-in theme, dark and light, so the tabs
// behind read as tabs on a band and the one in front as cut from the window: a step a person sees, by contrast ratio
// (lightness alone misleads near black, where a large step in L is a small one on screen).
func TestTheTitleBarStandsApartFromTheWindow(t *testing.T) {
	const min = 1.1
	for _, src := range theme.Builtins() {
		st, err := theme.Stack(theme.Builtins(), src.Name())
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range []theme.Variant{theme.Dark, theme.Light} {
			r, err := theme.Resolve(st, v)
			if err != nil {
				continue // a theme without that variant
			}
			band, _ := r.Color("titlebar.bg")
			surface, _ := r.Color("bg.surface")
			if got := theme.ContrastRatio(band, surface); got < min {
				t.Errorf("%s %s: the band %s on the window %s is %.3f:1, under %.2f:1", src.Name(), v, theme.Hex(band), theme.Hex(surface), got, min)
			}
		}
	}
}
