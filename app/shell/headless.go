package shell

import (
	"context"
	"errors"
	"fmt"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/internal/fsx"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Headless is the app as Run starts it, with no window and no loop of its own: the caller draws each frame on its
// own goroutine, at a size, on the clock it gives. The app's tests and app/plugintest drive it.
type Headless struct {
	sh      *shell
	ui      *ui.UI
	ts      *typeset.Typesetter
	plugins []plugin.Plugin
	host    plugin.Host // the last built-in's
	size    render.Point
	scale   float32 // the zoom it's drawn at; 1 when 0
	list    *render.List
	cancel  func()
}

// HeadlessOpts is how a Headless app is drawn.
type HeadlessOpts struct {
	Size  render.Point     // 1280×800 if zero
	Clock func() time.Time // the window's clock (oriel.now, countdowns); time.Now if nil
	// FontDirs are where its installed fonts are (ui.font): none if nil, so a scene draws the same everywhere.
	FontDirs []string
}

// memDesktop is a clipboard in memory, and a browser that opens nothing but notes what it was asked to.
type memDesktop struct {
	text   string
	opened []string
}

func (c *memDesktop) SetClipboard(s string)              { c.text = s }
func (c *memDesktop) ReadClipboard() (string, bool)      { return c.text, c.text != "" }
func (c *memDesktop) ReadClipboardFiles() []string       { return nil }
func (c *memDesktop) ReadClipboardImage() ([]byte, bool) { return nil, false }
func (c *memDesktop) Reveal(p string) error {
	c.opened = append(c.opened, "reveal:"+p)
	return nil
}
func (c *memDesktop) OpenURL(u string) error {
	c.opened = append(c.opened, u)
	return nil
}

// NewHeadless starts builtins and cfg.Plugins over cfg, drawn in the dark oriel theme with motion reduced,
// so frames are of the settled UI.
func NewHeadless(cfg Config, builtins Builtins, o HeadlessOpts) (*Headless, error) {
	cfg.defaults()
	if cfg.Desktop == nil {
		cfg.Desktop = &memDesktop{}
	}
	if o.Size == (render.Point{}) {
		o.Size = render.Point{X: 1280, Y: 800}
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	ts, err := typeset.NewBundled()
	if err != nil {
		return nil, err
	}
	dirs := o.FontDirs
	if dirs == nil {
		dirs = []string{} // none: not the machine's
	}
	ts.SetFontDirs(dirs)
	sh, cancel := newShell(cfg, builtins, func() {})
	sh.desktop = cfg.Desktop
	sh.launchDone = true // a window that's been up a while: mission control doesn't open as at launch
	unfont := contributeFonts(cfg.Registry, ts, func() {})
	h := &Headless{sh: sh, size: o.Size, cancel: func() { cancel(); unfont() }}
	last, err := sh.activate(context.Background(), &h.plugins)
	if err != nil {
		h.Close()
		return nil, err
	}
	h.host = last
	h.ts = ts
	h.ui = ui.New(ts, textStyle)
	h.ui.SetClipboard(cfg.Desktop)
	sh.provide(h.ui)
	st, _ := theme.Stack(theme.Builtins(), "oriel")
	r, err := theme.Resolve(st, theme.Dark)
	if err != nil {
		h.Close()
		return nil, err
	}
	h.ui.SetTheme(r)
	uo := h.ui.Options()
	uo.ReducedMotion = true
	h.ui.SetOptions(uo)
	h.ui.SetClock(o.Clock)
	sh.ui.Store(h.ui)
	sh.theme = h.ui.Theme
	sh.shot = func(th *theme.Resolved) *image.RGBA {
		if th == nil {
			th = h.ui.Theme()
		}
		return shotOf(h.ui, sh.cfg.Registry, h.size, th, frameWindow(h.host.State(), sh.cfg.WorkDir))
	}
	return h, nil
}

func (h *Headless) Registry() uireg.Registry { return h.sh.cfg.Registry }
func (h *Headless) Store() core.Store        { return h.sh.store }
func (h *Headless) UI() *ui.UI               { return h.ui }
func (h *Headless) Size() render.Point       { return h.size }

// Resize draws the next frames at size.
func (h *Headless) Resize(size render.Point) { h.size = size }

// Zoom draws the next frames at z: the same window, laid out at its size over z, as ui.font_size does.
func (h *Headless) Zoom(z float32) {
	h.size = render.Point{X: h.size.X * h.zoomed() / z, Y: h.size.Y * h.zoomed() / z}
	h.scale = z
}

// Activate starts e now, as one of cfg.Plugins would have been; Close stops it.
func (h *Headless) Activate(e plugin.Plugin) error {
	if err := e.Activate(context.Background(), h.sh.host(e.Manifest().ID)); err != nil {
		return err
	}
	h.plugins = append(h.plugins, e)
	return nil
}

// Close stops every plugin, the last started first.
func (h *Headless) Close() error {
	var err error
	for _, e := range slices.Backward(h.plugins) {
		err = errors.Join(err, e.Deactivate(context.Background()))
	}
	h.plugins = nil
	kit.ForgetLanguages(h.sh.cfg.Registry)
	if h.ts != nil {
		h.ts.Close()
	}
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	return err
}

func (h *Headless) zoomed() float32 {
	if h.scale == 0 {
		return 1
	}
	return h.scale
}

// Frame draws one frame, as the window would.
func (h *Headless) Frame() {
	defer fsx.Enter()()
	h.sh.seeNav(h.sh.cfg.Registry, h.ui.Now())
	h.list = new(render.List)
	start := time.Now()
	h.ui.Frame(h.list.Record(h.zoomed()), h.size, func(c *ui.Ctx) {
		buildWindow(c, h.sh.cfg.Registry, h.size, frameWindow(h.host.State(), h.sh.cfg.WorkDir))
	})
	h.sh.frames.Add(time.Now(), time.Since(start), 0, h.ui.WokenBy())
}

// List is the last frame's display list.
func (h *Headless) List() *render.List { return h.list }

// RunQueued runs the work plugins handed the loop (Host.Do).
// Queued says the loop has been handed work it hasn't run.
func (h *Headless) Queued() bool { return h.sh.queued() }

func (h *Headless) RunQueued() {
	defer fsx.Enter()()
	h.sh.runTodo()
}

// Until draws frames, running what the loop was handed, until ok or the timeout; it says whether ok came.
func (h *Headless) Until(timeout time.Duration, ok func() bool) bool {
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			return false
		}
		h.Frame()
		h.RunQueued()
		time.Sleep(5 * time.Millisecond)
	}
	h.Frame()
	return true
}

