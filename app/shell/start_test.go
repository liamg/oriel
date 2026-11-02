package shell_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/app/shell"
	"github.com/liamg/oriel/registry/uireg"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
)

// With nothing open the window shows the start view: the actions with their keys, and the logo for recents.
func TestStartViewAtLaunch(t *testing.T) {
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir()})
	r.WaitFrame("the start view", func(s string) bool {
		return apptest.HasAll(s, "New Session", "Resume a Session", "Open Directory", "Settings", "⌘N", "⌘O", "⇧⌘O", "⌘,")
	})
}

// An entry runs its command on Enter; one whose command isn't registered doesn't, and a plugin adds its own.
func TestStartViewEntries(t *testing.T) {
	ran := make(chan string, 4)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Setup: func(h plugin.Host) {
		h.Command(registry.Command{ID: "test.hello", Run: func(registry.Ctx, registry.Args) error { ran <- "hello"; return nil }})
		h.Contribute(uireg.StartEntry{Name: "hello", Title: "Say hello", Desc: "A plugin's action", Command: "test.hello", Order: 5})
	}})
	r.WaitText("Say hello")
	r.TypeKeys("<CR>") // the first entry is selected
	select {
	case got := <-ran:
		if got != "hello" {
			t.Fatalf("ran %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Enter didn't run the selected entry")
	}
	r.TypeKeys("j<CR>") // New Session: no session host, so it does nothing
	r.TypeKeys("jjj<CR>")
	r.WaitText("SETTINGS") // the settings page's heading
}

// Open Directory on a path reopens the window on it, and remembers it for the next launch's recents.
func TestOpenDirectory(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644)
	r := apptest.NewWindow(t, apptest.WindowOptions{State: state})
	r.WaitText("Open Directory")
	if err := r.Do(func() error {
		return r.Host.Run(context.Background(), "directory.open", registry.Args{"path": dir})
	}); err != nil {
		t.Fatal(err)
	}
	r.WaitFrame("the window on proj", func(s string) bool {
		return !strings.Contains(s, "Open Directory") && !strings.Contains(s, "Opening proj")
	})
	r.TypeKeys("<Space><Space>")
	r.WaitText("hello.txt")

	r2 := apptest.NewWindow(t, apptest.WindowOptions{State: state})
	r2.WaitFrame("proj in the recents", func(s string) bool {
		return apptest.HasAll(s, "proj", "DIRECTORIES")
	})
}

// Home has no session layout, so a docked view opened there leaves the start view in place, with no rail.
func TestStartViewStaysOnHome(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Frame()
	s.Run("view.open", registry.Args{"id": "problems"})
	s.Frame()
	s.Frame()
	if !(s.Find("Start") != nil) || (s.Find("Views") != nil) {
		t.Errorf("a docked view opened on Home changed it:\n%s", s.Text())
	}
}

// The start view against 39-start and 39b-start-empty, in both variants.
func TestStartViewSnapshots(t *testing.T) {
	for _, tt := range []struct {
		name    string
		recents bool
		variant theme.Variant
		mockup  string
	}{
		{"start-empty", false, theme.Dark, "39b-start-empty"},
		{"start-recents", true, theme.Dark, "39-start"},
		{"start-light", true, theme.Light, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s := apptest.New(t, apptest.Options{})
			shell.KeepRecents(s.Headless, filepath.Join(t.TempDir(), "recent.json"))
			if tt.recents {
				addRecents(s)
			}
			st, _ := theme.Stack(theme.Builtins(), "oriel")
			r, err := theme.Resolve(st, tt.variant)
			if err != nil {
				t.Fatal(err)
			}
			s.UI().SetTheme(r)
			s.Frame()
			s.Frame()
			s.Golden(tt.name)
			if tt.mockup == "" {
				return
			}
			img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
			mock, err := snapshot.ReadPNG("../../testdata/mockups/" + tt.mockup + ".png")
			if err != nil {
				t.Fatal(err)
			}
			d, _ := snapshot.Compare(mock, img, 0.1)
			t.Logf("%s against %s: %s", tt.name, tt.mockup, d)
			if d.Mean > 0.05 {
				t.Errorf("%s is %s off %s", tt.name, d, tt.mockup)
			}
		})
	}
}

