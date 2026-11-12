//go:build darwin && !ios && cgo

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#import <objc/runtime.h>
#include <stdlib.h>
#include <string.h>
#include "access_darwin.h"

extern int orielAXInfoOf(uintptr_t h, uint64_t id, int text, orielAXInfo *out);
extern uintptr_t *orielAXKids(uintptr_t h, uint64_t id, int *n);
extern uintptr_t orielAXElementOf(uintptr_t h, uint64_t id);
extern uintptr_t orielAXHit(uintptr_t h, double x, double y);
extern uintptr_t orielAXFocusedElement(uintptr_t h);
extern int orielAXDo(uintptr_t h, uint64_t id, int action, char *text, long loc, long len);
extern char *orielAXSubstring(uintptr_t h, uint64_t id, long loc, long len);

static const void *orielAXKey = &orielAXKey;

// orielAXViews is each bridge's view, held weakly and only touched on the main thread, so a block queued for a view
// AppKit has since freed finds nil.
static NSMapTable<NSNumber *, NSView *> *orielAXViews;

static NSString *orielTake(char *s) {
	if (s == NULL) {
		return nil;
	}
	NSString *out = [NSString stringWithUTF8String:s];
	free(s);
	return out;
}

static id orielAXObject(uintptr_t el, id fallback) {
	return el != 0 ? (__bridge id)(void *)el : fallback;
}

static NSArray *orielAXChildren(uintptr_t h, uint64_t node) {
	int n = 0;
	uintptr_t *els = orielAXKids(h, node, &n);
	NSMutableArray *out = [NSMutableArray arrayWithCapacity:n];
	for (int i = 0; i < n; i++) {
		[out addObject:(__bridge id)(void *)els[i]];
	}
	free(els);
	return out;
}

static NSAccessibilityRole orielRole(int r) {
	switch (r) {
	case orielAXToolbar: return NSAccessibilityToolbarRole;
	case orielAXStaticText: return NSAccessibilityStaticTextRole;
	case orielAXButton: return NSAccessibilityButtonRole;
	case orielAXTextField: return NSAccessibilityTextFieldRole;
	case orielAXList: return NSAccessibilityListRole;
	case orielAXRow: return NSAccessibilityRowRole;
	case orielAXOutline: return NSAccessibilityOutlineRole;
	case orielAXTabGroup: return NSAccessibilityTabGroupRole;
	case orielAXRadioButton: return NSAccessibilityRadioButtonRole;
	case orielAXMenu: return NSAccessibilityMenuRole;
	case orielAXMenuItem: return NSAccessibilityMenuItemRole;
	case orielAXSlider: return NSAccessibilitySliderRole;
	case orielAXImage: return NSAccessibilityImageRole;
	case orielAXLink: return NSAccessibilityLinkRole;
	case orielAXCheckBox: return NSAccessibilityCheckBoxRole;
	}
	return NSAccessibilityGroupRole;
}

static NSAccessibilitySubrole orielSubrole(int s) {
	switch (s) {
	case orielAXTabButton: return NSAccessibilityTabButtonSubrole;
	case orielAXDialog: return NSAccessibilityDialogSubrole;
	}
	return nil;
}

// orielAXScreenRect is a rect in points from the view's top left, on the screen.
static NSRect orielAXScreenRect(NSView *v, orielAXInfo *i) {
	if (v == nil || v.window == nil) {
		return NSZeroRect;
	}
	NSRect r = NSMakeRect(i->x, v.isFlipped ? i->y : NSHeight(v.bounds) - i->y - i->h, i->w, i->h);
	return NSAccessibilityFrameInView(v, r);
}

// orielAXViewPoint is a point on the screen, in points from the view's top left.
static NSPoint orielAXViewPoint(NSView *v, NSPoint p) {
	NSRect r = [v.window convertRectFromScreen:NSMakeRect(p.x, p.y, 0, 0)];
	NSPoint q = [v convertPoint:r.origin fromView:nil];
	if (!v.isFlipped) {
		q.y = NSHeight(v.bounds) - q.y;
	}
	return q;
}

static uintptr_t orielViewHandle(id v) {
	NSNumber *n = objc_getAssociatedObject(v, orielAXKey);
	return (uintptr_t)n.unsignedLongLongValue;
}

