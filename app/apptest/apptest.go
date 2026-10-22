// Package apptest drives the app in a test from outside package app: its built-ins with no window, drawn a frame at
// a time on a clock the test sets. A feature package's tests use it, driving the app through commands, keys and
// clicks, and reading what it drew.
package apptest

import (
	"cmp"
	"context"
	"fmt"
	"github.com/liamg/oriel/tools/perf"
	"image/png"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/script/api"

	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/voice"
)

// MockupSize is the mockups' window, in points.
var MockupSize = render.Point{X: 1600, Y: 1000}

// Timeout is how long Run, Press and Until wait.
var Timeout = 15 * time.Second

// InitLua is the init.lua a scene's config starts with: a keymap, a mode's key and a Permission hook.
const InitLua = `local L = oriel
L.keymap.set({ "n", "x" }, "ga", function() L.thread.new() end)
L.keymap.set("requests", "x", "thread.resolve")

-- auto-allow test runs in this workspace
L.on("Permission", function(ev)
  if ev.tool == "Bash" and ev.args:match("^go test ") then
    return "allow"
  end
end)
`

// Backends are the sandbox backends a scene's machine offers, whatever this one has.
func Backends() []string { return []string{"docker", "seatbelt"} }

// OfflineNet is the HTTP a scene's plugins get unless the test gives its own: loopback only, so no test reaches the
// network; its connections close when the test ends.
func OfflineNet(t testing.TB) *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if ip, err := netip.ParseAddr(host); host != "localhost" && (err != nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("tests don't reach the network: %s", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}

// OfflineWhisper is dictation's engine with its models in a directory of the test's, downloaded offline.
func OfflineWhisper(t testing.TB) *voice.Whisper {
	w := voice.New(nil)
	w.Dir, w.Net = t.TempDir(), OfflineNet(t)
	return w
}

// Desktop is a clipboard in memory, and a browser that opens nothing but notes what it was asked to.
type Desktop struct {
	Text   string
	Opened []string
}

func (d *Desktop) SetClipboard(s string)         { d.Text = s }
func (d *Desktop) ReadClipboard() (string, bool) { return d.Text, d.Text != "" }
func (d *Desktop) OpenURL(u string) error {
	d.Opened = append(d.Opened, u)
	return nil
}

// Options are the app a scene starts.
type Options struct {
	CrashDir string
	WorkDir  string            // files are relative to it
	Size     render.Point      // MockupSize if zero
	Config   func(*app.Config) // changes the config before the app starts: the session host, say
	ImageNet *http.Client      // what remote images are fetched with; offline if nil
}

// Scene is the app's built-ins over a store, drawn the way Run draws the window, on a fixed clock.
type Scene struct {
	*app.Headless
	T         testing.TB
	Now       time.Time // the window's clock
	Desktop   *Desktop
	ConfigDir string    // the config directory: init.lua, settings.toml, themes/
	Logs      *app.Logs // the window's logs, its ring of records
	// OverviewOff hides the project overview, whose temporary paths would change every run; overview tests dispose
	// it.
	OverviewOff registry.Disposable
}

// New starts a scene with a home and config of the test's, and stops it when the test ends.
func New(t testing.TB, o Options) *Scene {
	t.Helper()
	isolate.Test(t)
	if o.Size == (render.Point{}) {
		o.Size = MockupSize
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	conf := filepath.Join(home, ".config", "oriel")
	os.MkdirAll(conf, 0o755)
	os.WriteFile(filepath.Join(conf, "init.lua"), []byte(InitLua), 0o644)
	s := &Scene{T: t, Desktop: &Desktop{}, ConfigDir: conf}
	cfg := app.Config{Store: eventstore.New(), Registry: uireg.New(), Logs: app.NewLogs(app.LogOpts{}), ConfigDir: conf, CrashDir: o.CrashDir,
		Version: "v0.4.2", WorkDir: o.WorkDir, Desktop: s.Desktop, Net: OfflineNet(t), ImageNet: cmp.Or(o.ImageNet, OfflineNet(t)), SandboxBackends: Backends,
		Voice: app.VoiceConfig{Whisper: OfflineWhisper(t)}}
	if o.Config != nil {
		o.Config(&cfg)
	}
	s.Logs = cfg.Logs
	h, err := app.NewHeadless(cfg, app.HeadlessOpts{Size: o.Size, Clock: func() time.Time { return s.Now }})
	if err != nil {
		t.Fatal(err)
	}
	s.Headless = h
	t.Cleanup(func() {
		if err := h.Close(); err != nil {
			t.Errorf("closing the scene: %v", err)
		}
	})
	s.OverviewOff = h.Registry().View(app.OverviewView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() }))
	return s
}

