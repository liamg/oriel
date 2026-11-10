package apptest

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/agenttest"
	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/eventstore"
	"github.com/liamg/oriel/internal/isolate"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/headless"
	"github.com/liamg/oriel/ui/layout"
)

// WindowTimeout is how long a Window's app runs before it's stopped.
var WindowTimeout = 20 * time.Second

// Window is the app as Run runs it, on a headless window: its own loop and the real clock, for a test that drives
// an agent or the host while the window keeps drawing. A Scene draws a frame at a time instead.
type Window struct {
	Logs  *app.Logs // what the app logged: the loop held up, among the rest
	T     *testing.T
	Win   *headless.Window
	Store core.Store
	Host  plugin.Host // a plugin's host, for commands run on the loop (Do)
	Dir   string      // the directory it opened
	// ConfigDir is the config directory: init.lua, settings.toml.
	ConfigDir string
	done      chan error
	once      sync.Once

	mu      sync.Mutex
	plugins map[string]plugin.Plugin // each active, by id
}

// WindowOptions are the app a Window runs.
type WindowOptions struct {
	State   string            // the state directory
	WorkDir string            // the directory it opens
	Setup   func(plugin.Host) // runs as the test's own plugin starts
	Config  func(*app.Config) // changes the config first: the session host, say
	Plugins []plugin.Plugin   // more plugins
	Files   []string          // open in the editor at start
	Write   map[string]string // files written under WorkDir first, a directory of the test's if it's ""
	Keys    []registry.Keymap // keymaps the test's own plugin adds
	Timeout time.Duration     // how long the app runs before it's stopped; WindowTimeout if 0
}

// grab hands the test its host.
type grab struct {
	h     plugin.Host
	keys  []registry.Keymap
	setup func(plugin.Host)
}

func (g *grab) Manifest() plugin.Manifest { return plugin.Manifest{ID: "test.grab"} }
func (g *grab) Activate(_ context.Context, h plugin.Host) error {
	g.h = h
	for _, k := range g.keys {
		h.Keymap(k)
	}
	if g.setup != nil {
		g.setup(h)
	}
	return nil
}
func (g *grab) Deactivate(context.Context) error { return nil }

// windowNow is what a Window's hooks are timed by: it doesn't move, so how busy the machine is never makes a hook
// over its budget a warning (a Scene's hooks are timed by its own clock).
func windowNow() time.Time { return time.Time{} }

// NewWindow runs the app on a headless window until the test ends.
func NewWindow(t *testing.T, o WindowOptions) *Window {
	t.Helper()
	isolate.Test(t)
	ctx, cancel := context.WithTimeout(context.Background(), cmp.Or(o.Timeout, WindowTimeout))
	t.Cleanup(cancel)
	if o.Write != nil {
		if o.WorkDir == "" {
			o.WorkDir = t.TempDir()
		}
		rig.WriteFiles(t, o.WorkDir, o.Write)
	}
	be := headless.New()
	g := &grab{keys: o.Keys, setup: o.Setup}
	w := &Window{T: t, Store: eventstore.New(), Dir: o.WorkDir, done: make(chan error, 1), plugins: map[string]plugin.Plugin{}}
	w.ConfigDir = t.TempDir()
	cfg := app.Config{Store: w.Store, Registry: testRegistry(windowNow), ConfigDir: w.ConfigDir, LanguageServers: TestServers, StateDir: o.State, WorkDir: o.WorkDir, Files: o.Files, Plugins: append(o.Plugins, g), Net: isolate.Net(t), ImageNet: isolate.Net(t), SandboxBackends: Backends,
		Voice: app.VoiceConfig{Whisper: OfflineWhisper(t)}, Logs: app.NewLogs(app.LogOpts{Level: slog.LevelDebug})}
	if o.Config != nil {
		o.Config(&cfg)
		w.Store, w.ConfigDir = cfg.Store, cfg.ConfigDir
	}
	w.Logs = cfg.Logs
	activated := cfg.Activated
	cfg.Activated = func(p plugin.Plugin) {
		w.mu.Lock()
		w.plugins[p.Manifest().ID] = p
		w.mu.Unlock()
		if activated != nil {
			activated(p)
		}
	}
	go func() { w.done <- app.Run(ctx, be, cfg) }()
	select {
	case w.Win = <-be.Windows():
	case err := <-w.done:
		t.Fatalf("app exited: %v", err)
	}
	w.Host = g.h
	t.Cleanup(w.Stop)
	return w
}

