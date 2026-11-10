package voice_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/voice"
	"github.com/liamg/oriel/voice/voicetest"
)

type pass struct {
	text  string
	final bool
}

type ended struct {
	reason, committed string
	err               error
}

func dictate(t *testing.T, mic *voicetest.Mic, tr voice.Transcriber, o voice.ListenOpts) (*voice.Dictation, func() ([]pass, ended)) {
	t.Helper()
	s, err := mic.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var passes []pass
	end := make(chan ended, 1)
	o.Transcriber = tr
	o.OnText = func(text string, final bool) {
		mu.Lock()
		passes = append(passes, pass{text, final})
		mu.Unlock()
	}
	o.OnEnd = func(reason, committed string, err error) { end <- ended{reason, committed, err} }
	d := voice.Dictate(s, o)
	return d, func() ([]pass, ended) {
		t.Helper()
		select {
		case e := <-end:
			mu.Lock()
			defer mu.Unlock()
			return passes, e
		case <-time.After(5 * time.Second):
			t.Fatal("the dictation never ended")
			return nil, ended{}
		}
	}
}

func finals(ps []pass) []string {
	var out []string
	for _, p := range ps {
		if p.final {
			out = append(out, p.text)
		}
	}
	return out
}

// crit's voice.js: "the current phrase is re-transcribed about once a second and replaced in place; a pause commits
// it and starts the next."
func TestDictationCommitsAPhraseAtEachPause(t *testing.T) {
	mic := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(1200*time.Millisecond),
		voicetest.Silence(700*time.Millisecond), voicetest.Speech(400*time.Millisecond), voicetest.Silence(100*time.Millisecond))}
	_, wait := dictate(t, mic, &voicetest.Transcriber{}, voice.ListenOpts{Prompt: "Oriel."})
	ps, e := wait()
	if e.reason != "ended" || e.err != nil {
		t.Fatalf("ended %q, %v", e.reason, e.err)
	}
	if got, want := finals(ps), []string{"la la la la la la", "la la"}; !slices.Equal(got, want) {
		t.Errorf("committed phrases %q, want %q", got, want)
	}
	if e.committed != "la la la la la la la la" {
		t.Errorf("committed %q", e.committed)
	}
	if !slices.ContainsFunc(ps, func(p pass) bool { return !p.final }) {
		t.Error("the live phrase was never transcribed before its pause")
	}
}

func TestCancelDropsTheLivePhrase(t *testing.T) {
	mic := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(300*time.Millisecond)), Hold: true}
	var heard sync.WaitGroup
	heard.Add(1)
	d, wait := dictate(t, mic, &voicetest.Transcriber{}, voice.ListenOpts{OnState: func(string) { heard.Done() }})
	heard.Wait()
	d.Cancel()
	ps, e := wait()
	if e.reason != "cancelled" || len(finals(ps)) != 0 || e.committed != "" {
		t.Errorf("cancelled: %q, finals %q, committed %q", e.reason, finals(ps), e.committed)
	}
}

func TestStopTakesALastPass(t *testing.T) {
	mic := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(400*time.Millisecond)), Hold: true}
	var heard sync.WaitGroup
	heard.Add(1)
	d, wait := dictate(t, mic, &voicetest.Transcriber{}, voice.ListenOpts{OnState: func(string) { heard.Done() }})
	heard.Wait() // speech has begun: what's heard so far is the last pass
	d.Stop()
	ps, e := wait()
	if e.reason != "stopped" || len(finals(ps)) != 1 {
		t.Errorf("stopped: %q, finals %q", e.reason, finals(ps))
	}
}

func TestDictationEnds(t *testing.T) {
	quiet := &voicetest.Mic{Script: voicetest.Silence(2 * time.Second), Hold: true}
	_, wait := dictate(t, quiet, &voicetest.Transcriber{}, voice.ListenOpts{StartTimeout: time.Second})
	if _, e := wait(); e.reason != "timeout" {
		t.Errorf("no speech: %q, want timeout", e.reason)
	}
	talk := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(400*time.Millisecond),
		voicetest.Silence(2*time.Second)), Hold: true}
	_, wait = dictate(t, talk, &voicetest.Transcriber{}, voice.ListenOpts{EndAfterSilence: time.Second})
	if _, e := wait(); e.reason != "silence" || e.committed != "la la" {
		t.Errorf("after speech, then quiet: %q %q", e.reason, e.committed)
	}
	_, wait = dictate(t, talk, &voicetest.Transcriber{Err: errors.New("whisper-server exited")}, voice.ListenOpts{})
	if _, e := wait(); e.reason != "error" || e.err == nil || !strings.Contains(e.err.Error(), "whisper-server exited") {
		t.Errorf("a failed transcription: %q %v", e.reason, e.err)
	}
}