// PluginOf is the plugin started as id, as a T.
func PluginOf[T plugin.Plugin](s *Scene, id string) T {
	s.T.Helper()
	p, ok := s.Plugin(id)
	x, isT := p.(T)
	if !ok || !isT {
		s.T.Fatalf("no plugin %s of type %T", id, x)
	}
	return x
}

// Apply applies events to the state, as the session host's would.
func (s *Scene) Apply(evs ...core.Event) {
	s.T.Helper()
	for _, ev := range evs {
		if err := s.Store().Apply(ev); err != nil {
			s.T.Fatal(err)
		}
	}
}

// Run runs a command and waits until it has finished, drawing frames while its slow part is off the loop.
func (s *Scene) Run(id string, args registry.Args) {
	s.T.Helper()
	var err error
	done := false
	s.Registry().Run(registry.OnDone(context.Background(), func(e error) { err, done = e, true }), id, args)
	if !done && !s.Headless.Until(Timeout, func() bool { return done }) {
		s.T.Fatalf("%s never finished", id)
	}
	if err != nil {
		s.T.Fatal(err)
	}
}

// Press presses key as the window does a key on its own (Headless.Press).
func (s *Scene) Press(key string) {
	s.T.Helper()
	if err := s.Headless.Press(key); err != nil {
		s.T.Fatal(err)
	}
}

// Type presses each key (a character, or a name such as "CR", "Esc") on what has focus.
func (s *Scene) Type(keys ...string) {
	s.T.Helper()
	for _, k := range keys {
		if !s.Key(k) {
			s.T.Fatalf("nothing focused takes %q", k)
		}
	}
}

// Until draws frames, running the work handed to the loop, until ok; it fails the test after Timeout.
func (s *Scene) Until(what string, ok func() bool) {
	s.T.Helper()
	if !s.Headless.Until(Timeout, ok) {
		s.T.Fatalf("waiting for %s:\n%s", what, s.Text())
	}
}

// Hold presses mods (0 lets go) and draws, d later on the clock.
func (s *Scene) Hold(m render.Mods, d time.Duration) {
	s.UI().Mods(m)
	s.Frame()
	s.Now = s.Now.Add(d)
	s.Frame()
	s.Frame()
}

