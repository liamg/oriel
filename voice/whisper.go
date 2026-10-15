// Package voice is dictation: audio from the microphone through a recorder it runs (mic.go), split into phrases
// at its pauses (listen.go) and turned into text by a local whisper.cpp server or an OpenAI-compatible endpoint;
// an optional wake word (wake.go); and speech for replies (speak.go). Ported from crit's internal/voice and
// web/static/voice.js.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MaxAudio caps one phrase: 30s of 16 kHz mono 16-bit PCM, plus its header.
const MaxAudio = 30*16000*2 + 1024

// ErrNoBinary is whisper-server missing from PATH.
var ErrNoBinary = errors.New("whisper-server not found — install whisper.cpp (brew install whisper-cpp)")

// NoModelError is the model file missing; the browser offers to download it.
type NoModelError struct {
	Name string
	Path string
}

func (e *NoModelError) Error() string { return "no model at " + e.Path }

// Transcriber turns one WAV clip into the words spoken in it. prompt nudges the spelling of words it would guess at.
type Transcriber interface {
	Transcribe(ctx context.Context, wav []byte, prompt string) (string, error)
}

// Settings is what a transcription needs from the stored config.
type Settings struct {
	Model string
	URL   string
}

// Whisper runs one resident whisper-server, started on first use and stopped
// when idle.
type Whisper struct {
	// Dir holds the models; ~/.oriel/whisper.
	Dir string
	// Idle is how long the server stays up unused.
	Idle time.Duration
	// Settings reads the current config on every request.
	Settings func() Settings
	// LookPath and Spawn are swapped in tests.
	LookPath func(string) (string, error)
	Spawn    func(bin, model string) (url string, stop func(), err error)

	reqMu   sync.Mutex // one transcription at a time
	mu      sync.Mutex
	url     string
	model   string
	stop    func()
	lastUse time.Time

	dlMu sync.Mutex
	dl   map[string]*download
}

// New is a Whisper with the real binary, models in ~/.oriel/whisper.
func New(settings func() Settings) *Whisper {
	home, _ := os.UserHomeDir()
	return &Whisper{
		Dir:      filepath.Join(home, ".oriel", "whisper"),
		Idle:     10 * time.Minute,
		Settings: settings,
		LookPath: lookWhisper,
		Spawn:    spawn,
	}
}

// binaries are whisper.cpp's server under the names packagers give it.
var binaries = []string{"whisper-server", "whisper.cpp-server", "whisper-cpp-server"}

func lookWhisper(string) (string, error) {
	for _, b := range binaries {
		if p, err := exec.LookPath(b); err == nil {
			return p, nil
		}
	}
	return "", ErrNoBinary
}

// Installed reports whether transcription can run here at all.
func (w *Whisper) Installed() bool {
	if w.Settings().URL != "" {
		return true
	}
	_, err := w.LookPath("whisper-server")
	return err == nil
}

// DefaultModel is the model for this machine: small.en where Metal makes it
// cheap, base.en on CPU.
func DefaultModel() string {
	if runtime.GOOS == "darwin" && runtime.GOARCH == "arm64" {
		return "small.en"
	}
	return "base.en"
}

// ModelPath resolves a setting to a file: a name like "base.en" lives in Dir.
func (w *Whisper) ModelPath(name string) (path, resolved string) {
	if name == "" {
		name = DefaultModel()
	}
	if strings.ContainsRune(name, os.PathSeparator) || strings.HasSuffix(name, ".bin") {
		return name, name
	}
	return filepath.Join(w.Dir, "ggml-"+name+".bin"), name
}

// Transcribe returns the text spoken in one WAV clip. prompt nudges the
// spelling of words Whisper would otherwise guess at.
func (w *Whisper) Transcribe(ctx context.Context, wav []byte, prompt string) (string, error) {
	w.reqMu.Lock()
	defer w.reqMu.Unlock()
	st := w.Settings()
	if st.URL != "" {
		return openAI(ctx, st.URL, wav, prompt)
	}
	base, err := w.ensure(st.Model)
	if err != nil {
		return "", err
	}
	text, err := post(ctx, base+"/inference", wav, map[string]string{
		"response_format": "json",
		"temperature":     "0.0",
		"prompt":          prompt,
	})
	w.mu.Lock()
	w.lastUse = time.Now()
	w.mu.Unlock()
	return text, err
}

