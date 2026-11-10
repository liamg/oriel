package voice

import (
	"context"
	"errors"
	"github.com/liamg/oriel/internal/clock/clocktest"
	"github.com/liamg/oriel/internal/testhook"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeEngine struct {
	mu      sync.Mutex
	got     [][]float32
	prompts []string
	closed  atomic.Bool
}

func (e *fakeEngine) transcribe(_ context.Context, samples []float32, prompt string) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.got = append(e.got, samples)
	e.prompts = append(e.prompts, prompt)
	return "hello [BLANK_AUDIO] there", nil
}

func (e *fakeEngine) close() { e.closed.Store(true) }

// withModel is a Whisper whose base.en is on disk, loaded by load.
func withModel(t *testing.T, load func(string) (engine, error)) *Whisper {
	t.Helper()
	w := &Whisper{Dir: t.TempDir(), Idle: time.Hour, Settings: func() Settings { return Settings{} }, load: load,
		LookPath: func(string) (string, error) { return "", ErrNoBinary }}
	if err := os.WriteFile(filepath.Join(w.Dir, "ggml-base.en.bin"), []byte("model"), 0o644); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestTheBuiltInEngineTranscribesWithNoServer(t *testing.T) {
	eng := &fakeEngine{}
	var loaded []string
	w := withModel(t, func(path string) (engine, error) { loaded = append(loaded, path); return eng, nil })
	if !w.Installed() {
		t.Fatal("not installed with whisper.cpp built in")
	}
	wav := EncodeWAV([][]float32{{0, 0.5, -0.5}})
	for range 2 {
		text, err := w.Transcribe(context.Background(), wav, "Oriel.")
		if err != nil || text != "hello there" {
			t.Fatalf("got %q, %v", text, err)
		}
	}
	if len(loaded) != 1 || loaded[0] != filepath.Join(w.Dir, "ggml-base.en.bin") {
		t.Errorf("loaded %q: want the model once", loaded)
	}
	if s := eng.got[0]; len(s) != 3 || s[1] < 0.49 || s[2] > -0.49 || eng.prompts[0] != "Oriel." {
		t.Errorf("the engine got %v %q", s, eng.prompts)
	}
	w.Close()
	if !eng.closed.Load() || w.Running() {
		t.Error("Close didn't drop the model")
	}
}

func TestTheModelIsDroppedWhenIdle(t *testing.T) {
	eng := &fakeEngine{}
	w := withModel(t, func(string) (engine, error) { return eng, nil })
	c := clocktest.New(time.Unix(0, 0))
	w.Idle, w.Clock = 300*time.Millisecond, c
	if _, err := w.Transcribe(context.Background(), EncodeWAV(nil), ""); err != nil {
		t.Fatal(err)
	}
	c.Advance(200 * time.Millisecond)
	if _, err := w.Transcribe(context.Background(), EncodeWAV(nil), ""); err != nil {
		t.Fatal(err)
	}
	c.Advance(299 * time.Millisecond)
	if eng.closed.Load() {
		t.Fatal("the model was dropped before it had gone unused for Idle")
	}
	c.Advance(w.Idle / 10) // it looks a tenth of Idle apart
	if !eng.closed.Load() || w.Running() {
		t.Fatal("the idle model was never dropped")
	}
	if c.Waiting() != 0 {
		t.Error("still looking once the model was dropped")
	}
}

// waitFor waits for ok, looking again as each download starts, moves or ends.
func waitFor(t *testing.T, w *Whisper, what string, ok func() bool) {
	t.Helper()
	moved := make(chan struct{}, 1)
	remove := w.OnChange(func() {
		select {
		case moved <- struct{}{}:
		default:
		}
	})
	defer remove()
	deadline := time.After(10 * time.Second)
	for !ok() {
		select {
		case <-moved:
		case <-deadline:
			t.Fatalf("never: %s", what)
		}
	}
}

func TestALoadFailureIsTheTranscriptionsError(t *testing.T) {
	w := withModel(t, func(string) (engine, error) { return nil, errors.New("bad model") })
	if _, err := w.Transcribe(context.Background(), EncodeWAV(nil), ""); err == nil || err.Error() != "bad model" {
		t.Fatalf("err %v", err)
	}
}

func TestDecodeWAV(t *testing.T) {
	got, err := decodeWAV(EncodeWAV([][]float32{{0.25, -0.25}, {1}}))
	if err != nil || len(got) != 3 || got[0] < 0.24 || got[1] > -0.24 || got[2] < 0.99 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := decodeWAV([]byte("RIFF....WAVEjunk")); err == nil {
		t.Error("no audio, no error")
	}
}

// slowServer serves model bytes a chunk at a time, recording each request's Range header.
func slowServer(t *testing.T, body []byte) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var ranges []string
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		ranges = append(ranges, r.Header.Get("Range"))
		mu.Unlock()
		http.ServeContent(w, r, "m", time.Time{}, strings.NewReader(string(body)))
	}))
	t.Cleanup(src.Close)
	return src, &ranges
}

