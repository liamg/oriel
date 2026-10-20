package gio

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/io/clipboard"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/io/system"
	"gioui.org/io/transfer"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/unit"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
)

// Main runs the platform's event loop. It must be called from the main goroutine, and doesn't return:
// run the app on another goroutine and os.Exit when it's done.
func Main() { app.Main() }

// Backend opens Gio windows that share one typesetter.
type Backend struct {
	ts      *typeset.Typesetter
	mu      sync.Mutex
	windows []*Window
}

var _ render.Backend = (*Backend)(nil)

func New() (*Backend, error) {
	b := &Backend{}
	ts, err := typeset.New(b.invalidate)
	if err != nil {
		return nil, err
	}
	b.ts = ts
	return b, nil
}

func (b *Backend) Shaper() typeset.Shaper { return b.ts }

// invalidate redraws every window, when a fallback font lands.
func (b *Backend) invalidate() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, w := range b.windows {
		w.redraw.Store(true)
		w.win.Invalidate()
	}
}

func (b *Backend) NewWindow(o render.WindowOpts) (render.Window, error) {
	w := &Window{win: new(app.Window), events: make(chan render.Event), queue: make(chan render.Event, 1024), opts: o, pl: newPlacer()}
	w.scale.Store(float32(1))
	w.dpi.Store(float32(1))
	w.zoom.Store(float32(1))
	opts := []app.Option{app.Title(o.Title)}
	if f := o.Frame; f.Dx() > 0 && f.Dy() > 0 {
		opts = append(opts, app.Size(unit.Dp(f.Dx()), unit.Dp(f.Dy())))
	} else if o.Width > 0 && o.Height > 0 {
		opts = append(opts, app.Size(unit.Dp(o.Width), unit.Dp(o.Height)))
	}
	w.win.Option(opts...)
	b.mu.Lock()
	b.windows = append(b.windows, w)
	b.mu.Unlock()
	go func() {
		w.loop()
		b.mu.Lock()
		defer b.mu.Unlock()
		for i, x := range b.windows {
			if x == w {
				b.windows = append(b.windows[:i], b.windows[i+1:]...)
				break
			}
		}
	}()
	return w, nil
}

// Window is a Gio window. The app records frames on its own goroutine; the window's event loop
// replays the latest one whenever Gio asks for a frame.
type Window struct {
	opts   render.WindowOpts
	pl     placer
	win    *app.Window
	events chan render.Event // to the app, in order
	queue  chan render.Event // from the event loop, which never blocks on the app
	latest atomic.Pointer[render.List]
	handed atomic.Int64 // when latest was handed over, in unix nanoseconds
	redraw atomic.Bool
	scale  atomic.Value // float32: the display's density times the zoom
	dpi    atomic.Value // float32: the display's density
	zoom   atomic.Value // float32
	size   atomic.Value // render.Point
	cursor atomic.Uint32
	clip   atomic.Pointer[string] // text waiting to go on the clipboard
	read   atomic.Pointer[string] // what the clipboard held when last read
	ask    atomic.Bool            // read the clipboard on the next frame
	held   atomic.Bool            // a request to close goes to the app
}

func (w *Window) ReadClipboard() (string, bool) {
	if s := w.read.Load(); s != nil {
		return *s, *s != ""
	}
	return "", false
}

var _ render.Clipboard = (*Window)(nil)

// SetClipboard puts text on the system clipboard on the next frame.
func (w *Window) SetClipboard(text string) {
	w.clip.Store(&text)
	w.read.Store(&text)
	w.win.Invalidate()
}

