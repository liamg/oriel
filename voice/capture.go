package voice

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
)

// ErrMicDenied is the OS refusing Oriel the microphone; PrivacySettings is where the user can allow it on macOS.
var ErrMicDenied = errors.New("Oriel isn't allowed to use the microphone: allow it in System Settings › Privacy & Security › Microphone")

// PrivacySettings opens macOS's microphone privacy settings.
const PrivacySettings = "x-apple.systempreferences:com.apple.preference.security?Privacy_Microphone"

// openNative opens the microphone in this process (capture_darwin.go, capture_linux.go); nil where Oriel can't.
var openNative func() (Stream, error)

// Microphone is the system microphone, captured in this process where it can be (Core Audio on macOS; PulseAudio,
// or PipeWire's Pulse server, on Linux), else by a recorder program.
type Microphone struct {
	Recorder string // a recorder of RecorderNames to use instead; "" captures natively, falling back to the first installed
}

func (m Microphone) Open(ctx context.Context) (Stream, error) {
	if m.Recorder != "" || openNative == nil {
		return Recorder{Name: m.Recorder}.Open(ctx)
	}
	s, err := openNative()
	if err == nil {
		stop := context.AfterFunc(ctx, func() { _ = s.Close() })
		return &ctxStream{Stream: s, stop: stop}, nil
	}
	if errors.Is(err, ErrMicDenied) {
		return nil, err
	}
	rs, rerr := Recorder{}.Open(ctx)
	if errors.Is(rerr, ErrNoRecorder) {
		return nil, fmt.Errorf("the microphone: %w", err)
	}
	return rs, rerr
}

// Installed reports whether there's a way to capture audio.
func (m Microphone) Installed() bool {
	return m.Recorder == "" && openNative != nil || Recorder{Name: m.Recorder}.Installed()
}

type ctxStream struct {
	Stream
	stop func() bool
}

func (s *ctxStream) Close() error {
	s.stop()
	return s.Stream.Close()
}

// readerStream is a stream of raw s16le audio read from r until it ends: stop asks it to end, and free runs once
// reading has.
func readerStream(label string, r io.Reader, stop, free func()) *procStream {
	ctx, cancel := context.WithCancel(context.Background())
	s := &procStream{label: label, frames: make(chan []float32, 64), cancel: func() { cancel(); stop() }}
	go func() {
		err := readFrames(bufio.NewReaderSize(r, FrameSamples*2), s.frames, ctx.Done())
		free()
		s.fail(err)
		close(s.frames)
	}()
	return s
}
