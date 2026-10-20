//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

static NSMutableDictionary<NSNumber *, id> *orielHoverMonitors;

// orielWatchHover hands Gio a mouse move only while the view's window is the one under the pointer. AppKit sends
// moves to the key window wherever the pointer is, so over another app's window (a non-activating panel such as the
// screenshot preview) Gio would hover, and set the cursor for, what is behind it. The first move off the window
// becomes one far outside it, so Gio sends a leave and resets the cursor; the rest are dropped.
static void orielWatchHover(uintptr_t viewRef, uintptr_t h) {
	__weak NSView *weak = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (orielHoverMonitors == nil) {
			orielHoverMonitors = [NSMutableDictionary dictionary];
		}
		NSNumber *key = @(h);
		if (orielHoverMonitors[key] != nil) {
			[NSEvent removeMonitor:orielHoverMonitors[key]];
		}
		__block BOOL away = NO;
		orielHoverMonitors[key] = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskMouseMoved
			handler:^NSEvent *(NSEvent *ev) {
				NSView *v = weak;
				NSWindow *win = v.window;
				if (win == nil || ev.window != win) {
					return ev;
				}
				NSPoint at = [win convertPointToScreen:ev.locationInWindow];
				if ([NSWindow windowNumberAtPoint:at belowWindowWithWindowNumber:0] == win.windowNumber) {
					away = NO;
					return ev;
				}
				if (away) {
					return nil;
				}
				away = YES;
				return [NSEvent mouseEventWithType:NSEventTypeMouseMoved location:NSMakePoint(-1e5, -1e5)
					modifierFlags:ev.modifierFlags timestamp:ev.timestamp windowNumber:win.windowNumber
					context:nil eventNumber:0 clickCount:0 pressure:0];
			}];
	});
}

static void orielUnwatchHover(uintptr_t h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSNumber *key = @(h);
		if (orielHoverMonitors[key] != nil) {
			[NSEvent removeMonitor:orielHoverMonitors[key]];
			[orielHoverMonitors removeObjectForKey:key];
		}
	});
}
*/
import "C"

import (
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/io/event"
)

var hoverIDs atomic.Uintptr

// hoverWatcher keeps Gio's hover, and so the cursor, to moves over the window itself.
type hoverWatcher struct {
	id uintptr
}

func (s *hoverWatcher) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	if s.id == 0 {
		s.id = hoverIDs.Add(1)
	}
	C.orielWatchHover(C.uintptr_t(v.View), C.uintptr_t(s.id))
}

func (s *hoverWatcher) close() {
	if s.id != 0 {
		C.orielUnwatchHover(C.uintptr_t(s.id))
	}
}
