package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
)

// SH-15: plain oriel opens nothing, oriel <dir> opens dir, and oriel <file>… opens files.
func TestConfigFromFlagsDirectory(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	for _, tt := range []struct {
		args  []string
		dir   string
		files int
	}{
		{nil, "", 0},
		{[]string{dir}, dir, 0},
		{[]string{"."}, wd, 0},
		{[]string{"a.go", "b.go"}, "", 2},
	} {
		cfg, err := ConfigFromFlags(tt.args, "v1", io.Discard)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.WorkDir != tt.dir || len(cfg.Files) != tt.files {
			t.Errorf("%q: dir %q files %q, want %q and %d files", tt.args, cfg.WorkDir, cfg.Files, tt.dir, tt.files)
		}
	}
}

// startApp runs the app with nothing open, its state in state, and a grab host.
func startApp(t *testing.T, state, workDir string, setup func(h plugin.Host)) *running {
	t.Helper()
	return startAppWith(t, state, workDir, setup, nil)
}

// startAppWith is startApp with its config changed first: the session host, say.
func startAppWith(t *testing.T, state, workDir string, setup func(h plugin.Host), change func(*Config)) *running {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	be := headless.New()
	g := &grabHost{setup: setup}
	r := &running{t: t, store: eventstore.New(), dir: workDir, done: make(chan error, 1)}
	cfg := Config{Store: r.store, ConfigDir: t.TempDir(), StateDir: state, WorkDir: workDir, Plugins: []plugin.Plugin{g}, Net: offlineNet}
	if change != nil {
		change(&cfg)
		r.store = cfg.Store
	}
	go func() { r.done <- Run(ctx, be, cfg) }()
	select {
	case r.win = <-be.Windows():
	case err := <-r.done:
		t.Fatalf("app exited: %v", err)
	}
	r.host = g.h
	t.Cleanup(r.stop)
	return r
}

// SH-15: with nothing open the window shows the start view: the actions with their keys, and the logo for recents.
func TestStartViewAtLaunch(t *testing.T) {
	r := startApp(t, t.TempDir(), "", nil)
	r.waitFrame("the start view", func(s string) bool {
		return containsAll(s, "New Session", "Resume a Session", "Open Directory", "Settings", "⌘N", "⌘O", "⇧⌘O", "⌘,")
	})
}

// SH-15: an entry runs its command on Enter; one whose command isn't registered doesn't, and a plugin adds its own.
func TestStartViewEntries(t *testing.T) {
	ran := make(chan string, 4)
	r := startApp(t, t.TempDir(), "", func(h plugin.Host) {
		h.Command(registry.Command{ID: "test.hello", Run: func(registry.Ctx, registry.Args) error { ran <- "hello"; return nil }})
		h.Contribute(registry.StartEntry{Name: "hello", Title: "Say hello", Desc: "A plugin's action", Command: "test.hello", Order: 5})
	})
	r.waitText("Say hello")
	r.typeKeys("<CR>") // the first entry is selected
	select {
	case got := <-ran:
		if got != "hello" {
			t.Fatalf("ran %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Enter didn't run the selected entry")
	}
	r.typeKeys("j<CR>") // New Session: no session host, so it does nothing
	r.typeKeys("jjj<CR>")
	r.waitText("SETTINGS") // the settings page's heading
}

// SH-15: Open Directory on a path reopens the window on it, and remembers it for the next launch's recents.
func TestOpenDirectory(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "hello.txt"), []byte("hi\n"), 0o644)
	r := startApp(t, state, "", nil)
	r.waitText("Open Directory")
	if err := r.do(func() error {
		return r.host.Run(context.Background(), "directory.open", registry.Args{"path": dir})
	}); err != nil {
		t.Fatal(err)
	}
	r.waitFrame("the start view gone", func(s string) bool { return !strings.Contains(s, "Open Directory") })
	r.typeKeys("<Space><Space>")
	r.waitText("hello.txt")

	r2 := startApp(t, state, "", nil)
	r2.waitFrame("proj in the recents", func(s string) bool {
		return containsAll(s, "proj", "DIRECTORIES")
	})
}

// SH-15: closing the last view with nothing open brings the start view back.
func TestStartViewComesBack(t *testing.T) {
	r := startApp(t, t.TempDir(), "", nil)
	r.waitText("Open Directory")
	r.do(func() error { return r.host.Run(context.Background(), "view.open", registry.Args{"id": "problems"}) })
	r.waitFrame("the start view gone", func(s string) bool { return !strings.Contains(s, "Open Directory") })
	r.do(func() error { return r.host.Run(context.Background(), "view.close", nil) })
	r.waitText("Open Directory")
}

func containsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// SH-15: the start view against 39-start and 39b-start-empty, in both variants.
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
			s := newErrScene(t, "")
			s.sh.recents = &recents{path: filepath.Join(t.TempDir(), "recent.json")}
			if tt.recents {
				addRecents(s)
			}
			st, _ := theme.Stack(theme.Builtins(), "oriel")
			r, err := theme.Resolve(st, tt.variant)
			if err != nil {
				t.Fatal(err)
			}
			s.ui.SetTheme(r)
			s.frame()
			s.frame()
			s.golden(tt.name)
			if tt.mockup == "" {
				return
			}
			img := raster.Render(s.list, s.size, 1, s.ui.Theme().C(theme.BgBase))
			mock, err := snapshot.ReadPNG("../_design/mockups/" + tt.mockup + ".png")
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

// SH-15: a typed path lists itself and the directories in it that start with what's typed after its last slash.
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
		for _, p := range typedDirs(tt.q, home) {
			got = append(got, "~"+strings.TrimPrefix(p, home))
		}
		if strings.Join(got, " ") != tt.want {
			t.Errorf("typedDirs(%q) = %q, want %q", tt.q, got, tt.want)
		}
	}
}

// addRecents puts 39-start's three directories in s's recents.
func addRecents(s *errScene) {
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
		s.sh.recents.add(p, time.Now().Add(-d.ago))
	}
}

// startCard is the start view's card or row named label, not the rail's entry of the same name.
func (s *errScene) startCard(label string) *render.AccessNode {
	s.t.Helper()
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if n.Name == label && n.Role == render.RoleButton && n.Bounds.Min.X >= 0 {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	find(s.ui.AccessTree())
	if found == nil {
		s.t.Fatalf("no start card %q:\n%s", label, s.frameText())
	}
	return found
}

// SH-15: the start view fits the window: the columns shrink, and when both won't fit, Start and Recent become tabs.
func TestStartViewFits(t *testing.T) {
	for _, tt := range []struct {
		width float32
		tabs  bool
	}{{1400, false}, {1000, false}, {640, true}, {420, true}} {
		s := newScene(t, "", "", render.Point{X: tt.width, Y: 700})
		s.sh.recents = &recents{path: filepath.Join(t.TempDir(), "recent.json")}
		addRecents(s)
		s.frame()
		s.frame()
		s.frame()
		for _, l := range []string{"Open Directory", "Settings"} {
			if b := s.startCard(l).Bounds; b.Min.X < 0 || b.Max.X > tt.width {
				t.Errorf("at %v wide, %s is at %v", tt.width, l, b)
			}
		}
		text := s.frameText()
		if got := strings.Contains(text, "notes"); got == tt.tabs {
			t.Errorf("at %v wide, the recents show: %v\n%s", tt.width, got, text)
		}
		if !tt.tabs {
			continue
		}
		for range 4 {
			s.run("start.next", nil)
		}
		s.frame()
		s.frame()
		text = s.frameText()
		if !strings.Contains(text, "notes") || strings.Contains(text, "Open Directory") {
			t.Errorf("at %v wide, selecting a recent didn't switch to its tab:\n%s", tt.width, text)
		}
		if b := s.startCard("oriel").Bounds; b.Max.X > tt.width {
			t.Errorf("at %v wide, the recent oriel is at %v", tt.width, b)
		}
		s.click(centre(s.access("START").Bounds))
		if text := s.frameText(); !strings.Contains(text, "Open Directory") {
			t.Errorf("at %v wide, clicking the Start tab didn't show the actions:\n%s", tt.width, text)
		}
	}
}

// SH-15: the pointer moving onto a card selects it, as the keys do; the keys still move off it.
func TestStartViewHover(t *testing.T) {
	s := newErrScene(t, "")
	s.frame()
	s.frame()
	if !s.startCard("Open Directory").Selected {
		t.Fatal("Open Directory isn't selected at first")
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(s.startCard("Settings").Bounds)})
	s.frame()
	s.frame()
	if !s.startCard("Settings").Selected || s.startCard("Open Directory").Selected {
		t.Fatal("hovering Settings didn't select it")
	}
	s.run("start.prev", nil)
	s.frame()
	s.frame()
	if !s.startCard("Open Directory").Selected || s.startCard("Settings").Selected {
		t.Error("k didn't move the selection off the hovered card")
	}
}

// The window opens where it was when it last closed: its frame is kept in the state directory.
func TestWindowPlaceKept(t *testing.T) {
	state := t.TempDir()
	r := startApp(t, state, "", nil)
	if f := r.win.Opts().Frame; f != (render.Rect{}) {
		t.Fatalf("the first window asked for %v", f)
	}
	r.waitText("Open Directory")
	moved := render.XYWH(200, 150, 1100, 700)
	r.win.Move(moved)
	r.stop()

	r2 := startApp(t, state, "", nil)
	if f := r2.win.Opts().Frame; f != moved {
		t.Errorf("the next window asked for %v, want %v", f, moved)
	}
}

