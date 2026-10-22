//go:build whisper_native && cgo

package voice

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// The whisper.cpp built in (native/build.sh) transcribes a clip with a real model: ORIEL_WHISPER_MODEL, a ggml
// model such as ggml-tiny.en.bin.
func TestNativeWhisperTranscribes(t *testing.T) {
	model := os.Getenv("ORIEL_WHISPER_MODEL")
	if model == "" {
		t.Skip("ORIEL_WHISPER_MODEL isn't set")
	}
	if !Native() {
		t.Fatal("whisper.cpp is built in, but this CPU can't run it")
	}
	wav, err := os.ReadFile("testdata/fellow-americans.wav")
	if err != nil {
		t.Fatal(err)
	}
	w := New(func() Settings { return Settings{Model: model} })
	w.LookPath = func(string) (string, error) { return "", ErrNoBinary }
	defer w.Close()
	start := time.Now()
	text, err := w.Transcribe(context.Background(), wav, "Oriel.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%q in %s", text, time.Since(start))
	if got := strings.ToLower(text); !strings.Contains(got, "fellow americans") {
		t.Errorf("heard %q", text)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := w.Transcribe(ctx, wav, ""); err == nil {
		t.Error("a cancelled transcription ran anyway")
	}
}
