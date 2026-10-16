package app

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/liamg/oriel/agent/prompts"
	"github.com/liamg/oriel/registry"
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

func voiceScene(t *testing.T, mic *voicetest.Mic, tr *voicetest.Transcriber) (*errScene, *voicePlugin, *saidSpeaker) {
	t.Helper()
	sp := &saidSpeaker{}
	s := newSceneWith(t, "", "", mockupSize, func(cfg *Config) {
		cfg.Voice = VoiceConfig{Mic: mic, Transcriber: tr, Speaker: sp}
	})
	var x *voicePlugin
	for _, e := range s.plugins {
		if v, ok := e.(*voicePlugin); ok {
			x = v
		}
	}
	return s, x, sp
}

func (s *errScene) promptsPage() *promptsPlugin {
	for _, e := range s.plugins {
		if x, ok := e.(*promptsPlugin); ok {
			return x
		}
	}
	return nil
}

func (s *errScene) value(label string) string {
	if n := s.access(label); n != nil {
		return n.Value
	}
	return ""
}

// crit-reference.md, "Voice": "Dictation with Ctrl+Shift+Space". crit's voice.js: "Ctrl+Shift+Space in any text
// box records, and the words appear at the cursor as you speak … a pause commits it and starts the next."
func TestDictationTypesIntoTheFocusedField(t *testing.T) {
	mic := &voicetest.Mic{Name: "rec", Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond),
		voicetest.Speech(1200*time.Millisecond), voicetest.Silence(700*time.Millisecond), voicetest.Speech(400*time.Millisecond),
		voicetest.Silence(700*time.Millisecond))}
	tr := &voicetest.Transcriber{}
	s, x, _ := voiceScene(t, mic, tr)
	s.run("prompts.open", registry.Args{"id": prompts.StyleID})
	s.frame()
	s.frame()
	before := s.value("Coding style text")
	s.keyCommand(t, "<C-S-Space>")
	if x.dict == nil {
		t.Fatal("⌃⇧Space in a text field didn't start dictating")
	}
	s.frame()
	s.wantFrame(t, "● listening · rec")
	s.until(t, "both phrases typed", func() bool {
		return strings.Count(s.value("Coding style text"), "la") == 8 && x.typed == 0 && x.phrases == 2
	})
	got := s.value("Coding style text")
	if !strings.Contains(got, "la la la la la la la la") || strings.ReplaceAll(got, "la la la la la la la la", "") != before {
		t.Errorf("the field reads %q, want the words dictated where the cursor was in %q", got, before)
	}
	if p := tr.Prompts[0]; !strings.HasPrefix(p, "Oriel") {
		t.Errorf("Whisper's vocabulary is %q", p)
	}
	s.keyCommand(t, "<C-S-Space>")
	s.until(t, "dictation to stop", func() bool { return x.dict == nil })
	s.wantFrame(t, "not saved")
	if strings.Contains(s.frameText(), "listening") {
		t.Error("the badge stayed up")
	}
}

func TestDictationNeedsATextField(t *testing.T) {
	s, x, _ := voiceScene(t, &voicetest.Mic{}, &voicetest.Transcriber{})
	if err := s.reg.Run(context.Background(), "voice.dictate", nil); err == nil || !strings.Contains(err.Error(), "text field") {
		t.Errorf("with no text field focused: %v", err)
	}
	if x.dict != nil {
		t.Error("it dictated anyway")
	}
	s.reg.Options().Set("voice.dictation", registry.LayerUser, registry.At{}, false)
	s.run("prompts.open", registry.Args{"id": prompts.StyleID})
	s.frame()
	s.frame()
	if err := s.reg.Run(context.Background(), "voice.dictate", nil); err == nil || !strings.Contains(err.Error(), "off") {
		t.Errorf("turned off: %v", err)
	}
}

// Escape drops the phrase being heard: what it typed of it is taken back.
func TestEscapeCancelsDictation(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(1500*time.Millisecond))}
	s, x, _ := voiceScene(t, mic, &voicetest.Transcriber{})
	s.run("prompts.open", registry.Args{"id": prompts.StyleID})
	s.frame()
	s.frame()
	before := s.value("Coding style text")
	s.keyCommand(t, "<C-S-Space>")
	s.until(t, "the live phrase typed", func() bool { return x.typed > 0 })
	s.keyCommand(t, "<Esc>")
	s.until(t, "dictation to end", func() bool { return x.dict == nil })
	s.frame()
	if got := s.value("Coding style text"); got != before {
		t.Errorf("after Escape the field reads %q, want %q", got, before)
	}
	if !s.promptsPage().open {
		t.Error("Escape while dictating closed the page too")
	}
}

// crit-reference.md, "Voice": "an optional wake word, spoken replies". Off by default; on, what follows the word
// goes to a VoiceWake hook that takes it, or else to the omnibox with no session in front.
func TestTheWakeWord(t *testing.T) {
	mic := &voicetest.Mic{Hold: true, Script: voicetest.Script(voicetest.Silence(1200*time.Millisecond), voicetest.Speech(2*time.Second),
		voicetest.Silence(time.Second))}
	tr := &voicetest.Transcriber{Word: "jobs", Text: func(heard string) string { return "Computer, open " + heard }}
	s, x, _ := voiceScene(t, mic, tr)
	s.frame()
	if mic.Opens() != 0 {
		t.Fatal("the wake word listens though it's off")
	}
	s.reg.Options().Set("voice.wake", registry.LayerUser, registry.At{}, true)
	s.until(t, "the omnibox to open with the request", func() bool {
		return strings.Contains(s.frameText(), "open jobs jobs")
	})
	s.reg.Options().Set("voice.wake", registry.LayerUser, registry.At{}, false)

	var mu sync.Mutex
	var took string
	s.reg.Hook(registry.Hook{Event: "VoiceWake", Kind: registry.Decide, Label: "an assistant", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		mu.Lock()
		took = ev.(*VoiceWake).Text
		mu.Unlock()
		return registry.Verdict{Decided: true}, nil
	}})
	s.reg.Options().Set("voice.wake", registry.LayerUser, registry.At{}, true)
	s.until(t, "the hook to take the request", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.HasPrefix(took, "open jobs")
	})
	_ = x
}

func TestSpokenReplies(t *testing.T) {
	s, x, sp := voiceScene(t, &voicetest.Mic{}, &voicetest.Transcriber{})
	s.run("voice.speak", registry.Args{"text": "The tests pass."})
	s.until(t, "it to be said", func() bool { return len(sp.words()) == 1 })
	if sp.words()[0] != "The tests pass." {
		t.Errorf("said %q", sp.words())
	}
	s.reg.Options().Set("voice.speech", registry.LayerUser, registry.At{}, "off")
	if err := s.reg.Run(context.Background(), "voice.speak", registry.Args{"text": "x"}); err == nil {
		t.Error("speech is off, yet it spoke")
	}
	var _ voice.Speaker = sp
	_ = x
}
