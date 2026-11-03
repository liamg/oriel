package voice_test

import (
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/voice"
	"github.com/liamg/oriel/voice/voicetest"
)

// field is a text field's text before the cursor, edited the way the transcript says.
type field struct {
	text string
	live int
}

func (f *field) apply(e voice.Edit) {
	r := []rune(f.text)
	f.text = string(r[:len(r)-e.Delete]) + e.Insert
	f.live = e.Live
}

func (f *field) provisional() string {
	r := []rune(f.text)
	return string(r[len(r)-f.live:])
}

func TestTranscriptReplacesTheLivePhraseAndCommits(t *testing.T) {
	var tr voice.Transcript
	f := &field{}
	steps := []struct {
		text  string
		final bool
		want  string
		live  string
	}{
		{"hello", false, "hello", "hello"},
		{"hello world", false, "hello world", "hello world"},
		{"Hello, world.", true, "Hello, world.", ""},
		{"and then", false, "Hello, world. and then", " and then"},
		{"And then?", true, "Hello, world. And then?", ""},
		{"", true, "Hello, world. And then?", ""},
	}
	for i, s := range steps {
		f.apply(tr.Pass(s.text, s.final))
		if f.text != s.want || f.provisional() != s.live {
			t.Fatalf("step %d: field %q live %q, want %q live %q", i, f.text, f.provisional(), s.want, s.live)
		}
	}
	if got := tr.Committed(); got != "Hello, world. And then?" {
		t.Errorf("committed %q", got)
	}
}

func TestTranscriptRetypesOnlyWhatChanged(t *testing.T) {
	var tr voice.Transcript
	tr.Pass("open the café", false)
	e := tr.Pass("open the cafés", false)
	if e.Delete != 0 || e.Insert != "s" {
		t.Errorf("an added letter: %+v", e)
	}
	e = tr.Pass("open the cafe", false)
	if e.Delete != 2 || e.Insert != "e" {
		t.Errorf("a changed accent takes back whole runes: %+v", e)
	}
	if e.Live != utf8.RuneCountInString("open the cafe") {
		t.Errorf("live %d", e.Live)
	}
}

func TestTranscriptDropAndContinue(t *testing.T) {
	tr := voice.Transcript{Continue: true}
	f := &field{text: "Note:"}
	f.apply(tr.Pass("ship it", false))
	if f.text != "Note: ship it" {
		t.Errorf("after text, the first phrase gets a space: %q", f.text)
	}
	f.apply(tr.Drop())
	if f.text != "Note:" || f.live != 0 || tr.LiveText() != "" {
		t.Errorf("Esc takes back the live phrase: %q live %d", f.text, f.live)
	}
}

// Driven the way a text field is: a scripted transcriber's passes, through Dictate, into the transcript.
func TestDictationIntoATranscript(t *testing.T) {
	mic := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(2200*time.Millisecond),
		voicetest.Silence(700*time.Millisecond))}
	// Whisper refines the phrase as more of it is heard: 11 words' worth is all of it.
	say := &voicetest.Transcriber{Text: func(heard string) string {
		switch n := len(strings.Fields(heard)); {
		case n < 6:
			return "fix the"
		case n < 11:
			return "fix the bug"
		}
		return "Fix the bug."
	}}
	var mu sync.Mutex
	var tr voice.Transcript
	f := &field{}
	var seen []string
	done := make(chan struct{})
	voice.Dictate(mustOpen(t, mic), voice.ListenOpts{Transcriber: say,
		OnText: func(text string, final bool) {
			mu.Lock()
			defer mu.Unlock()
			f.apply(tr.Pass(text, final))
			seen = append(seen, f.text)
		},
		OnEnd: func(string, string, error) { close(done) }})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the dictation never ended")
	}
	mu.Lock()
	defer mu.Unlock()
	if f.text != "Fix the bug." || f.live != 0 {
		t.Errorf("field %q live %d (passes %q)", f.text, f.live, seen)
	}
	if len(seen) < 2 || !strings.HasPrefix(seen[0], "fix the") {
		t.Errorf("no provisional pass before the commit: %q", seen)
	}
}

func TestLevelFollowsSpeechAndRestsWhenQuiet(t *testing.T) {
	quiet := &voicetest.Mic{Script: voicetest.Silence(time.Second)}
	var levels []float64
	var mu sync.Mutex
	onLevel := func(l float64) { mu.Lock(); levels = append(levels, l); mu.Unlock() }
	d := voice.Dictate(mustOpen(t, quiet), voice.ListenOpts{Transcriber: &voicetest.Transcriber{}, OnLevel: onLevel})
	<-d.Done()
	if len(levels) != 0 {
		t.Errorf("a quiet mic moved the level: %v", levels)
	}
	talk := &voicetest.Mic{Script: voicetest.Script(voicetest.Silence(300*time.Millisecond), voicetest.Speech(300*time.Millisecond),
		voicetest.Silence(300*time.Millisecond))}
	d = voice.Dictate(mustOpen(t, talk), voice.ListenOpts{Transcriber: &voicetest.Transcriber{}, OnLevel: onLevel})
	<-d.Done()
	mu.Lock()
	defer mu.Unlock()
	peak := 0.0
	for _, l := range levels {
		peak = max(peak, l)
	}
	if peak < 0.5 || levels[len(levels)-1] != 0 || d.Level() != 0 {
		t.Errorf("levels %v: want a peak while talking and 0 after", levels)
	}
	if len(levels) > 2*voice.LevelSteps+2 {
		t.Errorf("the level changed %d times for one steady tone", len(levels))
	}
}

func mustOpen(t *testing.T, m voice.Mic) voice.Stream {
	t.Helper()
	s, err := m.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestScriptedTranscriberSaysItsLines(t *testing.T) {
	say := &voicetest.Transcriber{Say: []string{"one", "two"}}
	var got []string
	for range 3 {
		text, _ := say.Transcribe(t.Context(), nil, "")
		got = append(got, text)
	}
	if strings.Join(got, " ") != "one two two" {
		t.Errorf("said %q", got)
	}
}