// A typed path lists itself and the directories in it that start with what's typed after its last slash.
func TestTypedDirs(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{"src/oriel", "src/other", "src/.hidden", "docs"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	for _, tt := range []struct{ q, want string }{
		{"~/src/", "~/src ~/src/oriel ~/src/other"},
		{"~/src/or", "~/src/oriel"},
		{"~/do", "~/docs"},
	} {
		var got []string
		for _, p := range kit.TypedDirs(tt.q, home) {
			got = append(got, "~"+strings.TrimPrefix(p, home))
		}
		if strings.Join(got, " ") != tt.want {
			t.Errorf("kit.TypedDirs(%q) = %q, want %q", tt.q, got, tt.want)
		}
	}
}

// addRecents puts 39-start's three directories in s's recents.
func addRecents(s *apptest.Scene) {
	home, _ := os.UserHomeDir()
	for _, d := range []struct {
		name string
		ago  time.Duration
	}{{"notes", 7 * 24 * time.Hour}, {"dotfiles", 3 * 24 * time.Hour}, {"oriel", 10 * time.Minute}} {
		p := filepath.Join(home, "src", d.name)
		os.MkdirAll(p, 0o755)
		if d.name != "notes" {
			os.MkdirAll(filepath.Join(p, ".git"), 0o755)
			os.WriteFile(filepath.Join(p, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644)
		}
		shell.AddRecent(s.Headless, p, time.Now().Add(-d.ago))
	}
	s.SettleReads()
}

// The start view fits the window: the columns shrink, and when both won't fit, Start and Recent become tabs.
func TestStartViewFits(t *testing.T) {
	for _, tt := range []struct {
		width float32
		tabs  bool
	}{{1400, false}, {1000, false}, {640, true}, {420, true}} {
		s := apptest.New(t, apptest.Options{Size: render.Point{X: tt.width, Y: 700}})
		shell.KeepRecents(s.Headless, filepath.Join(t.TempDir(), "recent.json"))
		addRecents(s)
		s.Frame()
		s.Frame()
		s.Frame()
		for _, l := range []string{"Open Directory", "Settings"} {
			if b := s.StartCard(l).Bounds; b.Min.X < 0 || b.Max.X > tt.width {
				t.Errorf("at %v wide, %s is at %v", tt.width, l, b)
			}
		}
		text := s.Text()
		if got := strings.Contains(text, "notes"); got == tt.tabs {
			t.Errorf("at %v wide, the recents show: %v\n%s", tt.width, got, text)
		}
		if !tt.tabs {
			continue
		}
		// j moves through the cards and the links to the first recent.
		for range 20 {
			s.Run("start.next", nil)
			s.Frame()
			s.Frame()
			if text = s.Text(); strings.Contains(text, "notes") {
				break
			}
		}
		if !strings.Contains(text, "notes") || strings.Contains(text, "Open Directory") {
			t.Errorf("at %v wide, selecting a recent didn't switch to its tab:\n%s", tt.width, text)
		}
		if b := s.StartCard("oriel").Bounds; b.Max.X > tt.width {
			t.Errorf("at %v wide, the recent oriel is at %v", tt.width, b)
		}
		s.Click(apptest.Centre(s.Node("START").Bounds), 0)
		if text := s.Text(); !strings.Contains(text, "Open Directory") {
			t.Errorf("at %v wide, clicking the Start tab didn't show the actions:\n%s", tt.width, text)
		}
	}
}

// The pointer moving onto a card selects it, as the keys do; the keys still move off it.
func TestStartViewHover(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	s.Frame()
	s.Frame()
	if !s.StartCard("Open Directory").Selected {
		t.Fatal("Open Directory isn't selected at first")
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.StartCard("Settings").Bounds)})
	s.Frame()
	s.Frame()
	if !s.StartCard("Settings").Selected || s.StartCard("Open Directory").Selected {
		t.Fatal("hovering Settings didn't select it")
	}
	s.Run("start.prev", nil)
	s.Frame()
	s.Frame()
	if !s.StartCard("Open Directory").Selected || s.StartCard("Settings").Selected {
		t.Error("k didn't move the selection off the hovered card")
	}
}

