// Package headless is a render backend that records each frame as a display list, for tests.
package headless

import (
	"context"
	"image"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/typeset"
)

type Backend struct {
	windows chan *Window
	shaper  typeset.Shaper
}

// New returns a backend with a fixed-pitch stand-in shaper: fast and font-free, for layout and logic tests.
func New() *Backend { return NewWith(Shaper{}) }

// NewWith returns a backend that shapes with sh, such as typeset.NewBundled(), so Shot draws real text.
func NewWith(sh typeset.Shaper) *Backend {
	return &Backend{windows: make(chan *Window, 16), shaper: sh}
}

// Windows yields each window as it opens.
func (b *Backend) Windows() <-chan *Window { return b.windows }

func (b *Backend) Shaper() typeset.Shaper { return b.shaper }

func (b *Backend) NewWindow(o render.WindowOpts) (render.Window, error) {
	w := &Window{
		size:   render.Point{X: o.Width, Y: o.Height},
		events: make(chan render.Event, 64),
		done:   make(chan struct{}),
	}
	if w.size == (render.Point{}) {
		w.size = render.Point{X: 1280, Y: 800}
	}
	w.cond = sync.NewCond(&w.mu)
	b.windows <- w
	return w, nil
}

// Frame is one recorded frame.
type Frame struct {
	render.List
}

// Shot draws the frame on the CPU at the window's size and scale, over bg.
func (w *Window) Shot(f Frame, bg render.Color) *image.RGBA {
	return raster.Render(&f.List, w.Size(), w.Scale(), bg)
}

// Text is every glyph run's text, one run per line.
func (f Frame) Text() string {
	var lines []string
	for _, op := range f.Ops {
		if op.Kind == render.OpGlyphs {
			lines = append(lines, op.Run.Text)
		}
	}
	return strings.Join(lines, "\n")
}

type Window struct {
	mu     sync.Mutex
	cond   *sync.Cond
	frames []Frame
	size   render.Point
	events chan render.Event
	done   chan struct{}
	once   sync.Once
	sendMu sync.RWMutex
	closed bool
	cursor render.Cursor

	access  map[uint64]*render.AccessNode
	actions chan render.AccessAction
	clip    string
}

var (
	_ render.Accessible = (*Window)(nil)
	_ render.Clipboard  = (*Window)(nil)
)

func (w *Window) SetClipboard(text string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.clip = text
}

func (w *Window) ReadClipboard() (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clip, w.clip != ""
}

// Clipboard is what was last put on the clipboard.
func (w *Window) Clipboard() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.clip
}

// UpdateAccess applies an accessibility update, as a platform bridge would.
func (w *Window) UpdateAccess(u render.AccessUpdate) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.access == nil {
		w.access = map[uint64]*render.AccessNode{}
	}
	for _, n := range u.Changed {
		w.access[n.ID] = n
	}
	for _, id := range u.Removed {
		delete(w.access, id)
	}
}

func (w *Window) AccessActions() <-chan render.AccessAction {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.actions == nil {
		w.actions = make(chan render.AccessAction, 16)
	}
	return w.actions
}

// Access is the accessibility tree the updates have built, or nil before the first.
func (w *Window) Access() *render.AccessNode {
	w.mu.Lock()
	defer w.mu.Unlock()
	var build func(id uint64) *render.AccessNode
	build = func(id uint64) *render.AccessNode {
		n, ok := w.access[id]
		if !ok {
			return nil
		}
		out := *n
		out.Children = nil
		for _, k := range n.Children {
			if c := build(k.ID); c != nil {
				out.Children = append(out.Children, c)
			}
		}
		return &out
	}
	return build(0)
}

// Act asks the app to do something, as a screen reader would.
func (w *Window) Act(a render.AccessAction) { w.AccessActions(); w.actions <- a }

func (w *Window) SetCursor(c render.Cursor) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.cursor = c
}

// Cursor is the last cursor the app set.
func (w *Window) Cursor() render.Cursor {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cursor
}

func (w *Window) Frame(fn func(render.Frame)) error {
	var l render.List
	fn(l.Record(1))
	w.mu.Lock()
	w.frames = append(w.frames, Frame{l})
	w.mu.Unlock()
	w.cond.Broadcast()
	return nil
}

func (w *Window) Events() <-chan render.Event { return w.events }
func (w *Window) Scale() float32              { return 1 }

func (w *Window) Size() render.Point {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

// Send delivers an event to the app, as the OS would. It does nothing once the window is closed.
func (w *Window) Send(ev render.Event) {
	if rs, ok := ev.(render.ResizeEvent); ok {
		w.mu.Lock()
		w.size = rs.Size
		w.mu.Unlock()
	}
	w.sendMu.RLock()
	defer w.sendMu.RUnlock()
	if w.closed {
		return
	}
	select {
	case <-w.done:
	case w.events <- ev:
	}
}

// Close closes the window, as the user would; the app sees its event channel close.
func (w *Window) Close() {
	w.once.Do(func() {
		close(w.done)
		w.sendMu.Lock()
		defer w.sendMu.Unlock()
		w.closed = true
		close(w.events)
	})
}

// Frames returns every frame drawn so far.
func (w *Window) Frames() []Frame {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Frame(nil), w.frames...)
}

// WaitFrame waits for a frame that satisfies ok, checking every frame drawn so far first.
// On timeout it returns the last frame drawn, if any, and the context's error.
func (w *Window) WaitFrame(ctx context.Context, ok func(Frame) bool) (Frame, error) {
	stop := context.AfterFunc(ctx, func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.cond.Broadcast()
	})
	defer stop()
	w.mu.Lock()
	defer w.mu.Unlock()
	seen := 0
	for {
		for ; seen < len(w.frames); seen++ {
			if ok(w.frames[seen]) {
				return w.frames[seen], nil
			}
		}
		if err := ctx.Err(); err != nil {
			var last Frame
			if len(w.frames) > 0 {
				last = w.frames[len(w.frames)-1]
			}
			return last, err
		}
		w.cond.Wait()
	}
}

// Shaper is a fixed-pitch shaper: one glyph per rune, each the rune's code point.
type Shaper struct{}

func (Shaper) Shape(text string, f typeset.FontSpec) *typeset.Run {
	adv := f.Size * 0.6
	run := &typeset.Run{Text: text, Font: f, Ascent: f.Size * 1.1, Descent: f.Size * 0.3, Glyphs: make([]typeset.Glyph, 0, utf8.RuneCountInString(text))}
	for _, r := range text {
		run.Glyphs = append(run.Glyphs, typeset.Glyph{ID: typeset.GlyphID(r), X: run.Advance})
		run.Advance += adv
	}
	return run
}