var cursors = map[render.Cursor]pointer.Cursor{
	render.CursorDefault: pointer.CursorDefault, render.CursorPointer: pointer.CursorPointer, render.CursorText: pointer.CursorText,
	render.CursorNotAllowed: pointer.CursorNotAllowed, render.CursorGrab: pointer.CursorGrab, render.CursorResizeCol: pointer.CursorColResize,
	render.CursorResizeRow: pointer.CursorRowResize, render.CursorCrosshair: pointer.CursorCrosshair,
	render.CursorGrabbing: pointer.CursorGrabbing, render.CursorResizeNWSE: pointer.CursorNorthWestSouthEastResize,
	render.CursorResizeNESW: pointer.CursorNorthEastSouthWestResize,
}

func (w *Window) SetCursor(c render.Cursor) {
	if render.Cursor(w.cursor.Swap(uint32(c))) != c {
		w.win.Invalidate()
	}
}

func (w *Window) Scale() float32 { return w.scale.Load().(float32) }

var _ render.Zoomer = (*Window)(nil)

// SetZoom multiplies the display's density by z from the next frame, which tells the app its new size.
func (w *Window) SetZoom(z float32) {
	if z > 0 && w.zoom.Swap(z).(float32) != z {
		w.win.Invalidate()
	}
}

var _ render.Placed = (*Window)(nil)

// Placement is where the window is; where the windowing system won't say, as Wayland's won't, it's only its size.
func (w *Window) Placement() (render.Rect, bool) {
	if r, ok := w.pl.frame(w.dpi.Load().(float32)); ok {
		return r, true
	}
	s := w.Size()
	return render.Rect{Max: s}, s.X > 0 && s.Y > 0
}

func (w *Window) Size() render.Point {
	s, _ := w.size.Load().(render.Point)
	return s
}

func (w *Window) Events() <-chan render.Event { return w.events }

var _ render.CloseHolder = (*Window)(nil)

func (w *Window) HoldClose(hold bool) {
	w.held.Store(hold)
	holdQuit(hold)
}

func (w *Window) Close() {
	w.held.Store(false)
	holdQuit(false)
	w.win.Perform(system.ActionClose)
}

func (w *Window) Frame(fn func(render.Frame)) error {
	l := new(render.List)
	fn(l.Record(w.Scale()))
	if old := w.latest.Load(); old != nil && l.Same(old) {
		return nil // nothing changed: no need to draw it again
	}
	w.latest.Store(l)
	w.handed.Store(time.Now().UnixNano())
	w.win.Invalidate()
	return nil
}

// A frame is slow when it waits past slowWait for the window to start on it, or takes past slowDraw to draw and present.
const (
	slowWait = 100 * time.Millisecond
	slowDraw = 100 * time.Millisecond
)

// post queues ev for the app. It drops ev only if the app is 1024 events behind.
func (w *Window) post(ev render.Event) {
	select {
	case w.queue <- ev:
	default:
	}
}

func (w *Window) forward() {
	defer close(w.events)
	for ev := range w.queue {
		w.events <- ev
	}
}