// A link is a row like the cards: the pointer on it selects it and nothing else, and the keys reach it.
func TestStartViewHoverLink(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	for i, title := range []string{"Plugin page", "Another page"} {
		s.Host().Contribute(uireg.StartEntry{Name: fmt.Sprintf("p%d", i), Icon: "jobs", Title: title, Command: "settings.open", Order: 200 + i, Link: true})
	}
	s.Frame()
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.StartCard("Another page").Bounds)})
	s.Frame()
	s.Frame()
	if !s.StartCard("Another page").Selected {
		t.Error("hovering a link didn't select it")
	}
	for _, other := range []string{"Open Directory", "Settings", "Plugin page"} {
		if s.StartCard(other).Selected {
			t.Errorf("%s stays selected with the pointer on a link", other)
		}
	}
	s.Run("start.prev", nil)
	s.Frame()
	s.Frame()
	if !s.StartCard("Plugin page").Selected || s.StartCard("Another page").Selected {
		t.Error("k didn't move from one link to the one before")
	}
}

// The window opens where it was when it last closed: its frame is kept in the state directory.
func TestWindowPlaceKept(t *testing.T) {
	state := t.TempDir()
	r := apptest.NewWindow(t, apptest.WindowOptions{State: state})
	if f := r.Win.Opts().Frame; f != (render.Rect{}) {
		t.Fatalf("the first window asked for %v", f)
	}
	r.WaitText("Open Directory")
	moved := render.XYWH(200, 150, 1100, 700)
	r.Win.Move(moved)
	r.Stop()

	r2 := apptest.NewWindow(t, apptest.WindowOptions{State: state})
	if f := r2.Win.Opts().Frame; f != moved {
		t.Errorf("the next window asked for %v, want %v", f, moved)
	}
}

// A ✕ shows on a recent under the pointer; clicking it forgets the directory, for the next launch too.
func TestStartViewForgetOnHover(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	shell.KeepRecents(s.Headless, filepath.Join(t.TempDir(), "recent.json"))
	addRecents(s)
	s.Frame()
	s.Frame()
	forgets := func() (n int) {
		var walk func(a *render.AccessNode)
		walk = func(a *render.AccessNode) {
			if strings.HasPrefix(a.Name, "Forget ") {
				n++
			}
			for _, k := range a.Children {
				walk(k)
			}
		}
		walk(s.UI().AccessTree())
		return n
	}
	if n := forgets(); n != 0 {
		t.Fatalf("%d ✕ show with the pointer away", n)
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: apptest.Centre(s.StartCard("dotfiles").Bounds)})
	s.Frame()
	s.Frame()
	if n := forgets(); n != 1 {
		t.Fatalf("%d ✕ show with the pointer on dotfiles, want 1", n)
	}
	x := s.Node("Forget dotfiles")
	s.Click(apptest.Centre(x.Bounds), 0)
	for _, d := range shell.Recents(s.Headless) {
		if filepath.Base(d) == "dotfiles" {
			t.Fatal("dotfiles is still in the recents")
		}
	}
	if s.UI().NextFrame().IsZero() {
		t.Error("the click asked for no frame to show it")
	}
	s.Frame()
	if strings.Contains(s.Text(), "dotfiles") {
		t.Error("dotfiles still shows")
	}
}

// The start view keeps to its own keys. Its entries' and links' keys work (⌘, Settings); the rest don't
// (a global key such as ⌘K, an editor key such as ␣␣), as they've nothing to act on yet. With a directory open they
// all work again.
func TestStartViewKeepsToItsOwnKeys(t *testing.T) {
	ran := make(chan string, 4)
	setup := func(h plugin.Host) {
		h.Command(registry.Command{ID: "test.global", Run: func(registry.Ctx, registry.Args) error { ran <- "global"; return nil }})
		h.Command(registry.Command{ID: "test.normal", Run: func(registry.Ctx, registry.Args) error { ran <- "normal"; return nil }})
		h.Keymap(registry.Keymap{Mode: "global", Keys: "<D-y>", Command: "test.global"})
		h.Keymap(registry.Keymap{Mode: "n", Keys: "<Space>z", Command: "test.normal"})
	}
	r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Setup: setup})
	r.WaitText("Open Directory")
	r.TypeKeys("<D-y>")
	r.TypeKeys("<Space>z")
	r.TypeKeys("<D-,>")
	r.WaitText("SETTINGS")
	select {
	case k := <-ran:
		t.Errorf("a %s key ran on the start view", k)
	default:
	}

	r2 := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), WorkDir: t.TempDir(), Setup: setup})
	r2.TypeKeys("<D-y>")
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("a global key didn't run with a directory open")
	}
}