// HostWindow is a Window on a session host of the fake provider and a fixture repo.
func HostWindow(t *testing.T) (*Window, *rig.Host) {
	t.Helper()
	h := rig.NewHost(t)
	return NewWindow(t, WindowOptions{State: t.TempDir(), Config: Connect(h.W)}), h
}

// WindowPlugin is the plugin w started as id, as a T; the test fails without one. Use it on the loop (Do).
func WindowPlugin[T plugin.Plugin](w *Window, id string) T {
	w.T.Helper()
	w.mu.Lock()
	p, ok := w.plugins[id]
	w.mu.Unlock()
	x, isT := p.(T)
	if !ok || !isT {
		w.T.Fatalf("no plugin %s of type %T", id, x)
	}
	return x
}

// Stop closes the window and waits for the app to exit; it's safe to call again.
func (w *Window) Stop() {
	w.once.Do(func() {
		w.Win.Close()
		<-w.done
	})
}

// Do runs fn on the app's loop and returns its error.
func (w *Window) Do(fn func() error) error {
	w.T.Helper()
	got := make(chan error, 1)
	w.Host.Do(func() { got <- fn() })
	select {
	case err := <-got:
		return err
	case <-time.After(Timeout):
		w.T.Fatal("the loop never ran it")
	}
	return nil
}

// Run runs a command on the loop, failing the test if it fails.
func (w *Window) Run(id string, a registry.Args) {
	w.T.Helper()
	if err := w.Do(func() error { return w.Host.Run(context.Background(), id, a) }); err != nil {
		w.T.Fatal(err)
	}
}

// RunFinished runs a command on the loop and waits until it has finished, its slow part off the loop too (Scene.Run),
// failing the test if it fails.
func (w *Window) RunFinished(id string, a registry.Args) {
	w.T.Helper()
	done := make(chan error, 1)
	send := func(err error) {
		select {
		case done <- err:
		default:
		}
	}
	w.Do(func() error {
		if err := w.Host.Run(registry.OnDone(context.Background(), send), id, a); err != nil {
			send(err)
		}
		return nil
	})
	select {
	case err := <-done:
		if err != nil {
			w.T.Fatal(err)
		}
	case <-time.After(Timeout):
		w.T.Fatalf("%s never finished%s", id, rig.Stuck())
	}
}

// WaitFor polls ok, on the loop, for up to Timeout.
func (w *Window) WaitFor(what string, ok func() bool) {
	w.T.Helper()
	for deadline := time.Now().Add(Timeout); ; time.Sleep(10 * time.Millisecond) {
		var done bool
		w.Do(func() error { done = ok(); return nil })
		if done {
			return
		}
		if time.Now().After(deadline) {
			w.T.Fatalf("waiting for %s%s", what, rig.Stuck())
		}
	}
}