// Text is the last frame's runs of text, one a line, in the order they were painted.
func (h *Headless) Text() string {
	if h.list == nil {
		return ""
	}
	var lines []string
	for _, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs {
			lines = append(lines, op.Run.Text)
		}
	}
	return strings.Join(lines, "\n")
}

// TextAt is the middle of the run of text reading text painted last, on top of any other.
func (h *Headless) TextAt(text string) (render.Point, bool) {
	if h.list == nil {
		return render.Point{}, false
	}
	for _, op := range slices.Backward(h.list.Ops) {
		if op.Kind == render.OpGlyphs && op.Run.Text == text {
			return render.Point{X: op.At.X + op.Run.Advance/2, Y: op.At.Y - op.Run.Ascent/2}, true
		}
	}
	return render.Point{}, false
}

// Access is the accessibility tree of the last frame.
func (h *Headless) Access() *render.AccessNode { return h.ui.AccessTree() }

// Click moves the pointer to p and presses there with mods held, on the frame drawn last (the one p was read from,
// so nothing has moved under it), draws a frame with the button down, then lets go and draws another.
func (h *Headless) Click(p render.Point, mods render.Mods) {
	h.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	h.ui.Pointer(render.PointerEvent{Kind: render.PointerPress, Pos: p, Buttons: render.ButtonPrimary, Mods: mods})
	h.Frame()
	h.ui.Pointer(render.PointerEvent{Kind: render.PointerRelease, Pos: p, Buttons: render.ButtonPrimary, Mods: mods})
	h.Frame()
}

// Key presses key (vim notation's name: "a", "CR", "Esc") on what has focus, then draws a frame. It says whether
// anything took it.
func (h *Headless) Key(key string) bool {
	ok := h.ui.Key(render.KeyEvent{Key: key})
	h.Frame()
	return ok
}

// CallTool calls an agent tool as its MCP server does, off the loop, drawing frames until it answers (a Lua tool
// runs on the loop) or the timeout.
func (h *Headless) CallTool(ctx context.Context, timeout time.Duration, tool registry.AgentTool, args map[string]any) (string, error) {
	type answer struct {
		out string
		err error
	}
	done := make(chan answer, 1)
	go func() {
		out, err := registry.CallAgentTool(ctx, h.sh.cfg.Registry, tool, args)
		done <- answer{out, err}
	}()
	var a answer
	if !h.Until(timeout, func() bool {
		select {
		case a = <-done:
			return true
		default:
			return false
		}
	}) {
		return "", errors.New(tool.Name + " didn't answer in " + timeout.String())
	}
	return a.out, a.err
}

// Host is the host the built-ins' last plugin was started with: a test's way to run commands and read the state.
func (h *Headless) Host() plugin.Host { return h.host }

