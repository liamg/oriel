//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

extern void orielOcclusion(uintptr_t h, int occluded);

static NSMutableDictionary<NSNumber *, id> *orielOcclusionObservers;

// orielWatchOcclusion tells h whether the view's window is wholly covered (or minimised, or on another Space), now
// and on every change.
static void orielWatchOcclusion(uintptr_t viewRef, uintptr_t h) {
	__weak NSView *weak = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		NSWindow *win = weak.window;
		if (win == nil) {
			return;
		}
		if (orielOcclusionObservers == nil) {
			orielOcclusionObservers = [NSMutableDictionary dictionary];
		}
		NSNumber *key = @(h);
		if (orielOcclusionObservers[key] != nil) {
			[[NSNotificationCenter defaultCenter] removeObserver:orielOcclusionObservers[key]];
		}
		orielOcclusionObservers[key] = [[NSNotificationCenter defaultCenter]
			addObserverForName:NSWindowDidChangeOcclusionStateNotification object:win queue:nil
			usingBlock:^(NSNotification *n) {
				NSWindow *w = n.object;
				orielOcclusion(h, (w.occlusionState & NSWindowOcclusionStateVisible) == 0);
			}];
		orielOcclusion(h, (win.occlusionState & NSWindowOcclusionStateVisible) == 0);
	});
}

static void orielUnwatchOcclusion(uintptr_t h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		NSNumber *key = @(h);
		if (orielOcclusionObservers[key] != nil) {
			[[NSNotificationCenter defaultCenter] removeObserver:orielOcclusionObservers[key]];
			[orielOcclusionObservers removeObjectForKey:key];
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
	occlusionWindows sync.Map // id → func(occluded bool)
	occlusionIDs     atomic.Uintptr
)

// occlusionWatcher tells on when AppKit says the window is wholly covered, or seen again.
type occlusionWatcher struct {
	on func(occluded bool)
	id uintptr
}

func (s *occlusionWatcher) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	if s.id == 0 {
		s.id = occlusionIDs.Add(1)
		occlusionWindows.Store(s.id, s.on)
	}
	C.orielWatchOcclusion(C.uintptr_t(v.View), C.uintptr_t(s.id))
}

func (s *occlusionWatcher) close() {
	if s.id != 0 {
		occlusionWindows.Delete(s.id)
		C.orielUnwatchOcclusion(C.uintptr_t(s.id))
	}
}
