//go:build ((linux && !android) || freebsd || openbsd) && !nox11

package gio

/*
#cgo freebsd openbsd CFLAGS: -I/usr/X11R6/include -I/usr/local/include
#cgo freebsd openbsd LDFLAGS: -L/usr/X11R6/lib -L/usr/local/lib -lX11
#cgo linux pkg-config: x11

#include <stdlib.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/Xutil.h>

static Display *orielDpy;
static XErrorHandler orielPrev;

// orielOnError drops errors on our own connection, such as asking after a window that's just gone, which would
// otherwise exit; it passes the rest on.
static int orielOnError(Display *d, XErrorEvent *e) {
	if (d == orielDpy) {
		return 0;
	}
	return orielPrev ? orielPrev(d, e) : 0;
}

static int orielOpen(void) {
	if (orielDpy == NULL) {
		orielDpy = XOpenDisplay(NULL);
		if (orielDpy != NULL) {
			orielPrev = XSetErrorHandler(orielOnError);
		}
	}
	return orielDpy != NULL;
}

// orielCardinals reads up to n CARDINALs of a window's property; it says how many it read.
static int orielCardinals(Window w, const char *name, long *out, int n) {
	Atom a = XInternAtom(orielDpy, name, True);
	if (a == None) {
		return 0;
	}
	Atom type;
	int format;
	unsigned long got, after;
	unsigned char *data = NULL;
	if (XGetWindowProperty(orielDpy, w, a, 0, n, False, XA_CARDINAL, &type, &format, &got, &after, &data) != Success || data == NULL) {
		return 0;
	}
	int k = 0;
	if (format == 32) {
		for (; k < n && k < (int)got; k++) {
			out[k] = ((long *)data)[k];
		}
	}
	XFree(data);
	return k;
}

// orielWorkArea is the desktop less its panels (EWMH's _NET_WORKAREA), else the whole root window, less the
// window manager's frame around win (_NET_FRAME_EXTENTS: left, right, top, bottom), so the frame fits too.
static void orielWorkArea(Window win, long *x, long *y, long *w, long *h) {
	Window root = DefaultRootWindow(orielDpy);
	long v[4];
	if (orielCardinals(root, "_NET_WORKAREA", v, 4) == 4) {
		*x = v[0], *y = v[1], *w = v[2], *h = v[3];
	} else {
		XWindowAttributes at;
		XGetWindowAttributes(orielDpy, root, &at);
		*x = 0, *y = 0, *w = at.width, *h = at.height;
	}
	long e[4];
	if (orielCardinals(win, "_NET_FRAME_EXTENTS", e, 4) == 4) {
		*x += e[0], *y += e[2], *w -= e[0] + e[1], *h -= e[2] + e[3];
	}
}

// orielMove puts win's own area, inside the window manager's frame, at x, y, sized w by h.
static void orielMove(Window win, int x, int y, unsigned w, unsigned h) {
	XSizeHints *hints = XAllocSizeHints();
	long supplied;
	XGetWMNormalHints(orielDpy, win, hints, &supplied);
	hints->flags |= USPosition | PWinGravity;
	hints->win_gravity = StaticGravity; // x, y place win itself, not the frame around it
	XSetWMNormalHints(orielDpy, win, hints);
	XFree(hints);
	XMoveResizeWindow(orielDpy, win, x, y, w, h);
	XFlush(orielDpy);
}

static int orielFrame(Window win, int *x, int *y, unsigned *w, unsigned *h) {
	Window root, child;
	int gx, gy;
	unsigned border, depth;
	if (!XGetGeometry(orielDpy, win, &root, &gx, &gy, w, h, &border, &depth)) {
		return 0;
	}
	return XTranslateCoordinates(orielDpy, win, root, 0, 0, x, y, &child);
}
*/
import "C"

import (
	"sync"
	"time"

	"gioui.org/app"
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// x11Placer places a window over its own connection to the X server, in pixels: Dp times the window's scale. A move
// doesn't redraw, so it looks where the window is every second.
type x11Placer struct {
	mu    sync.Mutex
	win   C.Window
	scale float32
	at    render.Rect
	known bool
	stop  chan struct{}
}

func newPlacer() placer { return &x11Placer{} }

func (p *x11Placer) view(ev event.Event) bool {
	v, ok := ev.(app.X11ViewEvent)
	if !ok || v.Window == 0 {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if C.orielOpen() == 0 {
		return false
	}
	p.win = C.Window(v.Window)
	return true
}

func (p *x11Placer) place(saved render.Rect, size render.Point, scale float32) {
	p.mu.Lock()
	if p.win == 0 {
		p.mu.Unlock()
		return
	}
	p.scale = scale
	var x, y, w, h C.long
	C.orielWorkArea(p.win, &x, &y, &w, &h)
	work := render.XYWH(float32(x)/scale, float32(y)/scale, float32(w)/scale, float32(h)/scale)
	r := render.PlaceWindow([]render.Rect{work}, saved, size)
	C.orielMove(p.win, C.int(r.Min.X*scale), C.int(r.Min.Y*scale), C.uint(r.Dx()*scale), C.uint(r.Dy()*scale))
	p.at, p.known = r, true
	p.stop = make(chan struct{})
	p.mu.Unlock()
	go p.watch(p.stop)
}

func (p *x11Placer) watch(stop chan struct{}) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			p.look()
		}
	}
}

func (p *x11Placer) look() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.win == 0 || p.scale == 0 {
		return
	}
	var x, y C.int
	var w, h C.uint
	if C.orielFrame(p.win, &x, &y, &w, &h) != 0 && w > 0 && h > 0 {
		s := p.scale
		p.at, p.known = render.XYWH(float32(x)/s, float32(y)/s, float32(w)/s, float32(h)/s), true
	}
}

func (p *x11Placer) frame(scale float32) (render.Rect, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if scale > 0 {
		p.scale = scale
	}
	return p.at, p.known
}

// close stops looking: the window's going.
func (p *x11Placer) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop != nil {
		close(p.stop)
		p.stop = nil
	}
	p.win = 0
}