// The recorder is a program writing raw 16 kHz s16le to stdout; Oriel reads it in 20 ms frames.
func TestRecorderReadsFramesFromItsProgram(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	var ran []string
	r := voice.Recorder{Name: "rec", LookPath: func(name string) (string, error) { return "/bin/" + name, nil },
		Command: func(ctx context.Context, name string, args ...string) *exec.Cmd {
			ran = append([]string{name}, args...)
			return exec.CommandContext(ctx, sh, "-c", "head -c 1920 /dev/zero")
		}}
	s, err := r.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for f := range s.Frames() {
		if len(f) != voice.FrameSamples {
			t.Errorf("a frame of %d samples", len(f))
		}
		n++
	}
	if n != 3 || s.Err() != nil || s.Label() != "rec" {
		t.Errorf("%d frames, err %v, label %q", n, s.Err(), s.Label())
	}
	if want := "/bin/rec -q -t raw -r 16000 -e signed-integer -b 16 -c 1 -"; strings.Join(ran, " ") != want {
		t.Errorf("ran %q, want %q", strings.Join(ran, " "), want)
	}

	r.Command = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, sh, "-c", "echo 'no default input device' >&2; exit 1")
	}
	s, err = r.Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for range s.Frames() {
	}
	if err := s.Err(); err == nil || !strings.Contains(err.Error(), "no default input device") {
		t.Errorf("a recorder that fails says why: %v", err)
	}

	none := voice.Recorder{LookPath: func(string) (string, error) { return "", exec.ErrNotFound }}
	if _, err := none.Open(context.Background()); !errors.Is(err, voice.ErrNoRecorder) || none.Installed() {
		t.Errorf("with no recorder: %v", err)
	}
}

func TestEncodeWAV(t *testing.T) {
	b := voice.EncodeWAV([][]float32{{0, 1, -1}})
	if len(b) != 50 || string(b[:4]) != "RIFF" || string(b[8:16]) != "WAVEfmt " || string(b[36:40]) != "data" {
		t.Fatalf("header %q", b[:44])
	}
	if b[46] != 0xff || b[47] != 0x7f || b[48] != 0x00 || b[49] != 0x80 {
		t.Errorf("samples % x", b[44:])
	}
}

// crit's whisper.go openAI: "posts to an OpenAI-compatible /v1/audio/transcriptions endpoint. base may be the
// server or the full endpoint." Clean drops Whisper's non-speech markers.
func TestAnOpenAICompatibleEndpoint(t *testing.T) {
	var path, model, prompt string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, model, prompt = r.URL.Path, r.FormValue("model"), r.FormValue("prompt")
		_, _ = w.Write([]byte(`{"text":"[BLANK_AUDIO] Fix the  test. (silence)"}`))
	}))
	defer srv.Close()
	w := voice.New(func() voice.Settings { return voice.Settings{URL: srv.URL} })
	if !w.Installed() {
		t.Error("an endpoint is all transcription needs")
	}
	text, err := w.Transcribe(context.Background(), voice.EncodeWAV(nil), "Oriel.")
	if err != nil {
		t.Fatal(err)
	}
	if text != "Fix the test." || path != "/v1/audio/transcriptions" || model != "whisper-1" || prompt != "Oriel." {
		t.Errorf("%q from %s (model %q, prompt %q)", text, path, model, prompt)
	}
}

func TestASpokenReply(t *testing.T) {
	var ran [][]string
	look := func(name string) (string, error) {
		if name == "say" || name == "spd-say" || name == "piper" || name == "afplay" || name == "paplay" {
			return "/bin/" + name, nil
		}
		return "", exec.ErrNotFound
	}
	record := func(_ context.Context, stdin, name string, args ...string) error {
		ran = append(ran, append([]string{stdin, filepath.Base(name)}, args...))
		return nil
	}
	if err := (voice.System{LookPath: look, Run: record}).Speak(context.Background(), "Done."); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 1 || ran[0][0] != "Done." || (ran[0][1] != "say" && ran[0][1] != "spd-say") {
		t.Errorf("the system voice ran %q", ran)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "amy.onnx"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	ran = nil
	p := voice.Piper{Dir: dir, LookPath: look, Run: record}
	if v := p.Voices(); !slices.Equal(v, []string{"amy"}) {
		t.Errorf("voices %v", v)
	}
	if err := p.Speak(context.Background(), "Done."); err != nil {
		t.Fatal(err)
	}
	if len(ran) != 2 || ran[0][1] != "piper" || ran[0][3] != filepath.Join(dir, "amy.onnx") || ran[1][1] != "afplay" {
		t.Errorf("piper ran %q", ran)
	}
}
