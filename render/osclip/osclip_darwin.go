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

// orielClipSetImage puts n bytes of PNG on the clipboard, as PNG and as the TIFF other apps may look for; 0 when it
// can't.
static int orielClipSetImage(const void *png, size_t n) {
	@autoreleasepool {
		NSData *data = [NSData dataWithBytes:png length:n];
		NSBitmapImageRep *rep = [NSBitmapImageRep imageRepWithData:data];
		if (rep == nil) {
			return 0;
		}
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		[pb clearContents];
		[pb declareTypes:@[NSPasteboardTypePNG, NSPasteboardTypeTIFF] owner:nil];
		BOOL ok = [pb setData:data forType:NSPasteboardTypePNG];
		NSData *tiff = [rep TIFFRepresentation];
		if (tiff != nil) {
			[pb setData:tiff forType:NSPasteboardTypeTIFF];
		}
		return ok ? 1 : 0;
	}
}

// orielClipDensity is the clipboard picture's pixels per point, as AppKit reads it (a TIFF's or a PNG's DPI): 2 for a
// Retina screenshot; 0 when it can't tell.
static double orielClipDensity(NSPasteboard *pb) {
	NSImage *img = [[NSImage alloc] initWithPasteboard:pb];
	NSImageRep *rep = img.representations.firstObject;
	if (img == nil || rep == nil || img.size.width <= 0 || rep.pixelsWide <= 0) {
		return 0;
	}
	return (double)rep.pixelsWide / img.size.width;
}

// orielClipImage is the clipboard's picture as PNG, or NULL, and its pixels per point; the caller frees it.
static void *orielClipImage(size_t *n, double *density) {
	@autoreleasepool {
		NSPasteboard *pb = [NSPasteboard generalPasteboard];
		*density = orielClipDensity(pb);
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
	"errors"
	"math"
	"strings"
	"unsafe"

	"github.com/liamg/oriel/internal/imagex"
)

func files() []string {
	p := C.orielClipFiles()
	if p == nil {
		return nil
	}
	defer C.free(unsafe.Pointer(p))
	return strings.Split(C.GoString(p), "\n")
}

func setImage(png []byte) error {
	if C.orielClipSetImage(unsafe.Pointer(&png[0]), C.size_t(len(png))) == 0 {
		return errors.New("the clipboard didn't take the picture")
	}
	return nil
}

// image is the clipboard's PNG, saying its density, which AppKit may keep off the PNG itself (on the TIFF beside it,
// say): drawn without it, a Retina screenshot shows twice its size, enlarged.
func image() []byte {
	var n C.size_t
	var d C.double
	p := C.orielClipImage(&n, &d)
	if p == nil {
		return nil
	}
	defer C.free(p)
	if n > maxImage {
		return nil
	}
	b := C.GoBytes(p, C.int(n))
	if k := math.Round(float64(d)); k >= 2 {
		b = imagex.WithPNGDensity(b, float32(k))
	}
	return b
}
