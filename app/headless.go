package app

import (
	"context"
	"errors"
	"image"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
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
}

// memDesktop is a clipboard in memory, and a browser that opens nothing but notes what it was asked to.
type memDesktop struct {
	text   string
	opened []string
}

func (c *memDesktop) SetClipboard(s string)         { c.text = s }
func (c *memDesktop) ReadClipboard() (string, bool) { return c.text, c.text != "" }
func (c *memDesktop) OpenURL(u string) error {
	c.opened = append(c.opened, u)
	return nil
}

// NewHeadless starts the built-ins and cfg.Plugins over cfg, drawn in the dark oriel theme with motion reduced,
// so frames are of the settled UI.
func NewHeadless(cfg Config, o HeadlessOpts) (*Headless, error) {
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
	sh, cancel := newShell(cfg, func() {})
	sh.desktop = cfg.Desktop
	sh.launchDone = true // a window that's been up a while: mission control doesn't open as at launch
	h := &Headless{sh: sh, size: o.Size, cancel: cancel}
	last, err := sh.activate(context.Background(), &h.plugins)
	if err != nil {
		h.Close()
		return nil, err
	}
	h.host = last
	ts, err := typeset.NewBundled()
	if err != nil {
		h.Close()
		return nil, err
	}
	h.ts = ts
	h.ui = ui.New(ts, textStyle)
	h.ui.SetClipboard(cfg.Desktop)
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
		return shotOf(h.ui, sh.cfg.Registry, h.size, th)
	}
	return h, nil
}

func (h *Headless) Registry() registry.Registry { return h.sh.cfg.Registry }
func (h *Headless) Store() core.Store           { return h.sh.cfg.Store }
func (h *Headless) UI() *ui.UI                  { return h.ui }
func (h *Headless) Size() render.Point          { return h.size }

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
	forgetLanguages(h.sh.cfg.Registry)
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
	h.sh.seeNav(h.sh.cfg.Registry, h.ui.Now())
	h.list = new(render.List)
	h.ui.Frame(h.list.Record(h.zoomed()), h.size, func(c *ui.Ctx) { buildWindow(c, h.sh.cfg.Registry, h.size) })
}

// List is the last frame's display list.
func (h *Headless) List() *render.List { return h.list }

// RunQueued runs the work plugins handed the loop (Host.Do).
func (h *Headless) RunQueued() { h.sh.runTodo() }

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

// Click moves the pointer to p, presses and lets go there with mods held, a frame after each.
func (h *Headless) Click(p render.Point, mods render.Mods) {
	h.ui.Pointer(render.PointerEvent{Kind: render.PointerMove, Pos: p})
	h.Frame()
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