// ensure starts whisper-server for the configured model, or reuses the one
// already running.
func (w *Whisper) ensure(setting string) (string, error) {
	path, name := w.ModelPath(setting)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.url != "" && w.model == path {
		w.lastUse = time.Now()
		return w.url, nil
	}
	bin, err := w.LookPath("whisper-server")
	if err != nil {
		return "", ErrNoBinary
	}
	if _, err := os.Stat(path); err != nil {
		if st, ok := w.DownloadState(name); ok && !st.Done {
			return "", &DownloadingError{Status: st}
		}
		return "", &NoModelError{Name: name, Path: path}
	}
	if w.stop != nil {
		w.stop()
		w.url, w.stop = "", nil
	}
	url, stop, err := w.Spawn(bin, path)
	if err != nil {
		return "", err
	}
	w.url, w.model, w.stop, w.lastUse = url, path, stop, time.Now()
	go w.reap(url)
	return url, nil
}

// reap stops the server once it has gone unused for Idle.
func (w *Whisper) reap(url string) {
	tick := w.Idle / 10
	if tick < 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	for {
		time.Sleep(tick)
		w.mu.Lock()
		if w.url != url {
			w.mu.Unlock()
			return
		}
		if time.Since(w.lastUse) >= w.Idle {
			w.stop()
			w.url, w.stop = "", nil
			w.mu.Unlock()
			return
		}
		w.mu.Unlock()
	}
}

// Running reports whether the resident server is up.
func (w *Whisper) Running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.url != ""
}

// Close stops the server.
func (w *Whisper) Close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop != nil {
		w.stop()
	}
	w.url, w.stop = "", nil
}

// spawn starts whisper-server on a free loopback port and waits for it to
// listen, which is after the model has loaded.
func spawn(bin, model string) (string, func(), error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	cmd := exec.Command(bin, "-m", model, "--host", "127.0.0.1", "--port", strconv.Itoa(port))
	var errBuf bytes.Buffer
	cmd.Stderr = &errBuf
	if err := cmd.Start(); err != nil {
		return "", nil, err
	}
	done := make(chan struct{})
	go func() { _ = cmd.Wait(); close(done) }()
	stop := func() { _ = cmd.Process.Kill(); <-done }
	addr := "127.0.0.1:" + strconv.Itoa(port)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-done:
			return "", nil, fmt.Errorf("whisper-server exited: %s", lastLine(errBuf.String()))
		default:
		}
		if c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err == nil {
			_ = c.Close()
			return "http://" + addr, stop, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	return "", nil, errors.New("whisper-server did not start within a minute")
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

// openAI posts to an OpenAI-compatible /v1/audio/transcriptions endpoint.
// base may be the server or the full endpoint.
func openAI(ctx context.Context, base string, wav []byte, prompt string) (string, error) {
	url := strings.TrimRight(base, "/")
	if !strings.HasSuffix(url, "/audio/transcriptions") {
		if !strings.HasSuffix(url, "/v1") {
			url += "/v1"
		}
		url += "/audio/transcriptions"
	}
	return post(ctx, url, wav, map[string]string{
		"model":           "whisper-1",
		"response_format": "json",
		"prompt":          prompt,
	})
}

func post(ctx context.Context, url string, wav []byte, fields map[string]string) (string, error) {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	_, _ = fw.Write(wav)
	for k, v := range fields {
		if v != "" {
			_ = mw.WriteField(k, v)
		}
	}
	_ = mw.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	res, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("transcription failed: %s %s", res.Status, strings.TrimSpace(string(b)))
	}
	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return "", fmt.Errorf("transcription reply: %v", err)
	}
	return Clean(out.Text), nil
}

// noise is what Whisper writes for audio with no words in it.
var noise = regexp.MustCompile(`\[[^\]]*\]|\((?i:silence|music|blank_audio|inaudible|noise)[^)]*\)|\*[^*]*\*`)

// Clean drops Whisper's non-speech markers and collapses whitespace.
func Clean(s string) string {
	s = noise.ReplaceAllString(s, " ")
	return strings.Join(strings.Fields(s), " ")
}