@interface OrielAXElement : NSAccessibilityElement
@property (nonatomic) uintptr_t bridge;
@property (nonatomic) uint64_t node;
@property (nonatomic, weak) NSView *host;
@end

@implementation OrielAXElement

- (BOOL)orielInfo:(orielAXInfo *)i text:(BOOL)text {
	memset(i, 0, sizeof *i);
	return orielAXInfoOf(self.bridge, self.node, text ? 1 : 0, i) != 0;
}

- (BOOL)isAccessibilityElement {
	return YES;
}

- (NSAccessibilityRole)accessibilityRole {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] ? orielRole(i.role) : NSAccessibilityUnknownRole;
}

- (NSAccessibilitySubrole)accessibilitySubrole {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] ? orielSubrole(i.subrole) : nil;
}

- (NSString *)accessibilityLabel {
	orielAXInfo i;
	if (![self orielInfo:&i text:YES]) {
		return nil;
	}
	free(i.value);
	NSString *s = orielTake(i.label);
	return s.length > 0 ? s : nil;
}

- (id)accessibilityValue {
	orielAXInfo i;
	if (![self orielInfo:&i text:YES]) {
		return nil;
	}
	free(i.label);
	NSString *s = orielTake(i.value);
	switch (i.valueKind) {
	case 1:
		return s != nil ? s : @"";
	case 2:
		return @(i.number);
	}
	return nil;
}

- (NSRect)accessibilityFrame {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] ? orielAXScreenRect(self.host, &i) : NSZeroRect;
}

- (id)accessibilityParent {
	orielAXInfo i;
	if (![self orielInfo:&i text:NO] || i.parent == 0) {
		return self.host;
	}
	return orielAXObject(orielAXElementOf(self.bridge, i.parent), self.host);
}

- (NSArray *)accessibilityChildren {
	return orielAXChildren(self.bridge, self.node);
}

- (id)accessibilityWindow {
	return self.host.window;
}

- (id)accessibilityTopLevelUIElement {
	return self.host.window;
}

- (id)accessibilityHitTest:(NSPoint)p {
	NSView *v = self.host;
	return v != nil ? [v accessibilityHitTest:p] : self;
}

- (BOOL)isAccessibilityEnabled {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] && i.enabled;
}

- (BOOL)isAccessibilityFocused {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] && i.focused;
}

- (BOOL)isAccessibilitySelected {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] && i.selected;
}

- (BOOL)isAccessibilityExpanded {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] && i.expanded;
}

- (BOOL)isAccessibilityDisclosed {
	return [self isAccessibilityExpanded];
}

- (BOOL)isAccessibilitySelectorAllowed:(SEL)s {
	orielAXInfo i;
	if (![self orielInfo:&i text:NO]) {
		return NO;
	}
	if (s == @selector(accessibilityPerformPress)) {
		return i.press;
	}
	if (s == @selector(accessibilityPerformIncrement) || s == @selector(accessibilityPerformDecrement)) {
		return i.step;
	}
	if (s == @selector(isAccessibilityExpanded) || s == @selector(isAccessibilityDisclosed)) {
		return i.hasExpanded;
	}
	if (s == @selector(setAccessibilityValue:) || s == @selector(accessibilitySelectedTextRange) ||
		s == @selector(setAccessibilitySelectedTextRange:) || s == @selector(accessibilityNumberOfCharacters) ||
		s == @selector(accessibilitySelectedText) || s == @selector(accessibilityVisibleCharacterRange) ||
		s == @selector(accessibilityInsertionPointLineNumber) || s == @selector(accessibilityStringForRange:) ||
		s == @selector(accessibilityLineForIndex:) || s == @selector(accessibilityRangeForLine:) ||
		s == @selector(accessibilityFrameForRange:)) {
		return i.text;
	}
	return [super isAccessibilitySelectorAllowed:s];
}

- (BOOL)accessibilityPerformPress {
	return orielAXDo(self.bridge, self.node, orielAXPress, NULL, 0, 0) != 0;
}

- (BOOL)accessibilityPerformIncrement {
	return orielAXDo(self.bridge, self.node, orielAXIncrement, NULL, 0, 0) != 0;
}

- (BOOL)accessibilityPerformDecrement {
	return orielAXDo(self.bridge, self.node, orielAXDecrement, NULL, 0, 0) != 0;
}

