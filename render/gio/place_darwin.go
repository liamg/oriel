//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#include <os/lock.h>

typedef struct { double x, y, w, h; } orielRect;

static os_unfair_lock orielLock = OS_UNFAIR_LOCK_INIT;
static orielRect orielFrame;
static int orielKnown;

// orielTop is the main screen's top in Cocoa's coordinates, which run up from its bottom left; ours run down from
// its top left.
static CGFloat orielTop(void) {
	NSArray<NSScreen *> *ss = [NSScreen screens];
	return ss.count > 0 ? NSMaxY(ss[0].frame) : 0;
}

static orielRect orielFlip(NSRect r) {
	return (orielRect){r.origin.x, orielTop() - NSMaxY(r), r.size.width, r.size.height};
}

static void orielRecord(NSWindow *win) {
	orielRect r = orielFlip(win.frame);
	os_unfair_lock_lock(&orielLock);
	orielFrame = r;
	orielKnown = 1;
	os_unfair_lock_unlock(&orielLock);
}

// orielScreens fills out with the screens' visible frames, the main screen first, and says how many.
static int orielScreens(orielRect *out, int max) {
	__block int n = 0;
	void (^fill)(void) = ^{
		for (NSScreen *s in [NSScreen screens]) {
			if (n == max) {
				break;
			}
			out[n++] = orielFlip(s.visibleFrame);
		}
	};
	if ([NSThread isMainThread]) {
		fill();
	} else {
		dispatch_sync(dispatch_get_main_queue(), fill);
	}
	return n;
}

// orielPlace moves the view's window to r and keeps track of its frame from then on.
static void orielPlace(uintptr_t viewRef, orielRect r) {
	NSView *view = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = view.window;
		if (win == nil) {
			return;
		}
		[win setFrame:NSMakeRect(r.x, orielTop() - r.y - r.h, r.w, r.h) display:YES];
		orielRecord(win);
		__weak NSWindow *weak = win;
		void (^track)(NSNotification *) = ^(NSNotification *note) {
			NSWindow *w = weak;
			if (w != nil) {
				orielRecord(w);
			}
		};
		NSNotificationCenter *nc = [NSNotificationCenter defaultCenter];
		[nc addObserverForName:NSWindowDidMoveNotification object:win queue:nil usingBlock:track];
		[nc addObserverForName:NSWindowDidResizeNotification object:win queue:nil usingBlock:track];
	});
}

static int orielCurrent(orielRect *out) {
	os_unfair_lock_lock(&orielLock);
	*out = orielFrame;
	int known = orielKnown;
	os_unfair_lock_unlock(&orielLock);
	return known;
}
*/
import "C"

import (
	"gioui.org/app"
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

// appKitPlacer places a window with AppKit, in points, which are Gio's Dp on macOS. It reads the screens when it's
// made, off a frame: during one, AppKit's main thread waits on the window's loop.
type appKitPlacer struct {
	v       uintptr
	screens []render.Rect
}

func newPlacer() placer { return &appKitPlacer{screens: screens()} }

func (p *appKitPlacer) view(ev event.Event) bool {
	if v, ok := ev.(app.AppKitViewEvent); ok && v.View != 0 {
		p.v = v.View
		return true
	}
	return false
}

func screens() []render.Rect {
	var out [16]C.orielRect
	n := int(C.orielScreens(&out[0], C.int(len(out))))
	rs := make([]render.Rect, n)
	for i := range n {
		rs[i] = fromC(out[i])
	}
	return rs
}

func (p *appKitPlacer) place(saved render.Rect, size render.Point, _ float32) {
	if p.v != 0 {
		r := render.PlaceWindow(p.screens, saved, size)
		C.orielPlace(C.uintptr_t(p.v), C.orielRect{x: C.double(r.Min.X), y: C.double(r.Min.Y), w: C.double(r.Dx()), h: C.double(r.Dy())})
	}
}

func (p *appKitPlacer) frame(float32) (render.Rect, bool) {
	var r C.orielRect
	if C.orielCurrent(&r) == 0 {
		return render.Rect{}, false
	}
	return fromC(r), true
}

func (p *appKitPlacer) close() {}

func fromC(r C.orielRect) render.Rect {
	return render.XYWH(float32(r.x), float32(r.y), float32(r.w), float32(r.h))
}
