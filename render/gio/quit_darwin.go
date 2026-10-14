//go:build darwin && !ios

package gio

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>

// orielHoldQuit points the app menu's Quit at the key window's close, which asks the app first, or back at
// terminate:.
static void orielHoldQuit(int hold) {
	dispatch_async(dispatch_get_main_queue(), ^{
		SEL quit = @selector(terminate:), shut = @selector(performClose:);
		for (NSMenuItem *top in NSApp.mainMenu.itemArray) {
			for (NSMenuItem *item in top.submenu.itemArray) {
				if (item.action == quit || item.action == shut) {
					item.action = hold ? shut : quit;
					item.target = nil;
				}
			}
		}
	});
}
*/
import "C"

// holdQuit makes Quit (⌘Q) a request to close the window while hold, so a held close covers it too.
func holdQuit(hold bool) {
	h := C.int(0)
	if hold {
		h = 1
	}
	C.orielHoldQuit(h)
}