- (void)setAccessibilityFocused:(BOOL)focused {
	if (focused) {
		orielAXDo(self.bridge, self.node, orielAXFocus, NULL, 0, 0);
	}
}

- (void)setAccessibilityValue:(id)value {
	if ([value isKindOfClass:[NSString class]]) {
		orielAXDo(self.bridge, self.node, orielAXSetValue, (char *)[(NSString *)value UTF8String], 0, 0);
	}
}

- (NSRange)accessibilitySelectedTextRange {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] ? NSMakeRange(i.selLoc, i.selLen) : NSMakeRange(0, 0);
}

- (void)setAccessibilitySelectedTextRange:(NSRange)r {
	orielAXDo(self.bridge, self.node, orielAXSelect, NULL, (long)r.location, (long)r.length);
}

- (NSInteger)accessibilityNumberOfCharacters {
	orielAXInfo i;
	return [self orielInfo:&i text:NO] ? i.chars : 0;
}

- (NSString *)accessibilityStringForRange:(NSRange)r {
	return orielTake(orielAXSubstring(self.bridge, self.node, (long)r.location, (long)r.length));
}

- (NSString *)accessibilitySelectedText {
	NSRange r = [self accessibilitySelectedTextRange];
	return [self accessibilityStringForRange:r];
}

- (NSRange)accessibilityVisibleCharacterRange {
	return NSMakeRange(0, [self accessibilityNumberOfCharacters]);
}

- (NSInteger)accessibilityInsertionPointLineNumber {
	return 0;
}

- (NSInteger)accessibilityLineForIndex:(NSInteger)index {
	return 0;
}

- (NSRange)accessibilityRangeForLine:(NSInteger)line {
	return line == 0 ? [self accessibilityVisibleCharacterRange] : NSMakeRange(0, 0);
}

- (NSRect)accessibilityFrameForRange:(NSRange)r {
	return [self accessibilityFrame];
}

@end

static id orielViewChildren(id v, SEL cmd) {
	uintptr_t h = orielViewHandle(v);
	return h != 0 ? orielAXChildren(h, 0) : @[];
}

static id orielViewHit(id v, SEL cmd, NSPoint p) {
	uintptr_t h = orielViewHandle(v);
	if (h == 0) {
		return v;
	}
	NSPoint q = orielAXViewPoint(v, p);
	return orielAXObject(orielAXHit(h, q.x, q.y), v);
}

static id orielViewFocused(id v, SEL cmd) {
	uintptr_t h = orielViewHandle(v);
	return h != 0 ? orielAXObject(orielAXFocusedElement(h), v) : v;
}

// orielViewAcceptsFirstMouse lets a click on the window while another has the focus reach Oriel as well as bring the
// window forward, as a click does in most editors: AppKit's default swallows it, so with a picture-in-picture video
// or another always-on-top window taking the focus back, every control needed two clicks.
static BOOL orielViewAcceptsFirstMouse(id v, SEL cmd, NSEvent *e) {
	return YES;
}

static void orielAXOverride(Class sub, Class base, SEL sel, IMP imp, const char *types) {
	Method m = class_getInstanceMethod(base, sel);
	class_addMethod(sub, sel, imp, m != NULL ? method_getTypeEncoding(m) : types);
}

// orielViewSubclass is base's subclass with Oriel's overrides, made the first time it's asked for.
static Class orielViewSubclass(Class base) {
	NSString *name = [@"OrielAX_" stringByAppendingString:NSStringFromClass(base)];
	Class sub = NSClassFromString(name);
	if (sub != nil) {
		return sub;
	}
	sub = objc_allocateClassPair(base, name.UTF8String, 0);
	if (sub == nil) {
		return nil;
	}
	orielAXOverride(sub, base, @selector(accessibilityChildren), (IMP)orielViewChildren, "@@:");
	orielAXOverride(sub, base, @selector(accessibilityHitTest:), (IMP)orielViewHit, "@@:{CGPoint=dd}");
	orielAXOverride(sub, base, @selector(accessibilityFocusedUIElement), (IMP)orielViewFocused, "@@:");
	orielAXOverride(sub, base, @selector(acceptsFirstMouse:), (IMP)orielViewAcceptsFirstMouse, "c@:@");
	objc_registerClassPair(sub);
	return sub;
}

