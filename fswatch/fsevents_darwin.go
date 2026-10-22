//go:build darwin && cgo

package fswatch

/*
#cgo LDFLAGS: -framework CoreServices
#include <CoreServices/CoreServices.h>
#include <dispatch/dispatch.h>
#include <stdint.h>
#include <stdlib.h>

extern void orielFSEvents(uintptr_t handle, size_t n, char **paths, uint32_t *flags);

static void oriel_callback(ConstFSEventStreamRef ref, void *info, size_t n, void *paths,
		const FSEventStreamEventFlags flags[], const FSEventStreamEventId ids[]) {
	orielFSEvents((uintptr_t)info, n, (char **)paths, (uint32_t *)flags);
}

typedef struct {
	FSEventStreamRef ref;
	dispatch_queue_t queue;
} oriel_stream;

static CFStringRef oriel_string(const char *s) {
	return CFStringCreateWithCString(kCFAllocatorDefault, s, kCFStringEncodingUTF8);
}

static oriel_stream *oriel_start(const char *root, char **exclude, int nexclude, uintptr_t handle) {
	oriel_stream *s = malloc(sizeof(oriel_stream));
	if (s == NULL) {
		return NULL;
	}
	CFStringRef path = oriel_string(root);
	if (path == NULL) {
		free(s);
		return NULL;
	}
	CFArrayRef paths = CFArrayCreate(kCFAllocatorDefault, (const void **)&path, 1, &kCFTypeArrayCallBacks);
	CFRelease(path);
	if (paths == NULL) {
		free(s);
		return NULL;
	}
	FSEventStreamContext ctx = {0, (void *)handle, NULL, NULL, NULL};
	FSEventStreamRef ref = FSEventStreamCreate(kCFAllocatorDefault, oriel_callback, &ctx, paths,
		kFSEventStreamEventIdSinceNow, 0.05, kFSEventStreamCreateFlagFileEvents | kFSEventStreamCreateFlagNoDefer);
	CFRelease(paths);
	if (ref == NULL) {
		free(s);
		return NULL;
	}
	if (nexclude > 0) {
		CFMutableArrayRef ex = CFArrayCreateMutable(kCFAllocatorDefault, nexclude, &kCFTypeArrayCallBacks);
		if (ex != NULL) {
			for (int i = 0; i < nexclude; i++) {
				CFStringRef s = oriel_string(exclude[i]);
				if (s != NULL) {
					CFArrayAppendValue(ex, s);
					CFRelease(s);
				}
			}
			FSEventStreamSetExclusionPaths(ref, ex);
			CFRelease(ex);
		}
	}
	dispatch_queue_t queue = dispatch_queue_create("oriel.fswatch", DISPATCH_QUEUE_SERIAL);
	if (queue == NULL) {
		FSEventStreamRelease(ref);
		free(s);
		return NULL;
	}
	FSEventStreamSetDispatchQueue(ref, queue);
	if (!FSEventStreamStart(ref)) {
		FSEventStreamInvalidate(ref);
		FSEventStreamRelease(ref);
		dispatch_release(queue);
		free(s);
		return NULL;
	}
	s->ref = ref;
	s->queue = queue;
	return s;
}

static void oriel_nothing(void *arg) {}

// oriel_stop returns once no callback runs or will.
static void oriel_stop(oriel_stream *s) {
	FSEventStreamStop(s->ref);
	FSEventStreamInvalidate(s->ref);
	dispatch_sync_f(s->queue, NULL, oriel_nothing);
	FSEventStreamRelease(s->ref);
	dispatch_release(s->queue);
	free(s);
}
*/
import "C"

import (
	"fmt"
	"runtime/cgo"
	"sync"
	"unsafe"
)

// fsStream hands a stream's events from FSEvents' queue to a goroutine of its own, so the callback never waits on
// the watcher: stopping a stream waits for the callback.
type fsStream struct {
	send func([]fsevent)
	kick chan struct{}
	quit chan struct{}

	mu  sync.Mutex
	buf []fsevent
}

func (b *kqueue) watchTree(root string, exclude []string, send func([]fsevent)) (func(), error) {
	f := &fsStream{send: send, kick: make(chan struct{}, 1), quit: make(chan struct{})}
	h := cgo.NewHandle(f)
	croot := C.CString(root)
	defer C.free(unsafe.Pointer(croot))
	cex := (**C.char)(C.malloc(C.size_t(max(len(exclude), 1)) * C.size_t(unsafe.Sizeof((*C.char)(nil)))))
	ex := unsafe.Slice(cex, max(len(exclude), 1))
	for i, p := range exclude {
		ex[i] = C.CString(p)
	}
	s := C.oriel_start(croot, cex, C.int(len(exclude)), C.uintptr_t(h))
	for i := range exclude {
		C.free(unsafe.Pointer(ex[i]))
	}
	C.free(unsafe.Pointer(cex))
	if s == nil {
		h.Delete()
		return nil, fmt.Errorf("fswatch: FSEvents can't watch %s", root)
	}
	go f.loop()
	var once sync.Once
	return func() {
		once.Do(func() {
			C.oriel_stop(s)
			h.Delete()
			close(f.quit)
		})
	}, nil
}

func (f *fsStream) loop() {
	for {
		select {
		case <-f.quit:
			return
		case <-f.kick:
		}
		f.mu.Lock()
		evs := f.buf
		f.buf = nil
		f.mu.Unlock()
		if len(evs) > 0 {
			f.send(evs)
		}
	}
}

//export orielFSEvents
func orielFSEvents(handle C.uintptr_t, n C.size_t, paths **C.char, flags *C.uint32_t) {
	f := cgo.Handle(handle).Value().(*fsStream)
	ps := unsafe.Slice(paths, int(n))
	fl := unsafe.Slice(flags, int(n))
	evs := make([]fsevent, int(n))
	for i := range evs {
		evs[i] = fsevent{path: C.GoString(ps[i]), flags: uint32(fl[i])}
	}
	f.mu.Lock()
	f.buf = append(f.buf, evs...)
	f.mu.Unlock()
	select {
	case f.kick <- struct{}{}:
	default:
	}
}

// cFlags are FSEvents.h's flags, to check the Go copies against.
var cFlags = map[uint32]uint32{
	evMustScanSubDirs: uint32(C.kFSEventStreamEventFlagMustScanSubDirs),
	evUserDropped:     uint32(C.kFSEventStreamEventFlagUserDropped),
	evKernelDropped:   uint32(C.kFSEventStreamEventFlagKernelDropped),
	evItemCreated:     uint32(C.kFSEventStreamEventFlagItemCreated),
	evItemRemoved:     uint32(C.kFSEventStreamEventFlagItemRemoved),
	evItemRenamed:     uint32(C.kFSEventStreamEventFlagItemRenamed),
	evItemModified:    uint32(C.kFSEventStreamEventFlagItemModified),
	evItemIsFile:      uint32(C.kFSEventStreamEventFlagItemIsFile),
	evItemIsDir:       uint32(C.kFSEventStreamEventFlagItemIsDir),
}
