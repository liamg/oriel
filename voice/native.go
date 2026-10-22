//go:build whisper_native && cgo

package voice

/*
#cgo CFLAGS: -I${SRCDIR}/native/include -O2
#cgo linux,amd64 LDFLAGS: ${SRCDIR}/native/lib/linux_amd64/libwhisper.a ${SRCDIR}/native/lib/linux_amd64/libggml.a ${SRCDIR}/native/lib/linux_amd64/libggml-cpu.a ${SRCDIR}/native/lib/linux_amd64/libggml-base.a
#cgo linux,arm64 LDFLAGS: ${SRCDIR}/native/lib/linux_arm64/libwhisper.a ${SRCDIR}/native/lib/linux_arm64/libggml.a ${SRCDIR}/native/lib/linux_arm64/libggml-cpu.a ${SRCDIR}/native/lib/linux_arm64/libggml-base.a
#cgo darwin,arm64 LDFLAGS: ${SRCDIR}/native/lib/darwin_arm64/libwhisper.a ${SRCDIR}/native/lib/darwin_arm64/libggml.a ${SRCDIR}/native/lib/darwin_arm64/libggml-cpu.a ${SRCDIR}/native/lib/darwin_arm64/libggml-metal.a ${SRCDIR}/native/lib/darwin_arm64/libggml-base.a -framework Metal -framework MetalKit
#cgo darwin,amd64 LDFLAGS: ${SRCDIR}/native/lib/darwin_amd64/libwhisper.a ${SRCDIR}/native/lib/darwin_amd64/libggml.a ${SRCDIR}/native/lib/darwin_amd64/libggml-cpu.a ${SRCDIR}/native/lib/darwin_amd64/libggml-base.a
#cgo linux LDFLAGS: -lstdc++ -lm -lpthread
#cgo darwin LDFLAGS: -lc++ -framework Accelerate -framework Foundation
#include <stdlib.h>
#include "whisper.h"

static void oriel_quiet(enum ggml_log_level level, const char *text, void *user) {}

// oriel_cpu_ok: the library is built for x86-64-v3 (build.sh), so older CPUs don't load it.
static int oriel_cpu_ok(void) {
#if defined(__x86_64__)
	__builtin_cpu_init();
	return __builtin_cpu_supports("avx2") && __builtin_cpu_supports("fma") && __builtin_cpu_supports("f16c") &&
		__builtin_cpu_supports("bmi2");
#else
	return 1;
#endif
}

static struct whisper_context *oriel_load(const char *path) {
	whisper_log_set(oriel_quiet, NULL);
	struct whisper_context_params p = whisper_context_default_params();
	return whisper_init_from_file_with_params(path, p);
}

static bool oriel_abort(void *stop) { return *(volatile int *)stop != 0; }

static int oriel_run(struct whisper_context *ctx, const float *samples, int n, const char *prompt, const char *lang,
		int threads, int *stop) {
	struct whisper_full_params p = whisper_full_default_params(WHISPER_SAMPLING_GREEDY);
	p.n_threads = threads;
	p.language = lang;
	p.no_context = true;
	p.no_timestamps = true;
	p.print_special = false;
	p.print_progress = false;
	p.print_realtime = false;
	p.print_timestamps = false;
	p.initial_prompt = prompt;
	p.abort_callback = oriel_abort;
	p.abort_callback_user_data = stop;
	return whisper_full(ctx, p, samples, n);
}
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync/atomic"
	"unsafe"
)

func init() {
	if C.oriel_cpu_ok() != 0 {
		loadEngine = loadNative
	}
}

type native struct {
	ctx  *C.struct_whisper_context
	lang *C.char
}

func loadNative(path string) (engine, error) {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	ctx := C.oriel_load(p)
	if ctx == nil {
		return nil, fmt.Errorf("whisper.cpp couldn't load the model %s", path)
	}
	lang := "auto"
	if C.whisper_is_multilingual(ctx) == 0 {
		lang = "en"
	}
	return &native{ctx: ctx, lang: C.CString(lang)}, nil
}

// transcribe runs one clip; the caller runs one at a time.
func (n *native) transcribe(ctx context.Context, samples []float32, prompt string) (string, error) {
	if len(samples) == 0 {
		return "", nil
	}
	stop := (*C.int)(C.calloc(1, C.size_t(unsafe.Sizeof(C.int(0)))))
	defer C.free(unsafe.Pointer(stop))
	cancel := context.AfterFunc(ctx, func() { atomic.StoreInt32((*int32)(unsafe.Pointer(stop)), 1) })
	defer cancel()
	cp := C.CString(prompt)
	defer C.free(unsafe.Pointer(cp))
	threads := max(1, min(4, runtime.NumCPU()))
	if C.oriel_run(n.ctx, (*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)), cp, n.lang, C.int(threads), stop) != 0 {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "", errors.New("whisper.cpp failed to transcribe")
	}
	var b strings.Builder
	for i := range int(C.whisper_full_n_segments(n.ctx)) {
		b.WriteString(C.GoString(C.whisper_full_get_segment_text(n.ctx, C.int(i))))
		b.WriteByte(' ')
	}
	return Clean(b.String()), nil
}

func (n *native) close() {
	C.whisper_free(n.ctx)
	C.free(unsafe.Pointer(n.lang))
}
