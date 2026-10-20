//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

extern void orielSideButton(uintptr_t h, int button, unsigned long flags);

static NSMutableDictionary<NSNumber *, id> *orielSideMonitors;

// orielWatchSide takes the side buttons (3 and 4) in the view's window before Gio, which drops them, and hands each
// press to h.
static void orielWatchSide(uintptr_t viewRef, uintptr_t h) {
	__weak NSView *weak = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (orielSideMonitors == nil) {
			orielSideMonitors = [NSMutableDictionary dictionary];
		}
		NSNumber *key = @(h);
		if (orielSideMonitors[key] != nil) {
			[NSEvent removeMonitor:orielSideMonitors[key]];
		}
		orielSideMonitors[key] = [NSEvent addLocalMonitorForEventsMatchingMask:NSEventMaskOtherMouseDown | NSEventMaskOtherMouseUp
			handler:^NSEvent *(NSEvent *ev) {
				NSView *v = weak;
				if (v == nil || ev.window != v.window || (ev.buttonNumber != 3 && ev.buttonNumber != 4)) {
					return ev;
				}
				if (ev.type == NSEventTypeOtherMouseDown) {
					orielSideButton(h, (int)ev.buttonNumber, (unsigned long)ev.modifierFlags);
				}
				return nil;
			}];
	});
}

static void orielUnwatchSide(uintptr_t h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSNumber *key = @(h);
		if (orielSideMonitors[key] != nil) {
			[NSEvent removeMonitor:orielSideMonitors[key]];
			[orielSideMonitors removeObjectForKey:key];
		}
	});
}
*/
import "C"

import (
	"sync"
	"sync/atomic"

	"gioui.org/app"
	"gioui.org/io/event"

	"github.com/liamg/oriel/render"
)

var (
	sideWindows sync.Map // id → func(render.Event)
	sideIDs     atomic.Uintptr
)

// sideWatcher hands on the side buttons, which Gio's macOS backend drops, as their keys.
type sideWatcher struct {
	on func(render.Event)
	id uintptr
}

func (s *sideWatcher) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	if s.id == 0 {
		s.id = sideIDs.Add(1)
		sideWindows.Store(s.id, s.on)
	}
	C.orielWatchSide(C.uintptr_t(v.View), C.uintptr_t(s.id))
}

func (s *sideWatcher) close() {
	if s.id != 0 {
		sideWindows.Delete(s.id)
		C.orielUnwatchSide(C.uintptr_t(s.id))
	}
}