// WaitFile waits for the file name, under Dir, to read want.
func (w *Window) WaitFile(name, want string) {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	var got []byte
	for time.Now().Before(deadline) {
		got, _ = os.ReadFile(filepath.Join(w.Dir, name))
		if string(got) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	w.T.Fatalf("%s = %q, want %q", name, got, want)
}

// WaitNotice waits for the notice id in the notification history, and answers it.
func (w *Window) WaitNotice(id string) core.Notification {
	w.T.Helper()
	st := w.UntilState("notice "+id, func(st core.State) bool {
		return slices.ContainsFunc(st.Notifications, func(n core.Notification) bool { return n.ID == id })
	})
	i := slices.IndexFunc(st.Notifications, func(n core.Notification) bool { return n.ID == id })
	return st.Notifications[i]
}

// Shown are the layout's main panes, read on the loop.
func (w *Window) Shown() []layout.ViewRef {
	w.T.Helper()
	var got []layout.ViewRef
	w.Do(func() error { got = w.Host.Layout().Shown(); return nil })
	return got
}

// UntilState waits for the state to satisfy ok, as rig.WaitState does.
func (w *Window) UntilState(what string, ok func(core.State) bool) core.State {
	w.T.Helper()
	return rig.WaitState(w.T, w.Store, what, ok)
}

// TypeKeys sends keys in vim notation, one key press each.
func (w *Window) TypeKeys(keys string) {
	for _, k := range keymap.Parse(keys) {
		w.Win.Send(kit.KeyEvent(k))
	}
}

// TypeText types text, a character at a time.
func (w *Window) TypeText(s string) {
	for _, ch := range s {
		w.Win.Send(render.KeyEvent{Key: string(ch)})
	}
}

// WaitFrame waits for a frame whose text satisfies ok, and answers that text.
func (w *Window) WaitFrame(what string, ok func(string) bool) string {
	w.T.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()
	f, err := w.Win.WaitFrame(ctx, func(f headless.Frame) bool { return ok(f.Text()) })
	if err != nil {
		fs := w.Win.Frames()
		last := ""
		if len(fs) > 0 {
			last = fs[len(fs)-1].Text()
		}
		w.T.Fatalf("no frame with %s (%d frames); notices %q; last frame:\n%s%s", what, len(fs), rig.NoticeTexts(w.Store.State().Notifications), last, rig.Stuck())
	}
	return f.Text()
}

// WaitText waits for a frame with s in it.
func (w *Window) WaitText(s string) string {
	w.T.Helper()
	return w.WaitFrame(`"`+s+`"`, func(t string) bool { return strings.Contains(t, s) })
}

// Latest waits for the frame on screen, settled (no other drawn for a moment, so nothing's still moving), to satisfy
// ok, and returns it: unlike WaitFrame, not one drawn before.
func (w *Window) Latest(what string, ok func(headless.Frame) bool) headless.Frame {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	seen := -1
	for {
		fs := w.Win.Frames()
		if len(fs) > 0 && len(fs) == seen && ok(fs[len(fs)-1]) {
			return fs[len(fs)-1]
		}
		seen = len(fs)
		if time.Now().After(deadline) {
			last := ""
			if fs := w.Win.Frames(); len(fs) > 0 {
				last = fs[len(fs)-1].Text()
			}
			w.T.Fatalf("the frame on screen never had %s; it is:\n%s%s", what, last, rig.Stuck())
		}
		time.Sleep(40 * time.Millisecond)
	}
}

// WaitGone waits for the frame on screen to be without s.
func (w *Window) WaitGone(s string) {
	w.T.Helper()
	w.Latest("no "+s, func(f headless.Frame) bool { return !strings.Contains(f.Text(), s) })
}

// Click clicks at p.
func (w *Window) Click(p render.Point) {
	w.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	w.Win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary})
	w.Win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary})
}

// RightClickText right-clicks the middle of the first run of text in the frame on screen.
func (w *Window) RightClickText(text string) {
	w.T.Helper()
	w.clickOn(`"`+text+`" to click`, textIn(text, false), render.ButtonSecondary)
}

// clickOn clicks what find finds once it's settled on screen, aimed on the loop at the frame showing: the loop takes
// the click before it builds another, so a frame built meanwhile, by work landing, can't move what it lands on.
func (w *Window) clickOn(what string, find func(headless.Frame) (render.Point, bool), button render.Buttons) {
	w.T.Helper()
	w.clicksOn(what, find, button, 1)
}

// ClickTextTimes clicks the middle of the first run of text n times in quick succession: 2 is a double click, 3 a
// triple. All n are aimed at once, at where the text has settled, so nothing moving between them sends one astray.
func (w *Window) ClickTextTimes(text string, n int) {
	w.T.Helper()
	w.clicksOn(`"`+text+`" to click`, textIn(text, false), render.ButtonPrimary, n)
}

func (w *Window) clicksOn(what string, find func(headless.Frame) (render.Point, bool), button render.Buttons, n int) {
	w.T.Helper()
	find = enabledAt(w, find)
	for deadline := time.Now().Add(Timeout); ; {
		Settled(w, what, find)
		sent := false
		w.Do(func() error {
			fs := w.Win.Frames()
			if p, ok := find(fs[len(fs)-1]); ok {
				w.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: p})
				for range n {
					w.Win.Send(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: button})
					w.Win.Send(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: button})
				}
				sent = true
			}
			return nil
		})
		if sent {
			return
		}
		if time.Now().After(deadline) {
			w.T.Fatalf("%s: gone from screen whenever it was aimed at%s", what, rig.Stuck())
		}
	}
}

// enabledAt is find, but not while what's at its point is disabled: a greyed-out control isn't clicked, it's waited
// for, as a person would.
func enabledAt(w *Window, find func(headless.Frame) (render.Point, bool)) func(headless.Frame) (render.Point, bool) {
	return func(f headless.Frame) (render.Point, bool) {
		p, ok := find(f)
		if ok && disabledAt(w.Win.Access(), p) {
			return p, false
		}
		return p, ok
	}
}

