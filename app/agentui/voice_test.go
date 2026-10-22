package agentui_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/app/agentui"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/voice"
	"github.com/liamg/oriel/voice/voicetest"
)

type saidSpeaker struct {
	mu   sync.Mutex
	said []string
}

func (s *saidSpeaker) Speak(_ context.Context, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.said = append(s.said, text)
	return nil
}

func (s *saidSpeaker) words() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.said...)
}

func voiceScene(t *testing.T, cfg agentui.VoiceConfig) (*apptest.Scene, *agentui.Voice, *saidSpeaker) {
	t.Helper()
	sp := &saidSpeaker{}
	cfg.Speaker = sp
	s := apptest.New(t, apptest.Options{Config: func(c *apptest.Config) {
		if cfg.Whisper == nil {
			cfg.Whisper = c.Voice.Whisper
		}
		c.Voice = cfg
	}})
	return s, apptest.PluginOf[*agentui.Voice](s, "voice"), sp
}

// styleText is the Prompts page open on the coding style: a long field.
const styleText = "Coding style text"

func openStyle(s *apptest.Scene) {
	s.Run("prompts.open", registry.Args{"id": prompts.StyleID})
	s.Frame()
	s.Frame()
}

// value is the value of the node labelled label.
func value(s *apptest.Scene, label string) string { return s.Node(label).Value }

func noticed(s *apptest.Scene, text string) bool {
	return slices.ContainsFunc(s.Notices(), func(n string) bool { return strings.Contains(n, text) })
}

// Clicking the mic starts dictation; phrases appear, are corrected, and are committed. As crit's voice.js has it:
// "the words appear at the cursor as you speak … a pause commits it and starts the next."
func TestMicDictatesIntoALongField(t *testing.T) {
	mic := &voicetest.Mic{Name: "rec", Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond),
		voicetest.Speech(1200*time.Millisecond), voicetest.Silence(700*time.Millisecond), voicetest.Speech(400*time.Millisecond),
		voicetest.Silence(700*time.Millisecond))}
	tr := &voicetest.Transcriber{}
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Transcriber: tr})
	openStyle(s)
	before := value(s, styleText)
	s.ClickNode("Dictate")
	if !x.Dictating() {
		t.Fatal("clicking the mic didn't start dictating")
	}
	const said = "la la la la la la la la"
	s.Until("both phrases typed", func() bool { return strings.HasPrefix(value(s, styleText), said) })
	if got := value(s, styleText); got != said+before {
		t.Errorf("the field reads %q, want the words dictated at the cursor, at the start of %q", got, before)
	}
	if p := tr.Prompts[0]; !strings.HasPrefix(p, "Oriel") {
		t.Errorf("Whisper's vocabulary is %q", p)
	}
	s.ClickNode("Stop dictating")
	if x.Dictating() {
		t.Error("clicking the mic again didn't stop it")
	}
	s.WantFrame("not saved")
}

// A key press stops it, and the key is typed.
func TestKeyStopsDictation(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(600*time.Millisecond),
		voicetest.Silence(700*time.Millisecond))}
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Transcriber: &voicetest.Transcriber{}})
	openStyle(s)
	s.ClickNode("Dictate")
	s.Until("the phrase typed", func() bool { return strings.HasPrefix(value(s, styleText), "la la la") })
	s.Type("x")
	s.Frame()
	if x.Dictating() {
		t.Error("a key didn't stop dictating")
	}
	if got := value(s, styleText); !strings.HasPrefix(got, "la la lax") {
		t.Errorf("the field starts %q; want the phrase kept and the key typed", got[:min(len(got), 20)])
	}
}

// 4s with no speech once listening starts stops it; what was said stays.
func TestSilenceEndsDictation(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(600*time.Millisecond),
		voicetest.Silence(4500*time.Millisecond))}
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Transcriber: &voicetest.Transcriber{}})
	openStyle(s)
	s.ClickNode("Dictate")
	s.Until("the silence to end it", func() bool { return !x.Dictating() })
	s.Frame()
	if got := value(s, styleText); !strings.HasPrefix(got, "la la la") {
		t.Errorf("the field starts %q; want the phrase kept", got[:min(len(got), 20)])
	}
	if s.Find("Dictate") == nil {
		t.Error("the mic isn't idle again")
	}
}

// Esc drops the live phrase; the page stays open.
func TestEscapeDropsTheLivePhrase(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(1500*time.Millisecond))}
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Transcriber: &voicetest.Transcriber{}})
	openStyle(s)
	before := value(s, styleText)
	s.ClickNode("Dictate")
	s.Until("the live phrase typed", func() bool { return value(s, styleText) != before })
	s.Type("Esc")
	s.Frame()
	if x.Dictating() {
		t.Error("Escape didn't stop dictating")
	}
	if got := value(s, styleText); got != before {
		t.Errorf("after Escape the field reads %q, want %q", got, before)
	}
	if !s.Shown("prompts") {
		t.Error("Escape while dictating closed the page too")
	}
}

