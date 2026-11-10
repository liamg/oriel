// Package voice is dictation: audio from the microphone (capture.go, or a recorder it runs: mic.go), split into
// phrases at its pauses (listen.go) and turned into text by whisper.cpp, built in (native.go) or as a whisper-server it
// runs, or by an OpenAI-compatible endpoint; and speech for replies (speak.go). Ported from crit's internal/voice and
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
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

// Whisper keeps one model resident, loaded on first use and dropped when idle: in the whisper.cpp built into Oriel,
// else in a whisper-server it runs.
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
	// Metered says the OS counts the network as metered; the background download waits for another network.
	Metered func() bool
	// Net is what models are downloaded with; a client with connect and header timeouts if nil.
	Net *http.Client
	// Clock times how long the model has gone unused; the system's when nil.
	Clock clock.Clock

	load func(path string) (engine, error) // loadEngine, swapped in tests

	reqMu   sync.Mutex // one transcription at a time
	mu      sync.Mutex
	url     string // the server's, while one runs
	eng     engine // the model in this process, while loaded
	model   string
	gen     int // counts loads, so a reaper knows its own
	stop    func()
	lastUse time.Time

	dlMu      sync.Mutex
	dl        map[string]*download
	listeners map[int]func()
	nextL     int
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
		Metered:  Metered,
		load:     loadEngine,
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
	if w.Settings().URL != "" || w.load != nil {
		return true
	}
	_, err := w.LookPath("whisper-server")
	return err == nil
}

// DefaultModel is the model an empty voice.model means, on every machine.
func DefaultModel() string { return "base.en" }

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
	base, eng, err := w.ensure(st.Model)
	if err != nil {
		return "", err
	}
	var text string
	if eng != nil {
		var samples []float32
		if samples, err = decodeWAV(wav); err == nil {
			text, err = eng.transcribe(ctx, samples, prompt)
			text = Clean(text)
		}
	} else {
		text, err = post(ctx, base+"/inference", wav, map[string]string{
			"response_format": "json",
			"temperature":     "0.0",
			"prompt":          prompt,
		})
	}
	w.mu.Lock()
	w.lastUse = clock.Or(w.Clock).Now()
	w.mu.Unlock()
	return text, err
}

// ensure loads the configured model, in this process or in a whisper-server, or reuses the one loaded: the server's
// URL, or the engine.
func (w *Whisper) ensure(setting string) (string, engine, error) {
	path, name := w.ModelPath(setting)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stop != nil && w.model == path {
		w.lastUse = clock.Or(w.Clock).Now()
		return w.url, w.eng, nil
	}
	bin := ""
	if w.load == nil {
		var err error
		if bin, err = w.LookPath("whisper-server"); err != nil {
			return "", nil, ErrNoBinary
		}
	}
	if _, err := os.Stat(path); err != nil {
		if st, ok := w.DownloadState(name); ok && !st.Done {
			return "", nil, &DownloadingError{Status: st}
		}
		return "", nil, &NoModelError{Name: name, Path: path}
	}
	w.unload()
	if w.load != nil {
		eng, err := w.load(path)
		if err != nil {
			return "", nil, err
		}
		w.eng, w.stop = eng, eng.close
	} else {
		url, stop, err := w.Spawn(bin, path)
		if err != nil {
			return "", nil, err
		}
		w.url, w.stop = url, stop
	}
	w.gen++
	w.model, w.lastUse = path, clock.Or(w.Clock).Now()
	w.reapAfter(w.gen)
	return w.url, w.eng, nil
}

// unload drops the model loaded; w.mu is held.
func (w *Whisper) unload() {
	if w.stop != nil {
		w.stop()
	}
	w.url, w.eng, w.stop, w.model = "", nil, nil, ""
}

// reapAfter looks again in a tenth of Idle, and drops load gen's model once it has gone unused for Idle.
func (w *Whisper) reapAfter(gen int) {
	c := clock.Or(w.Clock)
	c.AfterFunc(max(w.Idle/10, 10*time.Millisecond), func() {
		if !w.reqMu.TryLock() {
			w.reapAfter(gen) // transcribing
			return
		}
		defer w.reqMu.Unlock()
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.gen != gen || w.stop == nil {
			return
		}
		if c.Now().Sub(w.lastUse) >= w.Idle {
			w.unload()
			return
		}
		w.reapAfter(gen)
	})
}

// Running reports whether a model is loaded.
func (w *Whisper) Running() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.stop != nil
}

// Close drops the model.
func (w *Whisper) Close() {
	if w == nil {
		return
	}
	w.reqMu.Lock()
	defer w.reqMu.Unlock()
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unload()
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
