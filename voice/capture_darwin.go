//go:build cgo

package voice

/*
#cgo LDFLAGS: -framework AudioToolbox -framework AVFoundation -framework CoreFoundation -lobjc
#include <AudioToolbox/AudioToolbox.h>
#include <dispatch/dispatch.h>
#include <objc/message.h>
#include <objc/runtime.h>
#include <stdlib.h>
#include <unistd.h>

extern void *AVMediaTypeAudio; // an NSString in AVFoundation

// oriel_mic_status is AVCaptureDevice's authorization for audio: 0 not asked, 1 restricted, 2 denied, 3 allowed.
static long oriel_mic_status(void) {
	return ((long (*)(id, SEL, void *))objc_msgSend)((id)objc_getClass("AVCaptureDevice"),
		sel_registerName("authorizationStatusForMediaType:"), AVMediaTypeAudio);
}

// oriel_mic_request asks the user for the microphone and waits for the answer.
static int oriel_mic_request(void) {
	__block int granted = 0;
	dispatch_semaphore_t done = dispatch_semaphore_create(0);
	void (^handler)(BOOL) = ^(BOOL ok) {
		granted = ok ? 1 : 0;
		dispatch_semaphore_signal(done);
	};
	((void (*)(id, SEL, void *, void *))objc_msgSend)((id)objc_getClass("AVCaptureDevice"),
		sel_registerName("requestAccessForMediaType:completionHandler:"), AVMediaTypeAudio, (void *)handler);
	dispatch_semaphore_wait(done, DISPATCH_TIME_FOREVER);
	dispatch_release(done);
	return granted;
}

typedef struct {
	AudioQueueRef q;
	int fd;
} oriel_aq;

// oriel_aq_input writes each buffer of audio to the pipe Go reads, and hands the buffer back.
static void oriel_aq_input(void *user, AudioQueueRef q, AudioQueueBufferRef buf, const AudioTimeStamp *t, UInt32 n,
		const AudioStreamPacketDescription *desc) {
	oriel_aq *a = user;
	const char *p = buf->mAudioData;
	size_t left = buf->mAudioDataByteSize;
	while (left > 0) {
		ssize_t w = write(a->fd, p, left);
		if (w <= 0) break;
		p += w;
		left -= (size_t)w;
	}
	AudioQueueEnqueueBuffer(q, buf, 0, NULL);
}

// oriel_aq_start records 16 kHz mono s16le from the default input into fd; Core Audio converts the device's format.
static OSStatus oriel_aq_start(int fd, oriel_aq **out) {
	AudioStreamBasicDescription f = {0};
	f.mSampleRate = 16000;
	f.mFormatID = kAudioFormatLinearPCM;
	f.mFormatFlags = kLinearPCMFormatFlagIsSignedInteger | kLinearPCMFormatFlagIsPacked;
	f.mBitsPerChannel = 16;
	f.mChannelsPerFrame = 1;
	f.mBytesPerFrame = 2;
	f.mFramesPerPacket = 1;
	f.mBytesPerPacket = 2;
	oriel_aq *a = calloc(1, sizeof *a);
	a->fd = fd;
	OSStatus st = AudioQueueNewInput(&f, oriel_aq_input, a, NULL, NULL, 0, &a->q);
	if (st != noErr) {
		free(a);
		return st;
	}
	for (int i = 0; i < 8; i++) {
		AudioQueueBufferRef b;
		if ((st = AudioQueueAllocateBuffer(a->q, 1280, &b)) != noErr || (st = AudioQueueEnqueueBuffer(a->q, b, 0, NULL)) != noErr) {
			AudioQueueDispose(a->q, true);
			free(a);
			return st;
		}
	}
	if ((st = AudioQueueStart(a->q, NULL)) != noErr) {
		AudioQueueDispose(a->q, true);
		free(a);
		return st;
	}
	*out = a;
	return noErr;
}

static void oriel_aq_stop(oriel_aq *a) {
	AudioQueueStop(a->q, true);
	AudioQueueDispose(a->q, true);
	free(a);
}
*/
import "C"

import (
	"fmt"
	"os"
	"sync"
	"syscall"
)

func init() { openNative = openCoreAudio }

// openCoreAudio records through an Audio Queue. Asking for the microphone the first time waits for the user's answer
// off the caller's goroutine: a refusal ends the stream with ErrMicDenied.
func openCoreAudio() (Stream, error) {
	status := C.oriel_mic_status()
	if status == 1 || status == 2 {
		return nil, ErrMicDenied
	}
	var fds [2]int
	if err := syscall.Pipe(fds[:]); err != nil {
		return nil, err
	}
	syscall.CloseOnExec(fds[0])
	syscall.CloseOnExec(fds[1])
	r := os.NewFile(uintptr(fds[0]), "mic")
	var (
		mu      sync.Mutex
		q       *C.oriel_aq
		stopped bool
	)
	// end stops the queue, if it started, and closes the pipe's write end, which ends the reading.
	end := func() {
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		stopped = true
		if q != nil {
			C.oriel_aq_stop(q)
		}
		syscall.Close(fds[1])
	}
	s := readerStream("coreaudio", r, func() { go end() }, func() { _ = r.Close() })
	go func() {
		if status == 0 && C.oriel_mic_request() == 0 {
			s.fail(ErrMicDenied)
			end()
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if stopped {
			return
		}
		if st := C.oriel_aq_start(C.int(fds[1]), &q); st != 0 {
			q = nil
			s.fail(fmt.Errorf("the microphone didn't start (Core Audio error %d)", int32(st)))
			stopped = true
			syscall.Close(fds[1])
		}
	}()
	return s, nil
}
