// Package voicetest is a microphone and a transcriber for tests: scripted audio, and words for its loud stretches.
package voicetest

import (
	"context"
	"encoding/binary"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/voice"
)

// Mic is a microphone for tests: each Open plays Script, then the stream ends (or waits for Close, with Hold).
type Mic struct {
	Script [][]float32
	Hold   bool   // keep the stream open after the script until it's closed
	Err    error  // what Open says
	Name   string // the stream's label

	mu     sync.Mutex
	opened int
}

// Opens is how many times it was opened.
func (m *Mic) Opens() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.opened
}

func (m *Mic) Open(context.Context) (voice.Stream, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	m.mu.Lock()
	m.opened++
	m.mu.Unlock()
	s := &fakeStream{frames: make(chan []float32), closed: make(chan struct{}), label: m.Name}
	go func() {
		defer close(s.frames)
		for _, f := range m.Script {
			select {
			case s.frames <- f:
			case <-s.closed:
				return
			}
		}
		if m.Hold {
			<-s.closed
		}
	}()
	return s, nil
}

type fakeStream struct {
	frames chan []float32
	closed chan struct{}
	once   sync.Once
	label  string
}

func (s *fakeStream) Frames() <-chan []float32 { return s.frames }
func (s *fakeStream) Label() string            { return s.label }
func (s *fakeStream) Err() error               { return nil }
func (s *fakeStream) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

// Silence and Speech are d of audio as frames: near-quiet, and a loud tone.
func Silence(d time.Duration) [][]float32 { return tone(d, 0.001) }
func Speech(d time.Duration) [][]float32  { return tone(d, 0.3) }

func tone(d time.Duration, amp float64) [][]float32 {
	n := int(d / (20 * time.Millisecond))
	out := make([][]float32, n)
	for i := range out {
		f := make([]float32, voice.FrameSamples)
		for j := range f {
			f[j] = float32(amp * math.Sin(float64(i*voice.FrameSamples+j)*2*math.Pi*220/voice.Rate))
		}
		out[i] = f
	}
	return out
}

// Script joins stretches of audio.
func Script(parts ...[][]float32) [][]float32 {
	var out [][]float32
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// Transcriber hears a word for every Per of loud audio in a clip (Word, "la" by default), or says Text.
type Transcriber struct {
	Word string
	Per  time.Duration // 200 ms by default
	Text func(heard string) string
	Err  error

	mu      sync.Mutex
	Prompts []string
}

func (f *Transcriber) Transcribe(_ context.Context, wav []byte, prompt string) (string, error) {
	f.mu.Lock()
	f.Prompts = append(f.Prompts, prompt)
	f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	loud := 0
	for i := 44; i+voice.FrameSamples*2 <= len(wav); i += voice.FrameSamples * 2 {
		peak := 0
		for j := i; j < i+voice.FrameSamples*2; j += 2 {
			peak = max(peak, abs(int(int16(binary.LittleEndian.Uint16(wav[j:])))))
		}
		if peak > 3000 {
			loud++
		}
	}
	per, word := f.Per, f.Word
	if per == 0 {
		per = 200 * time.Millisecond
	}
	if word == "" {
		word = "la"
	}
	n := int(time.Duration(loud) * 20 * time.Millisecond / per)
	heard := strings.TrimSpace(strings.Repeat(word+" ", n))
	if f.Text != nil {
		return f.Text(heard), nil
	}
	return heard, nil
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