func TestReadiness(t *testing.T) {
	model := []byte(strings.Repeat("ggml", 1000))
	src, _ := slowServer(t, model)
	testhook.Set(t, &ModelBase, src.URL)
	testhook.Set(t, &ModelSHA256, map[string]string{"base.en": sum(model)})

	w := &Whisper{Dir: t.TempDir(), Settings: func() Settings { return Settings{} },
		LookPath: func(string) (string, error) { return "", ErrNoBinary }}
	if r := w.Readiness(); r.State != Unavailable || !errors.Is(r.Err, ErrNoBinary) {
		t.Errorf("no engine: %+v", r)
	}
	w.load = func(string) (engine, error) { return &fakeEngine{}, nil }
	r := w.Readiness()
	var nm *NoModelError
	if r.State != NoModel || r.Model != "base.en" || !errors.As(r.Err, &nm) {
		t.Errorf("no model: %+v", r)
	}
	changes := make(chan struct{}, 1000)
	remove := w.OnChange(func() { changes <- struct{}{} })
	defer remove()
	if r := w.Prepare(); r.State != Downloading && r.State != Ready {
		t.Errorf("Prepare didn't start the download: %+v", r)
	}
	waitFor(t, w, "ready", func() bool { return w.Readiness().State == Ready })
	if len(changes) == 0 {
		t.Error("OnChange heard nothing of the download")
	}
	if s := (Readiness{State: Downloading, Download: DownloadStatus{Got: 42, Total: 100}}).String(); s != "Dictation is getting ready: downloading the Whisper model (42%)" {
		t.Errorf("message %q", s)
	}
}

func TestPrefetchIsBackgroundAndSkipsMeteredNetworks(t *testing.T) {
	model := []byte(strings.Repeat("ggml", 4096))
	src, _ := slowServer(t, model)
	testhook.Set(t, &ModelBase, src.URL)
	testhook.Set(t, &ModelSHA256, map[string]string{"base.en": sum(model)})
	testhook.Set(t, &backgroundRate, 1024)
	metered := true
	w := &Whisper{Dir: t.TempDir(), Settings: func() Settings { return Settings{} }, Metered: func() bool { return metered },
		load: func(string) (engine, error) { return &fakeEngine{}, nil }}
	if w.Prefetch() {
		t.Fatal("prefetched on a metered network")
	}
	metered = false
	if !w.Prefetch() {
		t.Fatal("no prefetch")
	}
	time.Sleep(100 * time.Millisecond)
	if st, _ := w.DownloadState("base.en"); st.Done || st.Got > int64(len(model))/2 {
		t.Fatalf("the background download isn't held back: %+v", st)
	}
	// Dictating asks for it: the same download goes full speed.
	w.Prepare()
	waitFor(t, w, "sped up and ready", func() bool { return w.Readiness().State == Ready })
	if w.Prefetch() {
		t.Error("prefetched a model already on disk")
	}
}

func TestADownloadResumesWhereItStopped(t *testing.T) {
	model := []byte(strings.Repeat("0123456789", 100))
	src, ranges := slowServer(t, model)
	testhook.Set(t, &ModelBase, src.URL)
	testhook.Set(t, &ModelSHA256, map[string]string{"base.en": sum(model)})
	w := &Whisper{Dir: t.TempDir()}
	if err := os.WriteFile(filepath.Join(w.Dir, "ggml-base.en.bin.part"), model[:300], 0o644); err != nil {
		t.Fatal(err)
	}
	if st := waitDownload(t, w); st.Error != "" {
		t.Fatal(st.Error)
	}
	if got, _ := os.ReadFile(filepath.Join(w.Dir, "ggml-base.en.bin")); string(got) != string(model) {
		t.Fatal("the resumed model isn't whole")
	}
	if len(*ranges) != 1 || (*ranges)[0] != "bytes=300-" {
		t.Errorf("requests %q: want one from byte 300", *ranges)
	}
}

func TestMicrophoneFallsBackToARecorder(t *testing.T) {
	testhook.Set(t, &openNative, func() (Stream, error) { return nil, errors.New("no sound server") })
	m := Microphone{}
	if _, err := m.Open(context.Background()); err == nil {
		t.Skip("a recorder is installed here")
	} else if !strings.Contains(err.Error(), "no sound server") {
		t.Errorf("with no recorder either, the native error: %v", err)
	}
	testhook.Set(t, &openNative, func() (Stream, error) { return nil, ErrMicDenied })
	if _, err := m.Open(context.Background()); !errors.Is(err, ErrMicDenied) {
		t.Errorf("a refusal isn't retried with a recorder: %v", err)
	}
}

func TestNativeStreamEndsOnClose(t *testing.T) {
	r, w := ioPipe()
	freed := make(chan struct{})
	s := readerStream("test", r, func() { _ = w.Close() }, func() { close(freed) })
	go func() { _, _ = w.Write(make([]byte, FrameSamples*2*3)) }()
	if f := <-s.Frames(); len(f) != FrameSamples {
		t.Fatalf("frame of %d", len(f))
	}
	_ = s.Close()
	for range s.Frames() {
	}
	<-freed
	if s.Err() != nil {
		t.Errorf("closed, but %v", s.Err())
	}
}

func ioPipe() (*os.File, *os.File) {
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}
	return r, w
}