// SH-15: a ✕ shows on a recent under the pointer; clicking it forgets the directory, for the next launch too.
func TestStartViewForgetOnHover(t *testing.T) {
	s := newErrScene(t, "")
	s.sh.recents = &recents{path: filepath.Join(t.TempDir(), "recent.json")}
	addRecents(s)
	s.frame()
	s.frame()
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
		walk(s.ui.AccessTree())
		return n
	}
	if n := forgets(); n != 0 {
		t.Fatalf("%d ✕ show with the pointer away", n)
	}
	s.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: centre(s.startCard("dotfiles").Bounds)})
	s.frame()
	s.frame()
	if n := forgets(); n != 1 {
		t.Fatalf("%d ✕ show with the pointer on dotfiles, want 1", n)
	}
	x := s.access("Forget dotfiles")
	s.click(centre(x.Bounds))
	for _, d := range s.sh.recents.list() {
		if filepath.Base(d.Path) == "dotfiles" {
			t.Fatal("dotfiles is still in the recents")
		}
	}
	if s.ui.NextFrame().IsZero() {
		t.Error("the click asked for no frame to show it")
	}
	s.frame()
	if strings.Contains(s.frameText(), "dotfiles") {
		t.Error("dotfiles still shows")
	}
}

// ui.md 01e: the start view keeps to its own keys. Its entries' and links' keys work (⌘, Settings); the rest don't
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
	r := startApp(t, t.TempDir(), "", setup)
	r.waitText("Open Directory")
	r.typeKeys("<D-y>")
	r.typeKeys("<Space>z")
	r.typeKeys("<D-,>")
	r.waitText("SETTINGS")
	select {
	case k := <-ran:
		t.Errorf("a %s key ran on the start view", k)
	default:
	}

	r2 := startApp(t, t.TempDir(), t.TempDir(), setup)
	r2.typeKeys("<D-y>")
	select {
	case <-ran:
	case <-time.After(5 * time.Second):
		t.Fatal("a global key didn't run with a directory open")
	}
}

// --cpuprofile names the file the CPU profile goes to.
func TestConfigFromFlagsCPUProfile(t *testing.T) {
	cfg, err := ConfigFromFlags([]string{"--cpuprofile", "/tmp/oriel.prof"}, "v1", io.Discard)
	if err != nil || cfg.CPUProfile != "/tmp/oriel.prof" {
		t.Fatalf("CPUProfile %q, err %v", cfg.CPUProfile, err)
	}
}

// ui.md 01e: however many links plugins add under the actions, every one shows whole: the strip wraps onto more
// lines inside the Start column rather than running past it.
func TestStartLinksWrap(t *testing.T) {
	for _, width := range []float32{1400, 1000, 640} {
		s := newScene(t, "", "", render.Point{X: width, Y: 900})
		s.sh.recents = &recents{path: filepath.Join(t.TempDir(), "recent.json")}
		addRecents(s)
		var titles []string
		for i := range 8 {
			title := fmt.Sprintf("Plugin page %d", i)
			titles = append(titles, title)
			s.host.Contribute(registry.StartEntry{Name: fmt.Sprintf("p%d", i), Icon: "jobs", Title: title, Command: "settings.open", Order: 200 + i,
				Link: true, Note: func() string { return "12 waiting" }})
		}
		s.frame()
		s.frame()
		s.frame()
		col := s.startCard("Settings").Bounds
		rows := map[float32]bool{}
		for _, title := range titles {
			b := s.access(title).Bounds
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

// ui.md 01e: Mission control is linked from the start view only while there's something to watch: a session whose
// agent is running, starting, or asking. Sessions that are only resumable don't count.
func TestStartLinksMissionControlWhileAgentsRun(t *testing.T) {
	s := newScene(t, "", "", render.Point{X: 1400, Y: 900})
	s.frame()
	s.frame()
	if strings.Contains(s.frameText(), "Mission control") {
		t.Error("Mission control is linked with no sessions")
	}
	s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Status: core.StatusQuiet}})
	s.frame()
	s.frame()
	if strings.Contains(s.frameText(), "Mission control") {
		t.Error("Mission control is linked for a session whose agent isn't running")
	}
	s.store.Apply(core.SessionSynced{Session: core.SessionState{ID: "w", Name: "w", Kind: core.KindWork, Status: core.StatusWorking,
		Agent: core.AgentState{Running: true}}})
	s.frame()
	s.frame()
	if !strings.Contains(s.frameText(), "Mission control") {
		t.Errorf("Mission control isn't linked while an agent runs:\n%s", s.frameText())
	}
}