// SettleReads draws frames until what they read from disk (kit.Reads) is in.
func (s *Scene) SettleReads() {
	s.T.Helper()
	rd, ok := kit.ReadsOf(s.Registry())
	if !ok {
		return
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(time.Millisecond) {
		s.Frame()
		if !rd.Busy() {
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatal("the window's reads never came in")
		}
	}
}

// Golden draws two frames, once what they read is in, and checks the second against testdata/name.list and .png;
// ORIEL_UPDATE=1 writes them.
func (s *Scene) Golden(name string) {
	s.T.Helper()
	s.SettleReads()
	s.Frame()
	s.Frame()
	if o := s.UI().Overflowing(); len(o) > 0 {
		s.T.Errorf("%s: text drawn past its box:\n%s", name, strings.Join(o, "\n"))
	}
	snapshot.Golden(s.T, "testdata/"+name+".list", []byte(snapshot.List(s.List())))
	img := raster.Render(s.List(), s.Size(), s.Scale(), s.UI().Theme().C(theme.BgBase))
	snapshot.GoldenPNG(s.T, "testdata/"+name+".png", img, 0.02, 0.001)
}

// Shot draws two frames and writes the second to $ORIEL_SHOTS/name.png: a screenshot for the docs.
func (s *Scene) Shot(name string) {
	s.T.Helper()
	s.SettleReads()
	s.Frame()
	s.Frame()
	img := raster.Render(s.List(), s.Size(), 1, s.UI().Theme().C(theme.BgBase))
	f, err := os.Create(filepath.Join(os.Getenv("ORIEL_SHOTS"), name+".png"))
	if err != nil {
		s.T.Fatal(err)
	}
	defer f.Close()
	png.Encode(f, img)
}

// Lint fails the test for each interactive node in the frame with no help id, or one that doesn't resolve.
func (s *Scene) Lint(state string) {
	s.T.Helper()
	for _, p := range s.HelpLint() {
		s.T.Errorf("%s: %s", state, p)
	}
}

// FullText is the frame's texts one a line, whole where the frame cut them to fit with "…".
func (s *Scene) FullText() string {
	var lines []string
	for _, l := range strings.Split(s.UI().Dump(), "\n") {
		l = strings.TrimSpace(l)
		if i := strings.Index(l, " \""); strings.HasPrefix(l, "text ") && i > 0 {
			if q, err := strconv.QuotedPrefix(l[i+1:]); err == nil {
				t, _ := strconv.Unquote(q)
				lines = append(lines, t)
			}
		}
	}
	return strings.Join(lines, "\n")
}

// WantFrame fails the test for each of wants the frame lacks.
func (s *Scene) WantFrame(wants ...string) {
	s.T.Helper()
	text := s.Text()
	for _, w := range wants {
		if !strings.Contains(text, w) {
			s.T.Errorf("the frame lacks %q:\n%s", w, text)
		}
	}
}

// TextPoint is the middle of the first run of text in the frame that reads text.
func (s *Scene) TextPoint(text string) render.Point {
	s.T.Helper()
	if s.List() == nil {
		s.T.Fatalf("no %q: no frame drawn yet", text)
	}
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}
		}
	}
	s.T.Fatalf("no %q in the frame", text)
	return render.Point{}
}

// TextStart is where the first run of text starting with prefix is drawn: its pen on the baseline.
func (s *Scene) TextStart(prefix string) (render.Point, bool) {
	for _, op := range s.List().Ops {
		if op.Kind == render.OpGlyphs && strings.HasPrefix(op.Run.Text, prefix) {
			return op.At, true
		}
	}
	return render.Point{}, false
}

// Drag presses at from, moves to to in steps, and lets go there.
func (s *Scene) Drag(from, to render.Point) {
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: from})
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: from, Buttons: render.ButtonPrimary})
	s.Frame()
	for i := 1; i <= 4; i++ {
		f := float32(i) / 4
		s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: render.Point{X: from.X + (to.X-from.X)*f, Y: from.Y + (to.Y-from.Y)*f}})
		s.Frame()
	}
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: to, Buttons: render.ButtonPrimary})
	s.Frame()
	s.Frame()
}

// MeanFrame is how long a frame takes, on average over 100 after 10 to settle, timed by perf.Times.
func (s *Scene) MeanFrame() time.Duration {
	s.Frames(10)
	return MeanFrames(s)[0]
}

// MeanFrames is each scene's MeanFrame, their frames taken in turn so what runs alongside slows each alike.
func MeanFrames(scenes ...*Scene) []time.Duration {
	for _, s := range scenes {
		s.Frames(10)
	}
	n := len(scenes)
	times := perf.Times(3, 100*n, nil, func(i int) { scenes[i%n].Frame() })
	out := make([]time.Duration, n)
	for i := range out {
		var mine []time.Duration
		for j := i; j < len(times); j += n {
			mine = append(mine, times[j])
		}
		out[i] = perf.Mean(mine)
	}
	return out
}