// disabledAt says what's on top at p is disabled, or is inside something disabled: the last node drawn there,
// as a click would reach it, not a page under a dialog.
func disabledAt(root *render.AccessNode, p render.Point) bool {
	top, found := false, false
	var walk func(n *render.AccessNode, dis bool)
	walk = func(n *render.AccessNode, dis bool) {
		if n == nil {
			return
		}
		b := n.Bounds
		in := b == (render.Rect{}) || p.X >= b.Min.X && p.X < b.Max.X && p.Y >= b.Min.Y && p.Y < b.Max.Y
		if !in {
			return
		}
		dis = dis || n.Disabled && b != (render.Rect{})
		if b != (render.Rect{}) {
			top, found = dis, true
		}
		for _, k := range n.Children {
			walk(k, dis)
		}
	}
	walk(root, false)
	return found && top
}

// textAt is the middle of a run of text in the frame on screen, once it's settled there: the first, or with last
// the last, the one on top.
func (w *Window) textAt(text string, last bool) render.Point {
	w.T.Helper()
	return Settled(w, `"`+text+`" to click`, textIn(text, last))
}

// textIn finds the middle of what shows of a run of text in a frame, as a person would aim at it: the first, or with
// last the last, the one on top. Text its clips hide all of, as in a panel still opening, isn't there.
func textIn(text string, last bool) func(headless.Frame) (render.Point, bool) {
	return func(f headless.Frame) (render.Point, bool) {
		var at render.Point
		found := false
		for _, r := range f.Runs() {
			if r.Text != text || !r.Visible() {
				continue
			}
			y := min(max(r.At.Y-(r.At.Y-r.Box.Min.Y)/2, r.Shown.Min.Y), r.Shown.Max.Y) // half its ascent up
			at, found = render.Point{X: (r.Shown.Min.X + r.Shown.Max.X) / 2, Y: y}, true
			if !last {
				break
			}
		}
		return at, found
	}
}

// settleIdle is how long a window that draws nothing new has to show a thing in one place for it to count as settled.
const settleIdle = 200 * time.Millisecond

