//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

extern void orielWindowClosed(uintptr_t h);

static NSMutableDictionary<NSNumber *, id> *orielCloseObservers;

// orielWatchClose tells h when the view's window closes.
static void orielWatchClose(uintptr_t viewRef, uintptr_t h) {
	__weak NSView *weak = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = weak.window;
		if (win == nil) {
			return;
		}
		if (orielCloseObservers == nil) {
			orielCloseObservers = [NSMutableDictionary dictionary];
		}
		NSNumber *key = @(h);
		if (orielCloseObservers[key] != nil) {
			[[NSNotificationCenter defaultCenter] removeObserver:orielCloseObservers[key]];
		}
		orielCloseObservers[key] = [[NSNotificationCenter defaultCenter]
			addObserverForName:NSWindowWillCloseNotification object:win queue:nil
			usingBlock:^(NSNotification *n) {
				orielWindowClosed(h);
			}];
	});
}

static void orielUnwatchClose(uintptr_t h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSNumber *key = @(h);
		if (orielCloseObservers[key] != nil) {
			[[NSNotificationCenter defaultCenter] removeObserver:orielCloseObservers[key]];
			[orielCloseObservers removeObjectForKey:key];
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
)

var (
	closeWindows sync.Map // id → func()
	closeIDs     atomic.Uintptr
)

// closeWatcher calls on when AppKit closes the window, which may be long before Gio's DestroyEvent: that waits for
// the view to be freed.
type closeWatcher struct {
	on func()
	id uintptr
}

func (s *closeWatcher) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	if s.id == 0 {
		s.id = closeIDs.Add(1)
		closeWindows.Store(s.id, s.on)
	}
	C.orielWatchClose(C.uintptr_t(v.View), C.uintptr_t(s.id))
}

func (s *closeWatcher) close() {
	if s.id != 0 {
		closeWindows.Delete(s.id)
		C.orielUnwatchClose(C.uintptr_t(s.id))
	}
}