// Frames draws n frames, and more while what they read from disk is coming in.
func (s *Scene) Frames(n int) {
	for range n {
		s.Frame()
	}
	if rd, ok := kit.ReadsOf(s.Registry()); ok && rd.Busy() {
		s.SettleReads()
	}
}

// ClickText clicks the middle of the first run of text that reads text.
func (s *Scene) ClickText(text string) {
	s.T.Helper()
	s.Click(s.TextPoint(text), 0)
}

// ClickTop clicks the run of text reading text painted last, on top of any other, and draws a frame.
func (s *Scene) ClickTop(text string) {
	s.T.Helper()
	p, ok := s.TextAt(text)
	if !ok {
		s.T.Fatalf("no %q in the frame", text)
	}
	s.Click(p, 0)
	s.Frame()
}

// FindRole is the first node in the accessibility tree with role and named label, or nil.
func (s *Scene) FindRole(role render.Role, label string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n.Role == role && n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// NodeRole is FindRole's node; the test fails without one.
func (s *Scene) NodeRole(role render.Role, label string) *render.AccessNode {
	s.T.Helper()
	n := s.FindRole(role, label)
	if n == nil {
		s.T.Fatalf("no %s labelled %q", role, label)
	}
	return n
}

// Find is the first node in the accessibility tree named label, or nil.
func (s *Scene) Find(label string) *render.AccessNode {
	var find func(n *render.AccessNode) *render.AccessNode
	find = func(n *render.AccessNode) *render.AccessNode {
		if n == nil {
			return nil
		}
		if n.Name == label {
			return n
		}
		for _, k := range n.Children {
			if f := find(k); f != nil {
				return f
			}
		}
		return nil
	}
	return find(s.Access())
}

// Node is the first node in the accessibility tree named label; the test fails without one.
func (s *Scene) Node(label string) *render.AccessNode {
	s.T.Helper()
	n := s.Find(label)
	if n == nil {
		s.T.Fatalf("nothing labelled %q", label)
	}
	return n
}

// Count is how many nodes in the accessibility tree are named label.
func (s *Scene) Count(label string) int {
	n := 0
	var walk func(*render.AccessNode)
	walk = func(a *render.AccessNode) {
		if a.Name == label {
			n++
		}
		for _, k := range a.Children {
			walk(k)
		}
	}
	walk(s.Access())
	return n
}

// Centre is the middle of r.
func Centre(r render.Rect) render.Point {
	return render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
}

// place is the place named name (a page: "stats", "usage"), if one's registered.
func (s *Scene) place(name string) (uireg.Place, bool) {
	for _, c := range s.Registry().Contributions("place") {
		if p := c.(uireg.Place); p.Name == name {
			return p, true
		}
	}
	return uireg.Place{}, false
}

// Shown says whether the place named name is showing.
func (s *Scene) Shown(name string) bool {
	p, ok := s.place(name)
	return ok && p.Shown()
}

// Close puts the place named name away, as going elsewhere would.
func (s *Scene) Close(name string) {
	s.T.Helper()
	p, ok := s.place(name)
	if !ok {
		s.T.Fatalf("no place %s", name)
	}
	p.Close()
	s.Frame()
}

// Showing says whether a view with id is in the layout, visible.
func (s *Scene) Showing(id string) bool {
	for _, v := range s.Host().Layout().Visible() {
		if v.ID == id {
			return true
		}
	}
	return false
}

// ClickNode clicks the control named label (ClickOn), and draws two frames after for what it opened to lay out.
func (s *Scene) ClickNode(label string) {
	s.T.Helper()
	s.ClickOn(label)
	s.Frame()
	s.Frame()
}

// ClickOn clicks the middle of the control named label once it's enabled and settled, in the same place two frames
// running, pressing and letting go on the frame it was found in.
func (s *Scene) ClickOn(label string) {
	s.T.Helper()
	var last *render.AccessNode
	for deadline := time.Now().Add(Timeout); ; {
		s.Frame()
		n := s.Find(label)
		if n != nil && last != nil && !n.Disabled && n.Bounds == last.Bounds {
			s.Click(Centre(n.Bounds), 0)
			return
		}
		last = n
		if time.Now().After(deadline) {
			s.T.Fatalf("%q never enabled and settled:\n%s", label, s.Text())
		}
		s.RunQueued()
		time.Sleep(5 * time.Millisecond)
	}
}

// Config is the app's config, as Options.Config changes it.
type Config = app.Config

// Connect is a config change that gives the app a session host, and its store.
func Connect(w *host.Host) func(*Config) {
	return func(cfg *Config) { cfg.Host, cfg.Store = w, w.Store() }
}

// Within is the first node inside the one whose label starts with in whose own label starts with label.
func (s *Scene) Within(in, label string) *render.AccessNode {
	s.T.Helper()
	var find func(n *render.AccessNode, name string) *render.AccessNode
	find = func(n *render.AccessNode, name string) *render.AccessNode {
		if strings.HasPrefix(n.Name, name) {
			return n
		}
		for _, k := range n.Children {
			if f := find(k, name); f != nil {
				return f
			}
		}
		return nil
	}
	parent := find(s.Access(), in)
	if parent == nil {
		s.T.Fatalf("nothing named %q", in)
	}
	n := find(parent, label)
	if n == nil {
		s.T.Fatalf("nothing named %q in %q", label, in)
	}
	return n
}

// Has says whether the frame has every one of texts.
func (s *Scene) Has(texts ...string) bool {
	frame := s.Text()
	for _, t := range texts {
		if !strings.Contains(frame, t) {
			return false
		}
	}
	return true
}

// HostScene is a scene with a session host of the fake provider and a fixture repo, its state in a directory of
// the test's; change changes the config after.
func HostScene(t *testing.T, change ...func(*Config)) (*Scene, *rig.Host) {
	t.Helper()
	h := rig.NewHost(t)
	state := t.TempDir()
	s := New(t, Options{Config: func(cfg *Config) {
		Connect(h.W)(cfg)
		cfg.StateDir = state
		for _, f := range change {
			f(cfg)
		}
	}})
	return s, h
}

// TypeInto clicks the node named label and types text into it, a character at a time.
func (s *Scene) TypeInto(label, text string) {
	s.T.Helper()
	s.Click(Centre(s.Node(label).Bounds), 0)
	s.TypeText(text)
}

// TypeText types text on what has focus, a character at a time.
func (s *Scene) TypeText(text string) {
	for _, r := range text {
		s.UI().Key(render.KeyEvent{Key: string(r)})
		s.Frame()
	}
}

// HasCommand says a command id is registered.
func (s *Scene) HasCommand(id string) bool {
	return slices.ContainsFunc(s.Registry().Commands(), func(c registry.Command) bool { return c.ID == id })
}

// InboxCard is the card id from the inbox source named source.
func (s *Scene) InboxCard(source, id string) (uireg.InboxCard, bool) {
	for _, c := range s.Registry().Contributions("inbox.source") {
		if src := c.(uireg.InboxSource); src.Name == source {
			for _, card := range src.Cards() {
				if card.ID == id {
					return card, true
				}
			}
		}
	}
	return uireg.InboxCard{}, false
}

// Notices are the notification history's titles and texts, for a failure's message.
func (s *Scene) Notices() []string { return rig.NoticeTexts(s.Store().State().Notifications) }

// UntilState draws frames until ok holds of the state, as Until does, and answers that state.
func (s *Scene) UntilState(what string, ok func(core.State) bool) core.State {
	s.T.Helper()
	var st core.State
	s.Until(what, func() bool { st = s.Store().State(); return ok(st) })
	return st
}

// Reopen does what the window does when it's asked to open again on a directory (a session's): every plugin starts
// again on it. It says whether it was asked.
func (s *Scene) Reopen() bool {
	s.T.Helper()
	asked, err := s.Headless.Reopen()
	if err != nil {
		s.T.Fatal(err)
	}
	if asked {
		s.OverviewOff = s.Registry().View(app.OverviewView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node { return ui.ColumnNode() }))
	}
	return asked
}