// orielProbeAcceptsFirstMouse says Oriel's subclass of NSView takes a click that brings its window forward: its
// acceptsFirstMouse: answers YES. It needs no window or main thread. An int, not a BOOL: BOOL is bool on arm64 and
// a signed char on x86_64, and Go sees the two as different types.
static int orielProbeAcceptsFirstMouse(void) {
	Class sub = orielViewSubclass([NSView class]);
	if (sub == nil) {
		return 0;
	}
	Method m = class_getInstanceMethod(sub, @selector(acceptsFirstMouse:));
	if (m == NULL) {
		return 0;
	}
	BOOL (*f)(id, SEL, id) = (BOOL (*)(id, SEL, id))method_getImplementation(m);
	return f(nil, @selector(acceptsFirstMouse:), nil) ? 1 : 0;
}

// orielAXAttach answers the view's accessibility questions from bridge h: its children, hit tests and focus. Gio's
// view is its own class, so the view is moved to a subclass made here that overrides those three, and takes a
// click that brings the window forward (acceptsFirstMouse:).
static void orielAXAttach(uintptr_t viewRef, uintptr_t h) {
	NSView *view = (__bridge NSView *)(void *)viewRef;
	dispatch_async(dispatch_get_main_queue(), ^{
		if (orielAXViews == nil) {
			orielAXViews = [NSMapTable strongToWeakObjectsMapTable];
		}
		[orielAXViews setObject:view forKey:@(h)];
		objc_setAssociatedObject(view, orielAXKey, @(h), OBJC_ASSOCIATION_RETAIN_NONATOMIC);
		Class base = object_getClass(view);
		if (strncmp(class_getName(base), "OrielAX_", 8) == 0) {
			return;
		}
		Class sub = orielViewSubclass(base);
		if (sub != nil) {
			object_setClass(view, sub);
		}
	});
}

// orielAXDetach forgets h's view without touching it: the window's loop ends while AppKit is freeing the view.
static void orielAXDetach(uintptr_t h) {
	dispatch_async(dispatch_get_main_queue(), ^{
		[orielAXViews removeObjectForKey:@(h)];
	});
}

// orielAXMake is a new element for node, owned by the caller. It runs on the main thread.
static uintptr_t orielAXMake(uintptr_t viewRef, uintptr_t h, uint64_t node) {
	OrielAXElement *e = [[OrielAXElement alloc] init];
	e.bridge = h;
	e.node = node;
	e.host = (__bridge NSView *)(void *)viewRef;
	return (uintptr_t)CFBridgingRetain(e);
}

static void orielAXRelease(uintptr_t el) {
	dispatch_async(dispatch_get_main_queue(), ^{
		id e = CFBridgingRelease((CFTypeRef)(void *)el);
		NSAccessibilityPostNotification(e, NSAccessibilityUIElementDestroyedNotification);
	});
}

static void orielAXPost(uintptr_t h, uint64_t node, int kind, char *text) {
	NSString *s = orielTake(text);
	dispatch_async(dispatch_get_main_queue(), ^{
		NSView *view = [orielAXViews objectForKey:@(h)];
		if (view == nil) {
			return;
		}
		id target = view;
		if (node != 0) {
			uintptr_t el = orielAXElementOf(h, node);
			if (el == 0) {
				return;
			}
			target = (__bridge id)(void *)el;
		}
		switch (kind) {
		case orielAXNoteFocus:
			NSAccessibilityPostNotification(target, NSAccessibilityFocusedUIElementChangedNotification);
			break;
		case orielAXNoteTitle:
			NSAccessibilityPostNotification(target, NSAccessibilityTitleChangedNotification);
			break;
		case orielAXNoteValue:
			NSAccessibilityPostNotification(target, NSAccessibilityValueChangedNotification);
			break;
		case orielAXNoteSelectedText:
			NSAccessibilityPostNotification(target, NSAccessibilitySelectedTextChangedNotification);
			break;
		case orielAXNoteLayout:
			NSAccessibilityPostNotification(view, NSAccessibilityLayoutChangedNotification);
			break;
		case orielAXNoteExpanded:
			NSAccessibilityPostNotification(target, NSAccessibilityRowExpandedNotification);
			break;
		case orielAXNoteCollapsed:
			NSAccessibilityPostNotification(target, NSAccessibilityRowCollapsedNotification);
			break;
		case orielAXNoteAnnounce:
			if (s != nil) {
				NSAccessibilityPostNotificationWithUserInfo(NSApp, NSAccessibilityAnnouncementRequestedNotification, @{
					NSAccessibilityAnnouncementKey: s,
					NSAccessibilityPriorityKey: @(NSAccessibilityPriorityMedium),
				});
			}
			break;
		}
	});
}
*/
import "C"

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"gioui.org/app"
	"gioui.org/io/event"
)