// However many links plugins add under the actions, every one shows whole: the strip wraps onto more
// lines inside the Start column rather than running past it.
func TestStartLinksWrap(t *testing.T) {
	for _, width := range []float32{1400, 1000, 640} {
		s := apptest.New(t, apptest.Options{Size: render.Point{X: width, Y: 900}})
		shell.KeepRecents(s.Headless, filepath.Join(t.TempDir(), "recent.json"))
		addRecents(s)
		var titles []string
		for i := range 8 {
			title := fmt.Sprintf("Plugin page %d", i)
			titles = append(titles, title)
			s.Host().Contribute(uireg.StartEntry{Name: fmt.Sprintf("p%d", i), Icon: "jobs", Title: title, Command: "settings.open", Order: 200 + i,
				Link: true, Note: func() string { return "12 waiting" }})
		}
		s.Frame()
		s.Frame()
		s.Frame()
		col := s.StartCard("Settings").Bounds
		rows := map[float32]bool{}
		for _, title := range titles {
			b := s.Node(title).Bounds
			if b.Min.X < col.Min.X-1 || b.Max.X > col.Max.X+1 {
				t.Errorf("at %v wide, %q is at %v, outside the column %v", width, title, b, col)
			}
			rows[b.Min.Y] = true
		}
		if len(rows) < 2 {
			t.Errorf("at %v wide, eight links sit on %d line", width, len(rows))
		}
	}
}

// The start view shows only while nothing runs, so it has no link to mission control; ⌘-key and the omnibox reach it.
func TestStartHasNoMissionControlLink(t *testing.T) {
	s := apptest.New(t, apptest.Options{Size: render.Point{X: 1400, Y: 900}})
	s.Store().Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Status: core.StatusWorking,
		Agent: core.AgentState{Running: true}}})
	s.Frame()
	s.Frame()
	if strings.Contains(s.Text(), "MISSION CONTROL") {
		s.Run("mission.toggle", nil)
		s.Frame()
	}
	if strings.Contains(s.Text(), "Mission control") {
		t.Errorf("the start view links mission control:\n%s", s.Text())
	}
}

// Open Directory's picker walks home off the loop, busy until the walk lands, then lists what's under home with
// its git repos marked.
func TestDirectoriesWalkHomeOffTheLoop(t *testing.T) {
	s := apptest.New(t, apptest.Options{})
	home, _ := os.UserHomeDir()
	for _, d := range []string{"src/oriel/.git", "src/notes"} {
		os.MkdirAll(filepath.Join(home, d), 0o755)
	}
	src, walks := shell.DirectoriesSource(s.Headless)
	defer walks.Dispose()
	var items []uireg.PickerItem
	s.Until("the walk", func() bool {
		items = src.Items(context.Background(), "")
		return !src.Loading()
	})
	items = src.Items(context.Background(), "")
	notes := map[string]string{}
	for _, it := range items {
		notes[it.Label] = it.Note
	}
	if n, ok := notes["~/src/oriel"]; !ok || n != "git" {
		t.Errorf("~/src/oriel: %q, %v; want marked git", n, ok)
	}
	if n, ok := notes["~/src/notes"]; !ok || n != "" {
		t.Errorf("~/src/notes: %q, %v; want listed, unmarked", n, ok)
	}
}

// The window opens on the chosen theme's background, so it isn't white until the first frame.
func TestWindowOpensOnTheThemesBackground(t *testing.T) {
	for _, v := range []theme.Variant{theme.Dark, theme.Light} {
		r := apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Setup: func(h plugin.Host) {
			h.Options().Set("ui.variant", registry.LayerUser, registry.At{}, string(v))
		}})
		st, err := theme.Stack(theme.Builtins(), "oriel")
		if err != nil {
			t.Fatal(err)
		}
		want, err := theme.Resolve(st, theme.Effective(st, v))
		if err != nil {
			t.Fatal(err)
		}
		if got := r.Win.Opts().Background; got != want.C(theme.BgBase) || got.A == 0 {
			t.Errorf("%s: the window opened on %v, want %v", v, got, want.C(theme.BgBase))
		}
		r.Stop()
	}
}

// Config.Started runs once the window is open and every plugin is active: the updater's health check after a
// swap waits for it.
func TestStartedRunsOnceTheWindowIsUp(t *testing.T) {
	started := make(chan struct{}, 2)
	apptest.NewWindow(t, apptest.WindowOptions{State: t.TempDir(), Config: func(cfg *apptest.Config) { cfg.Started = func() { started <- struct{}{} } }})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("Started wasn't called")
	}
	select {
	case <-started:
		t.Fatal("Started was called twice")
	case <-time.After(100 * time.Millisecond):
	}
}