// Retheme resolves ui.theme and ui.variant, as the window does when a theme changes.
func (s *Scene) Retheme() {
	s.T.Helper()
	if err := s.Headless.Retheme(); err != nil {
		s.T.Fatal(err)
	}
}

// WriteConfig writes files under the config directory.
func (s *Scene) WriteConfig(files map[string]string) {
	s.T.Helper()
	for name, src := range files {
		p := filepath.Join(s.ConfigDir, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			s.T.Fatal(err)
		}
	}
}

// WaitGit waits for the git work running off the loop, as the GitTrees service says.
func (s *Scene) WaitGit() {
	for kit.GitOf(s.Registry()).Busy() {
		time.Sleep(time.Millisecond)
	}
}

// PluginVM is a Lua VM with the plugin API p is granted, as an installed plugin's.
func (s *Scene) PluginVM(p plugin.Caller) *script.VM {
	vm := script.New()
	apis := api.InstallPlugin(vm, s.HostAs("builtin:lua"), p)
	s.T.Cleanup(func() { apis.Dispose(); vm.Close() })
	return vm
}

// SettleGit frames until every project's git status has come: the window's work tree's first read, and each project's,
// as the GitTrees service says.
func (s *Scene) SettleGit() {
	s.T.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		s.Frame()
		if !kit.GitOf(s.Registry()).Busy() && s.gitRead() {
			s.Frame()
			return
		}
		if time.Now().After(deadline) {
			s.T.Fatal("the projects' git status never came")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// gitRead says the window's work tree and each project's have been read once.
func (s *Scene) gitRead() bool {
	trees := kit.GitOf(s.Registry())
	dirs := []string{""}
	for _, p := range kit.ProjectsHere(s.Host()).Projects {
		dirs = append(dirs, p.Dir)
	}
	for _, d := range dirs {
		if !trees.Tree(d).Load().Done {
			return false
		}
	}
	return true
}

// WaitFor frames until the frame's text, its lines run together, has want, for up to 5s, and answers that text.
func (s *Scene) WaitFor(want string) string {
	s.T.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s.Frame()
		text := strings.ReplaceAll(s.Text(), "\n", "")
		if strings.Contains(text, want) || time.Now().After(deadline) {
			return text
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// RightClick right-clicks p, and answers the frame's text after: the menu it opened.
func (s *Scene) RightClick(p render.Point) string {
	s.Frame()
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonSecondary})
	s.UI().Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonSecondary})
	s.Frame()
	s.Frame()
	return s.Text()
}

// StartCard is the start view's button labelled label.
func (s *Scene) StartCard(label string) *render.AccessNode {
	s.T.Helper()
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
	find(s.Access())
	if found == nil {
		s.T.Fatalf("no start card %q:\n%s", label, s.Text())
	}
	return found
}

// CountPrefix is how many nodes in the accessibility tree have a name starting with prefix.
func (s *Scene) CountPrefix(prefix string) int {
	n := 0
	var walk func(*render.AccessNode)
	walk = func(a *render.AccessNode) {
		if strings.HasPrefix(a.Name, prefix) {
			n++
		}
		for _, k := range a.Children {
			walk(k)
		}
	}
	walk(s.Access())
	return n
}

// LastNotice is the newest notification whose ID starts with prefix.
func LastNotice(st core.State, prefix string) (core.Notification, bool) {
	for _, n := range slices.Backward(st.Notifications) {
		if strings.HasPrefix(n.ID, prefix) {
			return n, true
		}
	}
	return core.Notification{}, false
}

// Day is the mockups' day, in UTC.
var Day = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

// At is a time of day on Day.
func At(h, m, s int) time.Time {
	return Day.Add(time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s)*time.Second)
}
