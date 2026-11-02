//go:build darwin && !ios

package osclip

/*
#cgo CFLAGS: -x objective-c -fobjc-arc
#cgo LDFLAGS: -framework AppKit

#import <AppKit/AppKit.h>
#include <stdlib.h>
#include <string.h>

// orielClipFiles is the copied files' paths, one a line, or NULL; the caller frees it.
static char *orielClipFiles(void) {
	@autoreleasepool {
		NSArray<NSURL *> *urls = [[NSPasteboard generalPasteboard] readObjectsForClasses:@[[NSURL class]]
			options:@{NSPasteboardURLReadingFileURLsOnlyKey: @YES}];
		if (urls.count == 0) {
			return NULL;
		}
		NSMutableArray<NSString *> *paths = [NSMutableArray array];
		for (NSURL *u in urls) {
			if (u.path != nil) {
				[paths addObject:u.path];
			}
		}
		return strdup([[paths componentsJoinedByString:@"\n"] UTF8String]);
	}
}

// orielClipImage is the clipboard's picture as PNG, or NULL; the caller frees it.
static void *orielClipImage(size_t *n) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		NSData *png = [pb dataForType:NSPasteboardTypePNG];
		if (png == nil) {
			NSData *tiff = [pb dataForType:NSPasteboardTypeTIFF];
			if (tiff == nil) {
				return NULL;
			}
			NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:tiff];
			png = [rep representationUsingType:NSBitmapImageFileTypePNG properties:@{}];
			if (png == nil) {
				return NULL;
			}
		}
		*n = png.length;
		void *out = malloc(png.length);
		memcpy(out, png.bytes, png.length);
		return out;
	}
}
*/
import "C"

import (
	"strings"
	"unsafe"
)

func files() []string {
	p := C.orielClipFiles()
	if p == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(p))
	return strings.Split(C.GoString(p), "\n")
}

func image() []byte {
	var n C.size_t
	p := C.orielClipImage(&n)
	if p == nil {
		return nil
	}
	defer C.free(p)
	if n > maxImage {
		return nil
	}
	return C.GoBytes(p, C.int(n))
}