// Scale is the zoom the frames are drawn at.
func (h *Headless) Scale() float32 { return h.zoomed() }

// Plugin is the plugin started as id: a built-in, or one Activate started. A lazy one is loaded first.
func (h *Headless) Plugin(id string) (plugin.Plugin, bool) {
	for _, p := range h.plugins {
		if p.Manifest().ID != id {
			continue
		}
		if waits(p) {
			if err := h.sh.cfg.Registry.Run(context.Background(), registry.LoadCommand(id), nil); err != nil {
				return nil, false
			}
		}
		return unwrapped(p), true
	}
	return nil, false
}

// Press presses key (vim notation: "<D-J>", "j") as the window does a key on its own: the keymaps of the modes in
// force run their command, waiting for it, else it's typed at what has focus. Then it draws a frame.
func (h *Headless) Press(key string) error {
	reg := h.sh.cfg.Registry
	var err error
	if k, _ := reg.Lookup(h.sh.keyModesFor(key), keymap.Parse(key)); k != nil {
		done := false
		reg.Run(registry.OnDone(context.Background(), func(e error) { err, done = e, true }), k.Command, k.Args)
		if !done && !h.Until(15*time.Second, func() bool { return done }) {
			return errors.New(k.Command + " never finished")
		}
	} else {
		h.ui.Key(kit.KeyEvent(key))
	}
	h.Frame()
	return err
}

// HelpLint is the help lint of the last frame: each interactive node without a help id, and each help id that
// doesn't resolve to a page and anchor.
func (h *Headless) HelpLint() []string {
	var out []string
	if missing := h.ui.WithoutHelp(); len(missing) > 0 {
		out = append(out, fmt.Sprintf("%d interactive nodes have no help id", len(missing)))
	}
	p, ok := h.Plugin("help")
	v, isHelp := p.(*helpView)
	if !ok || !isHelp {
		return append(out, "no help view")
	}
	ix := v.index()
	for _, n := range h.ui.HelpNodes() {
		if _, _, ok := ix.For(n.Help); !ok {
			out = append(out, fmt.Sprintf("help id %q doesn't resolve to a page and anchor", n.Help))
		}
	}
	return out
}

// Retheme resolves ui.theme and ui.variant from the registry, as the loop does when a theme changes.
func (h *Headless) Retheme() error {
	name, _, _ := h.sh.cfg.Registry.Options().Get("ui.theme", registry.At{})
	v := theme.Dark
	if got, _, _ := h.sh.cfg.Registry.Options().Get("ui.variant", registry.At{}); got == "light" {
		v = theme.Light
	}
	srcs, _ := h.sh.cfg.Registry.Themes()
	n, _ := name.(string)
	r, err := resolveTheme(srcs, n, v)
	if err != nil {
		return err
	}
	h.ui.SetTheme(r)
	return nil
}

// Relaunching says a restart asked for Oriel to start again once the window closes.
func (h *Headless) Relaunching() bool { return h.sh.relaunch.Load() != nil }

// Relaunch starts Oriel again, as Run does once the window has closed, if a restart asked for it.
func (h *Headless) Relaunch() error {
	var err error
	h.sh.relaunchAfter(&err)
	return err
}

// Reopen does what the window's loop does when it's asked to open on another directory (a session's): it moves onto
// it underneath the plugins. It says whether it was asked.
func (h *Headless) Reopen() (bool, error) {
	var to reopenTo
	select {
	case to = <-h.sh.reopen:
	default:
		return false, nil
	}
	h.sh.move(context.Background(), to)
	return true, nil
}

// HostAs is a plugin host whose contributions are source's, as a plugin started as source has.
func (h *Headless) HostAs(source string) plugin.Host { return h.sh.host(source) }

// KeyModes are the keymap modes a key goes through now, most specific first.
func (h *Headless) KeyModes() []string { return h.sh.keyModes() }

// KeyModesFor are the keymap modes key would go through now, most specific first: a ⌃ key a terminal keeps skips
// the window's.
func (h *Headless) KeyModesFor(key string) []string { return h.sh.keyModesFor(key) }

// Drop drops files on the window, as the OS does: on the node under them that takes files, else opened in the
// editor.
func (h *Headless) Drop(ev render.DropEvent) { h.sh.dropped(ev) }

// ReopenAsked says the window has been asked to open again on another directory or session.
func (h *Headless) ReopenAsked() bool { return len(h.sh.reopen) > 0 }

// SidebarForm is the form the sidebar takes for the projects here: wide, narrow, one, or none.
func (h *Headless) SidebarForm() string {
	for _, p := range h.plugins {
		if r, ok := p.(*rail); ok {
			return r.formNow(kit.ProjectsHere(h.host))
		}
	}
	return ""
}
