//go:build cgo

package voice

/*
#cgo LDFLAGS: -ldl
#include <dlfcn.h>
#include <stdint.h>
#include <stdlib.h>

// The few declarations of libpulse-simple's API used, so no headers are needed to build: it's loaded at run time.
typedef struct { int format; uint32_t rate; uint8_t channels; } oriel_pa_spec;
typedef struct { uint32_t maxlength, tlength, prebuf, minreq, fragsize; } oriel_pa_attr;
typedef void *(*oriel_pa_new_fn)(const char *, const char *, int, const char *, const char *, const oriel_pa_spec *,
	const void *, const oriel_pa_attr *, int *);
typedef int (*oriel_pa_read_fn)(void *, void *, size_t, int *);
typedef void (*oriel_pa_free_fn)(void *);
typedef const char *(*oriel_pa_strerror_fn)(int);

static oriel_pa_new_fn oriel_pa_new;
static oriel_pa_read_fn oriel_pa_read;
static oriel_pa_free_fn oriel_pa_free;
static oriel_pa_strerror_fn oriel_pa_strerror;

static int oriel_pa_load(void) {
	void *h = dlopen("libpulse-simple.so.0", RTLD_NOW);
	if (!h) return 0;
	oriel_pa_new = (oriel_pa_new_fn)dlsym(h, "pa_simple_new");
	oriel_pa_read = (oriel_pa_read_fn)dlsym(h, "pa_simple_read");
	oriel_pa_free = (oriel_pa_free_fn)dlsym(h, "pa_simple_free");
	oriel_pa_strerror = (oriel_pa_strerror_fn)dlsym(h, "pa_strerror");
	return oriel_pa_new && oriel_pa_read && oriel_pa_free && oriel_pa_strerror;
}

// oriel_pa_open records 16 kHz mono s16le (PA_SAMPLE_S16LE) from the default source (PA_STREAM_RECORD), in 20 ms
// fragments.
static void *oriel_pa_open(int *err) {
	oriel_pa_spec spec = {3, 16000, 1}; // PA_SAMPLE_S16LE
	oriel_pa_attr attr = {(uint32_t)-1, (uint32_t)-1, (uint32_t)-1, (uint32_t)-1, 640};
	return oriel_pa_new(NULL, "Oriel", 2, NULL, "Dictation", &spec, NULL, &attr, err);
}

static int oriel_pa_readn(void *s, void *buf, size_t n, int *err) { return oriel_pa_read(s, buf, n, err); }
static void oriel_pa_close(void *s) { oriel_pa_free(s); }
static const char *oriel_pa_error(int err) { return oriel_pa_strerror(err); }
*/
import "C"

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"unsafe"
)

var pulseLoaded = sync.OnceValue(func() bool { return C.oriel_pa_load() != 0 })

func init() { openNative = openPulse }

// openPulse records through libpulse-simple, which PipeWire's Pulse server also serves.
func openPulse() (Stream, error) {
	if !pulseLoaded() {
		return nil, errors.New("libpulse-simple isn't installed")
	}
	var cerr C.int
	h := C.oriel_pa_open(&cerr)
	if h == nil {
		return nil, errors.New("PulseAudio: " + C.GoString(C.oriel_pa_error(cerr)))
	}
	p := &pulse{h: h}
	return readerStream("pulse", p, func() { p.stopped.Store(true) }, func() { C.oriel_pa_close(h) }), nil
}

type pulse struct {
	h       unsafe.Pointer
	stopped atomic.Bool
}

// Read blocks for len(b) bytes of audio, 20 ms at a time; once stopped it ends.
func (p *pulse) Read(b []byte) (int, error) {
	if p.stopped.Load() {
		return 0, io.EOF
	}
	b = b[:min(len(b), FrameSamples*2)]
	var cerr C.int
	if C.oriel_pa_readn(p.h, unsafe.Pointer(&b[0]), C.size_t(len(b)), &cerr) < 0 {
		return 0, errors.New("PulseAudio: " + C.GoString(C.oriel_pa_error(cerr)))
	}
	return len(b), nil
}