// Settled waits for find to give the same answer in a later frame than it first did, or for settleIdle with no new
// frame, with every event sent already taken: where a thing has stopped moving, as a person waits for a card to land
// before clicking it. A slow machine draws an animation's frames far apart, and a click aimed where a sliding button
// was, or where it was before a scroll still waiting to be taken, lands where it isn't.
func Settled[T comparable](w *Window, what string, find func(headless.Frame) (T, bool)) T {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	var prev T
	prevFrames, seen := -1, false
	var since time.Time
	for {
		fs := w.Win.Frames()
		if len(fs) > 0 {
			v, ok := find(fs[len(fs)-1])
			switch {
			case w.Win.Pending() > 0: // what's on screen is about to change
				seen = false
			case !ok:
				seen = false
			case seen && v == prev && (len(fs) > prevFrames || time.Since(since) >= settleIdle):
				return v
			case !seen || v != prev:
				prev, prevFrames, since, seen = v, len(fs), time.Now(), true
			}
		}
		if time.Now().After(deadline) {
			last := ""
			if len(fs) > 0 {
				last = fs[len(fs)-1].Text()
			}
			w.T.Fatalf("%s never settled on screen; it is:\n%s%s", what, last, rig.Stuck())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Hover rests the pointer on the first run of text until until is on screen, pointing at it again whenever it moves
// first: what shows on hover goes when the thing under the pointer slides away, as a list still settling moves it.
func (w *Window) Hover(text, until string) {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	var at render.Point
	for {
		if p := w.textAt(text, false); p != at {
			at = p
			w.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
		}
		if fs := w.Win.Frames(); len(fs) > 0 && strings.Contains(fs[len(fs)-1].Text(), until) {
			return
		}
		if time.Now().After(deadline) {
			w.T.Fatalf("hovering %q never showed %q%s", text, until, rig.Stuck())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TextPoint is the middle of the first run of text in the frame on screen.
func (w *Window) TextPoint(text string) render.Point {
	w.T.Helper()
	return w.textAt(text, false)
}

// ClickText clicks the middle of the first run of text in the frame on screen.
func (w *Window) ClickText(text string) {
	w.T.Helper()
	w.clickOn(`"`+text+`" to click`, textIn(text, false), render.ButtonPrimary)
}

// DropFiles drops files from the OS's file manager on the middle of the first run of text in the frame.
func (w *Window) DropFiles(text string, paths ...string) {
	w.T.Helper()
	w.Win.Send(render.DropEvent{Pos: w.textAt(text, false), Paths: paths})
}

// ClickTop clicks the middle of the last run of text drawn in the frame on screen: the one on top.
func (w *Window) ClickTop(text string) {
	w.T.Helper()
	w.clickOn(`"`+text+`" to click`, textIn(text, true), render.ButtonPrimary)
}

// Access is the node of the accessibility tree on screen named label, or nil.
func (w *Window) Access(label string) *render.AccessNode {
	var found *render.AccessNode
	var find func(n *render.AccessNode)
	find = func(n *render.AccessNode) {
		if found == nil && n.Name == label {
			found = n
		}
		for _, k := range n.Children {
			find(k)
		}
	}
	if tr := w.Win.Access(); tr != nil {
		find(tr)
	}
	return found
}

// ClickAccess clicks the control named label, found in the window's accessibility tree where it's settled
// (settled).
func (w *Window) ClickAccess(label string) {
	w.T.Helper()
	w.ClickAccessIn("", label)
}

// ClickAccessIn is ClickAccess for the control named label inside the node named within: one card's button of
// several. An empty within is the whole tree.
func (w *Window) ClickAccessIn(within, label string) {
	w.T.Helper()
	w.clickOn(fmt.Sprintf("%q in %q to click", label, within), func(headless.Frame) (render.Point, bool) {
		n := w.lastAccess(within, label)
		if n == nil {
			return render.Point{}, false
		}
		return Centre(n.Bounds), true
	}, render.ButtonPrimary)
}

// lastAccess is the last node on screen named label inside a node named within (anywhere when within is empty), or nil.
func (w *Window) lastAccess(within, label string) *render.AccessNode {
	var found *render.AccessNode
	var find func(n *render.AccessNode, in bool)
	find = func(n *render.AccessNode, in bool) {
		in = in || within == "" || n.Name == within
		if in && n.Name == label {
			found = n
		}
		for _, k := range n.Children {
			find(k, in)
		}
	}
	if tr := w.Win.Access(); tr != nil {
		find(tr, false)
	}
	return found
}

// HasAll says s has every one of parts.
func HasAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !strings.Contains(s, p) {
			return false
		}
	}
	return true
}

// AgentWindow is a HostWindow with a work session, "w", of the fake agent in front, the window on its worktree.
func AgentWindow(t *testing.T) (*Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	return AgentWindowOn(t, rig.NewHost(t))
}

// AgentWindowOn is AgentWindow on the host given.
func AgentWindowOn(t *testing.T, h *rig.Host) (*Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	return AgentWindowWith(t, h, WindowOptions{})
}

// AgentWindowWith is AgentWindowOn with the window's options: its plugins, a config change past the host's.
func AgentWindowWith(t *testing.T, h *rig.Host, o WindowOptions) (*Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	more := o.Config
	o.Config = func(c *app.Config) {
		Connect(h.W)(c)
		if more != nil {
			more(c)
		}
	}
	if o.State == "" {
		o.State = t.TempDir()
	}
	w := NewWindow(t, o)
	w.UntilState("the machine", func(st core.State) bool { return !st.Machine.Sampled.IsZero() })
	w.Run("session.start", registry.Args{"kind": "work", "name": "w", "provider": "fake", "repos": h.Repo})
	a := h.Fake.WaitStarted(t)
	st := w.UntilState("the session in front", func(st core.State) bool { return st.Focus == "w" })
	se, _ := st.Session("w")
	p, _ := se.Snapshot().Primary()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var dir string
		w.Do(func() error { dir = w.Host.Here().Project; return nil })
		if filepath.Clean(dir) == filepath.Clean(p.Dir) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the window never reopened on %s", p.Dir)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return w, a, h
}

// ConsoleWindow is AgentWindow with the session's console open, as ⌘⇧A opens it.
func ConsoleWindow(t *testing.T) (*Window, *agenttest.FakeSession, *rig.Host) {
	t.Helper()
	w, a, h := AgentWindow(t)
	w.TypeKeys("<D-A>")
	w.WaitText("SESSION STATS")
	w.WaitGit()
	return w, a, h
}

// WaitAccess polls ok, for up to Timeout, while the window draws: what the accessibility tree says, say.
func (w *Window) WaitAccess(what string, ok func(w *Window) bool) {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	for !ok(w) {
		if time.Now().After(deadline) {
			w.T.Fatalf("never: %s%s", what, rig.Stuck())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// WaitFocused waits for the control named label to hold keyboard focus: type into a control a click or command has
// just moved focus to only once it has.
func (w *Window) WaitFocused(label string) {
	w.T.Helper()
	w.WaitAccess(label+" focused", func(w *Window) bool {
		n := w.Access(label)
		return n != nil && n.Focused
	})
}

// ScrollAt scrolls what's under the last run of text drawn, by dy.
func (w *Window) ScrollAt(text string, dy float32) {
	w.T.Helper()
	at := w.textAt(text, true)
	w.Win.Send(render.PointerEvent{Kind: render.PointerMove, Pos: at})
	w.Win.Send(render.PointerEvent{Kind: render.PointerScroll, Pos: at, Scroll: render.Point{Y: dy}})
}

// ReviewWindow is a work session on the fake whose worktree changes round.go from a committed base, with a second
// provider, codex, picked to review it; the diff of the change is open.
func ReviewWindow(t *testing.T) (*Window, *agenttest.FakeSession, *agenttest.Fake, *rig.Host) {
	t.Helper()
	author := agenttest.NewFake()
	codex := &agenttest.Fake{Name: "codex", Started: make(chan *agenttest.FakeSession, 4), Offers: []agent.Model{{ID: "gpt-5", Default: true}}}
	lw := rig.NewHost(t, author, codex)
	lw.Fake = author
	r, a, _ := AgentWindowOn(t, lw)
	if err := r.Do(func() error {
		return r.Host.Options().Set("review.ai.provider", registry.LayerUser, registry.At{}, "codex")
	}); err != nil {
		t.Fatal(err)
	}
	se, _ := r.Store.Session("w")
	p, _ := se.Snapshot().Primary()
	dir := p.Dir
	path := filepath.Join(dir, "round.go")
	os.WriteFile(path, []byte(rig.RoundBase), 0o644)
	rig.Git(t, dir, "add", "-A")
	rig.Git(t, dir, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-qm", "base")
	rig.Git(t, dir, "update-ref", "refs/heads/main", "HEAD")
	os.WriteFile(path, []byte(rig.RoundAgent), 0o644)
	r.Do(func() error { return r.Host.Run(context.Background(), "diff.open", registry.Args{"path": dir}) })
	r.WaitFrame("the diff with the AI review column", func(s string) bool {
		return HasAll(s, "func roundHalfEven", "AI REVIEW", "No AI review yet.", "REVIEWER", "Codex", "Review changes", "Prompt…")
	})
	return r, a, codex, lw
}

// ProjectDir is session id's primary project's directory.
func (w *Window) ProjectDir(id core.SessionID) string {
	w.T.Helper()
	se, ok := w.Store.Session(id)
	if !ok {
		w.T.Fatalf("no session %s", id)
	}
	p, _ := se.Snapshot().Primary()
	return p.Dir
}

// WaitGit waits for git's work, and every tree's read, to be done, as someone waits for a busy view to settle before
// clicking in it: a status coming in builds the views on it again, and a click under way when it does is lost.
func (w *Window) WaitGit() {
	w.T.Helper()
	deadline := time.Now().Add(Timeout)
	for {
		var busy bool
		w.Do(func() error { busy = kit.GitOf(w.Host).Busy(); return nil })
		if !busy {
			return
		}
		if time.Now().After(deadline) {
			w.T.Fatal("git's work never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// HeldUp are the times the window's loop was held up, as its watchdog logged them: by the machine, with Oriel's share
// of the CPU, or by its own work, with where.
func (w *Window) HeldUp() []string {
	if w.Logs == nil {
		return nil
	}
	var out []string
	for _, r := range w.Logs.Ring.Records() {
		if !strings.HasPrefix(r.Msg, "loop held up") {
			continue
		}
		line := r.Time.Format("15:04:05.000") + " " + r.Msg
		for _, a := range r.Attrs {
			line += " " + a.Key + "=" + a.Value
		}
		out = append(out, line)
	}
	return out
}