// The C enums are appkitRole's, appkitSubrole's and appkitNoteKind's lists: these fail to compile if they drift.
var (
	_ = [1]struct{}{}[int(C.orielAXRoleCount)-int(axRoleCount)]
	_ = [1]struct{}{}[int(C.orielAXCheckBox)-int(axCheckBox)]
	_ = [1]struct{}{}[int(C.orielAXTextField)-int(axTextField)]
	_ = [1]struct{}{}[int(C.orielAXSubroleCount)-int(axSubroleCount)]
	_ = [1]struct{}{}[int(C.orielAXDialog)-int(axDialog)]
	_ = [1]struct{}{}[int(C.orielAXNoteCount)-int(noteCount)]
	_ = [1]struct{}{}[int(C.orielAXNoteCollapsed)-int(noteCollapsed)]
)

var (
	appkitBridges sync.Map // handle → *appkitBridge
	appkitIDs     atomic.Uintptr
)

// appkitBridge gives Gio's NSView an NSAccessibilityElement for each node. Nothing is made, and no notification
// posted, until a screen reader first asks the view for its children.
type appkitBridge struct {
	ax     *access
	h      uintptr
	nsview atomic.Uintptr
	on     atomic.Bool
	els    elementMap
}

func newAccessBridge(ax *access) accessBridge {
	b := &appkitBridge{ax: ax, h: appkitIDs.Add(1)}
	b.els.make = func(id uint64) uintptr {
		return uintptr(C.orielAXMake(C.uintptr_t(b.nsview.Load()), C.uintptr_t(b.h), C.uint64_t(id)))
	}
	b.els.free = func(_ uint64, el uintptr) { C.orielAXRelease(C.uintptr_t(el)) }
	appkitBridges.Store(b.h, b)
	return b
}

func appkitBridgeOf(h C.uintptr_t) *appkitBridge {
	if b, ok := appkitBridges.Load(uintptr(h)); ok {
		return b.(*appkitBridge)
	}
	return nil
}

func (b *appkitBridge) view(ev event.Event) {
	v, ok := ev.(app.AppKitViewEvent)
	if !ok || v.View == 0 {
		return
	}
	b.nsview.Store(v.View)
	C.orielAXAttach(C.uintptr_t(v.View), C.uintptr_t(b.h))
}

func (b *appkitBridge) update(c accessChange) {
	v := b.nsview.Load()
	if !b.on.Load() || v == 0 || c.empty() {
		return
	}
	b.els.drop(c.Removed)
	for _, n := range appkitNotes(c, &b.ax.tree) {
		var text *C.char
		if n.Text != "" {
			text = C.CString(n.Text) // freed by orielAXPost
		}
		C.orielAXPost(C.uintptr_t(b.h), C.uint64_t(n.ID), C.int(n.Kind), text)
	}
}

func (b *appkitBridge) activate(bool) {}

func (b *appkitBridge) wanted() bool { return b.on.Load() }

func (b *appkitBridge) close() {
	appkitBridges.Delete(b.h)
	C.orielAXDetach(C.uintptr_t(b.h))
	b.els.clear()
}

// points is how many points an app unit is: the zoom.
func (b *appkitBridge) points() float32 {
	g := b.ax.geom()
	if g.Dpi <= 0 || g.Scale <= 0 {
		return 1
	}
	return g.Scale / g.Dpi
}

func cArray(hs []uintptr) *C.uintptr_t {
	if len(hs) == 0 {
		return nil
	}
	p := (*C.uintptr_t)(C.malloc(C.size_t(len(hs)) * C.size_t(unsafe.Sizeof(C.uintptr_t(0)))))
	out := unsafe.Slice(p, len(hs))
	for i, h := range hs {
		out[i] = C.uintptr_t(h)
	}
	return p
}

// viewTakesFirstClick says Oriel's view takes a click that brings its window forward, as well as doing what it's on.
func viewTakesFirstClick() bool { return C.orielProbeAcceptsFirstMouse() != 0 }
