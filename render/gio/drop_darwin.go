//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#import <objc/runtime.h>

extern void orielDropped(uintptr_t h, char *paths, double x, double y);

static char orielDropKey;

static NSArray<NSURL *> *orielDragFiles(id<NSDraggingInfo> info) {
	return [info.draggingPasteboard readObjectsForClasses:@[[NSURL class]]
		options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
}

static NSDragOperation orielDragEntered(id self, SEL _cmd, id<NSDraggingInfo> info) {
	return orielDragFiles(info).count > 0 ? NSDragOperationCopy : NSDragOperationNone;
}

// orielPerformDrag hands the files to Go with where they were let go, as fractions of the view from its top left.
static BOOL orielPerformDrag(id self, SEL _cmd, id<NSDraggingInfo> info) {
	NSArray<NSURL *> *urls = orielDragFiles(info);
	NSNumber *h = objc_getAssociatedObject(self, &orielDropKey);
	if (urls.count == 0 || h == nil) {
		return NO;
	}
	NSView *v = self;
	NSRect b = v.bounds;
	if (b.size.width <= 0 || b.size.height <= 0) {
		return NO;
	}
	NSPoint p = [v convertPoint:info.draggingLocation fromView:nil];
	double x = (p.x - b.origin.x) / b.size.width;
	double y = (p.y - b.origin.y) / b.size.height;
	if (!v.isFlipped) {
		y = 1 - y;
	}
	NSMutableArray<NSString *> *paths = [NSMutableArray array];
	for (NSURL *u in urls) {
		if (u.path != nil) {
			[paths addObject:u.path];
		}
	}
	orielDropped(h.unsignedLongValue, (char *)[[paths componentsJoinedByString:@"\n"] UTF8String], x, y);
	return YES;
}

// orielWatchDrops makes the view take files dragged from Finder, telling h of each drop.
static void orielWatchDrops(uintptr_t viewRef, uintptr_t h) {
	NSView *v = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		Class cls = [v class];
		const char *op = [[NSString stringWithFormat:@"%s@:@", @encode(NSDragOperation)] UTF8String];
		const char *ok = [[NSString stringWithFormat:@"%s@:@", @encode(BOOL)] UTF8String];
		class_addMethod(cls, @selector(draggingEntered:), (IMP)orielDragEntered, op);
		class_addMethod(cls, @selector(draggingUpdated:), (IMP)orielDragEntered, op);
		class_addMethod(cls, @selector(performDragOperation:), (IMP)orielPerformDrag, ok);
		objc_setAssociatedObject(v, &orielDropKey, @(h), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
		[v registerForDraggedTypes:@[NSPasteboardTypeFileURL]];
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
	dropWindows sync.Map // id → func(paths []string, x, y float64)
	dropIDs     atomic.Uintptr
)

// dropWatcher tells on of files dropped on the window from Finder: their paths, and where as fractions of the
// window from its top left.
type dropWatcher struct {
	on func(paths []string, x, y float64)
	id uintptr
}

func (s *dropWatcher) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	if s.id == 0 {
		s.id = dropIDs.Add(1)
		dropWindows.Store(s.id, s.on)
	}
	C.orielWatchDrops(C.uintptr_t(v.View), C.uintptr_t(s.id))
}

func (s *dropWatcher) close() {
	if s.id != 0 {
		dropWindows.Delete(s.id)
	}
}