// Starting in a second field stops the first.
func TestOneDictationAtATime(t *testing.T) {
	mic := &voicetest.Mic{Hold: true}
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Transcriber: &voicetest.Transcriber{}})
	s.Frame()
	a, b := ui.ID(1), ui.ID(2)
	if err := x.Start(a); err != nil {
		t.Fatal(err)
	}
	if err := x.Start(b); err != nil {
		t.Fatal(err)
	}
	if x.State(a).Listening || !x.State(b).Listening || mic.Opens() != 2 {
		t.Errorf("listening into the first %v, the second %v, %d opens; want only the second", x.State(a).Listening, x.State(b).Listening, mic.Opens())
	}
	x.Stop()
}

// With no dictation registered (voice off), no mic is drawn.
func TestNoMicWithDictationOff(t *testing.T) {
	s, _, _ := voiceScene(t, agentui.VoiceConfig{Mic: &voicetest.Mic{}, Transcriber: &voicetest.Transcriber{}})
	openStyle(s)
	if s.Find("Dictate") == nil {
		t.Fatal("a long field has no mic")
	}
	s.Registry().Options().Set("voice.dictation", registry.LayerUser, registry.At{}, false)
	s.Until("the mic to go", func() bool { return s.Find("Dictate") == nil })
}

// slowModel serves half a model, then holds the rest until the test ends.
type slowModel struct{ release chan struct{} }

func (m slowModel) RoundTrip(r *http.Request) (*http.Response, error) {
	body := io.MultiReader(strings.NewReader(strings.Repeat("x", 500)), held(m.release))
	return &http.Response{StatusCode: http.StatusOK, ContentLength: 1000, Body: io.NopCloser(body), Request: r}, nil
}

type held chan struct{}

func (h held) Read([]byte) (int, error) {
	<-h
	return 0, errors.New("the test ended")
}

// The model downloads in the background at start; while it's not ready, the toast shows progress and nothing is
// recorded.
func TestModelNotReady(t *testing.T) {
	release := make(chan struct{})
	w := voice.New(nil)
	w.Dir, w.Net = t.TempDir(), &http.Client{Transport: slowModel{release}}
	w.LookPath = func(string) (string, error) { return "whisper-server", nil }
	w.Metered = func() bool { return false }
	mic := &voicetest.Mic{}
	s, _, _ := voiceScene(t, agentui.VoiceConfig{Mic: mic, Whisper: w})
	t.Cleanup(func() { close(release) })
	s.Until("the model prefetched", func() bool {
		st, ok := w.DownloadState(voice.DefaultModel())
		return ok && st.Got == 500
	})
	openStyle(s)
	s.ClickNode("Dictate")
	s.Until("the toast", func() bool {
		return noticed(s, "Preparing voice: Dictation is getting ready: downloading the Whisper model (50%)")
	})
	if mic.Opens() != 0 {
		t.Error("it recorded before the model was ready")
	}
}

// A microphone permission refusal is a toast, which offers the Privacy settings on macOS.
func TestMicDenied(t *testing.T) {
	s, x, _ := voiceScene(t, agentui.VoiceConfig{Mic: &voicetest.Mic{Err: voice.ErrMicDenied}, Transcriber: &voicetest.Transcriber{}})
	openStyle(s)
	s.ClickNode("Dictate")
	s.Until("the toast", func() bool { return noticed(s, "isn't allowed to use the microphone") })
	if x.Dictating() {
		t.Error("it dictates without a microphone")
	}
	ns := s.Store().State().Notifications
	n := ns[slices.IndexFunc(ns, func(n core.Notification) bool { return n.ID == "voice.mic" })]
	if offers := len(n.Actions) == 1 && n.Actions[0].Title == "Open Privacy settings"; offers != (runtime.GOOS == "darwin") {
		t.Errorf("actions %+v on %s", n.Actions, runtime.GOOS)
	}
}

func TestSpokenReplies(t *testing.T) {
	s, _, sp := voiceScene(t, agentui.VoiceConfig{Mic: &voicetest.Mic{}, Transcriber: &voicetest.Transcriber{}})
	s.Run("voice.speak", registry.Args{"text": "The tests pass."})
	s.Until("it to be said", func() bool { return len(sp.words()) == 1 })
	if sp.words()[0] != "The tests pass." {
		t.Errorf("said %q", sp.words())
	}
	s.Registry().Options().Set("voice.speech", registry.LayerUser, registry.At{}, "off")
	if err := s.Registry().Run(context.Background(), "voice.speak", registry.Args{"text": "x"}); err == nil {
		t.Error("speech is off, yet it spoke")
	}
}
