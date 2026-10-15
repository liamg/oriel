package voice

import (
	"bufio"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// Rate is the sample rate everything here works at: 16 kHz mono, what Whisper takes.
const Rate = 16000

// FrameSamples is one frame: 20 ms.
const FrameSamples = Rate / 50

// Mic opens the microphone.
type Mic interface {
	Open(ctx context.Context) (Stream, error)
}

// Stream is the microphone, open: 20 ms frames of 16 kHz mono audio, each sample in [-1, 1].
type Stream interface {
	Frames() <-chan []float32 // closed when the stream ends
	Label() string            // what's recording: "rec", "arecord"
	Err() error               // why it ended, once Frames is closed; nil when it was closed
	Close() error
}

// ErrNoRecorder is no recorder on PATH to capture audio with.
var ErrNoRecorder = errors.New("dictation needs a recorder to capture audio with: install sox (its rec), ffmpeg, or on Linux pipewire, pulseaudio or alsa-utils")

// recorder is a program that writes raw 16 kHz mono signed 16-bit little-endian audio to stdout.
type recorder struct {
	name string
	args []string
	only string // the GOOS it's for; "" for every one
}

// Recorders, in the order Recorder tries them.
var recorders = []recorder{
	{name: "pw-record", args: []string{"--rate", "16000", "--channels", "1", "--format", "s16", "-"}, only: "linux"},
	{name: "parecord", args: []string{"--raw", "--format=s16le", "--rate=16000", "--channels=1"}, only: "linux"},
	{name: "arecord", args: []string{"-q", "-f", "S16_LE", "-r", "16000", "-c", "1", "-t", "raw"}, only: "linux"},
	{name: "rec", args: []string{"-q", "-t", "raw", "-r", "16000", "-e", "signed-integer", "-b", "16", "-c", "1", "-"}},
	{name: "ffmpeg", args: []string{"-loglevel", "error", "-f", "avfoundation", "-i", ":0", "-ac", "1", "-ar", "16000", "-f", "s16le", "-"}, only: "darwin"},
	{name: "ffmpeg", args: []string{"-loglevel", "error", "-f", "pulse", "-i", "default", "-ac", "1", "-ar", "16000", "-f", "s16le", "-"}, only: "linux"},
}

// RecorderNames are the recorders this platform can use, in the order they're tried.
func RecorderNames() []string {
	var out []string
	for _, r := range recorders {
		if (r.only == "" || r.only == runtime.GOOS) && !contains(out, r.name) {
			out = append(out, r.name)
		}
	}
	return out
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Recorder captures audio by running a recorder program, the first on PATH of RecorderNames, or Name's. Oriel
// adds no audio library: every platform has a recorder that writes raw PCM to a pipe.
type Recorder struct {
	Name     string // a recorder of RecorderNames; "" picks the first installed
	LookPath func(string) (string, error)
	// Command makes the recorder's process; swapped in tests.
	Command func(ctx context.Context, name string, args ...string) *exec.Cmd
}

func (r Recorder) pick() (recorder, string, error) {
	look := r.LookPath
	if look == nil {
		look = exec.LookPath
	}
	for _, rc := range recorders {
		if rc.only != "" && rc.only != runtime.GOOS || r.Name != "" && rc.name != r.Name {
			continue
		}
		if p, err := look(rc.name); err == nil {
			return rc, p, nil
		}
	}
	if r.Name != "" {
		return recorder{}, "", fmt.Errorf("the recorder %s isn't installed", r.Name)
	}
	return recorder{}, "", ErrNoRecorder
}

// Installed reports whether there's a recorder to capture audio with.
func (r Recorder) Installed() bool {
	_, _, err := r.pick()
	return err == nil
}

func (r Recorder) Open(ctx context.Context) (Stream, error) {
	rc, path, err := r.pick()
	if err != nil {
		return nil, err
	}
	command := r.Command
	if command == nil {
		command = exec.CommandContext
	}
	ctx, cancel := context.WithCancel(ctx)
	cmd := command(ctx, path, rc.args...)
	out, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &limited{w: &stderr, n: 4096}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("%s: %w", rc.name, err)
	}
	s := &procStream{label: rc.name, frames: make(chan []float32, 64), cancel: cancel}
	go func() {
		readErr := readFrames(bufio.NewReader(out), s.frames, ctx.Done())
		waitErr := cmd.Wait()
		s.mu.Lock()
		if !s.closed {
			switch {
			case waitErr != nil:
				s.err = fmt.Errorf("%s stopped: %s", rc.name, firstLine(stderr.String(), waitErr.Error()))
			case readErr != nil:
				s.err = readErr
			}
		}
		s.mu.Unlock()
		close(s.frames)
	}()
	return s, nil
}

// readFrames turns raw s16le audio into frames until it ends, or done.
func readFrames(r io.Reader, out chan<- []float32, done <-chan struct{}) error {
	buf := make([]byte, FrameSamples*2)
	for {
		n, err := io.ReadFull(r, buf)
		if n >= 2 {
			f := make([]float32, n/2)
			for i := range f {
				f[i] = float32(int16(binary.LittleEndian.Uint16(buf[2*i:]))) / 32768
			}
			select {
			case out <- f:
			case <-done:
				_, _ = io.Copy(io.Discard, r)
				return nil
			}
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type procStream struct {
	label  string
	frames chan []float32
	cancel context.CancelFunc
	mu     sync.Mutex
	err    error
	closed bool
}

func (s *procStream) Frames() <-chan []float32 { return s.frames }
func (s *procStream) Label() string            { return s.label }

func (s *procStream) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (s *procStream) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.cancel()
	return nil
}

// limited keeps the first n bytes written.
type limited struct {
	w io.Writer
	n int
}

func (l *limited) Write(p []byte) (int, error) {
	if l.n > 0 {
		k := min(len(p), l.n)
		l.n -= k
		_, _ = l.w.Write(p[:k])
	}
	return len(p), nil
}

func firstLine(s, or string) string {
	if s = strings.TrimSpace(s); s == "" {
		return or
	}
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// EncodeWAV is frames as a 16-bit mono WAV file at Rate.
func EncodeWAV(frames [][]float32) []byte {
	n := 0
	for _, f := range frames {
		n += len(f)
	}
	b := make([]byte, 44+n*2)
	copy(b[0:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+n*2))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], Rate)
	binary.LittleEndian.PutUint32(b[28:], Rate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(n*2))
	o := 44
	for _, f := range frames {
		for _, v := range f {
			v = max(-1, min(1, v))
			var s int16
			if v < 0 {
				s = int16(v * 0x8000)
			} else {
				s = int16(v * 0x7fff)
			}
			binary.LittleEndian.PutUint16(b[o:], uint16(s))
			o += 2
		}
	}
	return b
}