func (w *Window) loop() {
	go w.forward()
	var mu sync.Mutex
	closed := false
	defer func() {
		mu.Lock()
		closed = true
		close(w.queue)
		mu.Unlock()
	}()
	stop := watchAppearance(func(dark bool) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			w.post(render.AppearanceEvent{Dark: dark})
		}
	})
	defer stop()
	side := &sideWatcher{on: func(ev render.Event) {
		mu.Lock()
		defer mu.Unlock()
		if !closed {
			w.post(ev)
		}
	}}
	defer side.close()
	var ops op.Ops
	f := NewFrame()
	tag := new(int)
	var held pointer.Buttons
	var clock inputClock
	var shown *render.List
	var slowSent time.Time
	var down render.Mods // the modifier keys held
	viewed, placed := false, false
	defer w.pl.close()
	for {
		switch e := w.win.Event().(type) {
		case app.DestroyEvent:
			return
		case *app.ClosingEvent:
			if w.held.Load() {
				e.Abort()
				w.post(render.CloseRequestEvent{})
			}
		case app.FrameEvent:
			gtx := app.NewContext(&ops, e)
			dpi := gtx.Metric.PxPerDp
			if viewed && !placed {
				w.pl.place(w.opts.Frame, render.Point{X: w.opts.Width, Y: w.opts.Height}, dpi)
				placed = true
			}
			w.dpi.Store(dpi)
			scale := dpi * w.zoom.Load().(float32)
			size := render.Point{X: float32(e.Size.X) / scale, Y: float32(e.Size.Y) / scale}
			if scale != w.Scale() || size != w.Size() {
				w.scale.Store(scale)
				w.size.Store(size)
				w.post(render.ResizeEvent{Size: size})
			}
			area := clip.Rect{Max: e.Size}.Push(gtx.Ops)
			event.Op(gtx.Ops, tag)
			cursors[render.Cursor(w.cursor.Load())].Add(gtx.Ops)
			area.Pop()
			gtx.Execute(key.FocusCmd{Tag: tag})
			if t := w.clip.Swap(nil); t != nil {
				gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(*t))})
			}
			if w.ask.Swap(false) {
				gtx.Execute(clipboard.ReadCmd{Tag: tag})
			}
			for {
				ev, ok := gtx.Event(
					transfer.TargetFilter{Target: tag, Type: "application/text"},
					key.FocusFilter{Target: tag},
					key.Filter{Focus: tag, Optional: key.ModShift | key.ModCtrl | key.ModAlt | key.ModSuper | key.ModCommand},
					// Tab and ⇧Tab are gio's focus moves unless asked for by name; the app moves focus itself.
					key.Filter{Focus: tag, Name: key.NameTab, Optional: key.ModShift | key.ModCtrl | key.ModAlt | key.ModSuper | key.ModCommand},
					pointer.Filter{Target: tag, Kinds: pointer.Move | pointer.Drag | pointer.Press | pointer.Release | pointer.Scroll | pointer.Leave,
						ScrollX: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}, ScrollY: pointer.ScrollRange{Min: -1 << 20, Max: 1 << 20}},
				)
				if !ok {
					break
				}
				switch ev := ev.(type) {
				case transfer.DataEvent:
					if r := ev.Open(); r != nil {
						if b, err := io.ReadAll(io.LimitReader(r, 16<<20)); err == nil {
							s := string(b)
							w.read.Store(&s)
						}
						r.Close()
					}
					continue
				case key.Event, key.FocusEvent:
					w.ask.Store(true)
				}
				for _, k := range translate(ev, scale, &held, &down) {
					if pe, ok := k.(render.PointerEvent); ok {
						pe.At = clock.at(ev.(pointer.Event).Time)
						k = pe
					}
					w.post(k)
				}
			}
			if w.redraw.Swap(false) {
				w.post(render.RedrawEvent{})
			}
			start := time.Now()
			var waited time.Duration
			f.Begin(gtx.Ops, scale)
			if l := w.latest.Load(); l != nil {
				if l != shown {
					shown, waited = l, start.Sub(time.Unix(0, w.handed.Load()))
				}
				l.Replay(f)
			}
			f.End()
			e.Frame(gtx.Ops)
			if took := time.Since(start); (waited > slowWait || took > slowDraw) && time.Since(slowSent) > time.Second {
				slowSent = time.Now()
				w.post(render.SlowFrameEvent{Waited: waited, Took: took})
			}
		default:
			side.view(e)
			if w.pl.view(e) {
				viewed = true
			}
		}
	}
}

// inputClock turns the OS's input timestamps, from an epoch of its own, into wall time. The epoch is the earliest any
// event puts it at: the event that arrived soonest after it happened reads as arriving at once.
type inputClock struct {
	epoch time.Time
}

func (c *inputClock) at(t time.Duration) time.Time {
	if t == 0 {
		return time.Time{}
	}
	if e := time.Now().Add(-t); c.epoch.IsZero() || e.Before(c.epoch) {
		c.epoch = e
	}
	return c.epoch.Add(t)
}
