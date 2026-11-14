package agentui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/voice"
)

// Voice: the window's widget.Dictator, so every widget.Prose dictates; and spoken replies.

// Dictation stops by itself after this much quiet once you've spoken, or this long with nothing said.
const (
	voiceSilence = 4 * time.Second
	voiceNothing = 6 * time.Second
)

// VoiceConfig swaps dictation's parts; nil ones are the real ones, from the voice.* options.
type VoiceConfig struct {
	Mic         voice.Mic
	Transcriber voice.Transcriber
	Speaker     voice.Speaker
	// Whisper is the engine and its models when there's no Transcriber: voice.New's, in ~/.oriel/whisper, if nil.
	// Its Settings are the voice.* options when nil.
	Whisper *voice.Whisper
}

// Voice is dictation: what the microphone hears, transcribed into the field dictated into, and read aloud.
type Voice struct {
	kit.Disposer
	Config VoiceConfig
	h      plugin.Host

	whisper  *voice.Whisper
	work     work.Work           // a download's watch
	provided registry.Disposable // the Dictator, while voice.dictation is on

	// On the loop.
	dict      *voice.Dictation
	running   []*voice.Dictation // every one not yet known to be over
	gen       int                // counts dictations, so a late callback knows it's stale
	target    ui.ID
	listening bool
	level     float64
	tr        voice.Transcript
	text      string // what it typed: tr's edits applied
	live      int
	notReady  string
	waiting   bool // a start found the model on its way: its toast follows the download
}

var _ widget.Dictator = (*Voice)(nil)

// Manifest loads it on the first use of one of its commands, or a mic's first Start (VoiceEntries).
func (x *Voice) Manifest() plugin.Manifest {
	return plugin.Manifest{ID: "voice", Lazy: kit.LazyOn(x.commands(), nil)}
}

func (x *Voice) str(name string) string {
	s, _ := registry.Option[string](x.h.Options(), name, registry.At{})
	return s
}

func (x *Voice) on(name string) bool {
	b, _ := registry.Option[bool](x.h.Options(), name, registry.At{})
	return b
}

func (x *Voice) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(&x.work)
	x.whisper = x.Config.Whisper
	if x.whisper == nil {
		home, _ := os.UserHomeDir()
		x.whisper = voice.New(nil)
		x.whisper.Dir = filepath.Join(home, ".oriel", "whisper")
	}
	if x.whisper.Settings == nil {
		x.whisper.Settings = func() voice.Settings { return voice.Settings{Model: x.str("voice.model"), URL: x.str("voice.url")} }
	}
	x.Add(registry.DisposeFunc(x.whisper.Close))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(registry.DisposeFunc(x.whisper.OnChange(func() { h.Do(x.refresh) })))
	for _, name := range []string{"voice.dictation", "voice.model", "voice.url"} {
		x.Add(h.Options().Watch(name, func() { h.Do(x.settle) }))
	}
	x.settle()
	x.Add(registry.DisposeFunc(func() {
		x.Stop()
		if x.provided != nil {
			x.provided.Dispose()
		}
		for _, d := range x.running {
			d.Cancel()
			<-d.Done()
		}
	}))
	return nil
}

func voiceOptions() []registry.OptionSpec {
	out := []registry.OptionSpec{
		{Name: "voice.dictation", Type: registry.Bool, Default: true,
			Help: "Dictation into long text fields: the mic in each, or ⌘⇧Space (Ctrl⇧Space elsewhere) in one. Off, no field has a mic."},
		{Name: "voice.model", Type: registry.String, Default: voice.DefaultModel(), Choices: func() []string { return []string{"base.en", "small.en", "medium.en"} },
			Help: "The Whisper model: base.en, small.en, medium.en or a path to a ggml model, downloaded in the background when Oriel starts."},
		{Name: "voice.url", Type: registry.String, Default: "", Validate: func(v any) error {
			if s := v.(string); s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
				return errors.New("the transcription URL must be http:// or https://")
			}
			return nil
		}, Help: "An OpenAI-compatible transcription endpoint used instead of Whisper on this machine: the server, or its /v1/audio/transcriptions."},
		{Name: "voice.recorder", Type: registry.String, Default: "oriel", Choices: func() []string { return append([]string{"oriel"}, voice.RecorderNames()...) },
			Help: "What captures audio: oriel, Oriel itself; or one of " + strings.Join(voice.RecorderNames(), ", ") + "."},
		{Name: "voice.speech", Type: registry.Enum, Values: []string{"system", "piper", "off"}, Default: "system",
			Help: "How replies are spoken: system (say, spd-say or espeak), piper (a voice in ~/.oriel/piper), or off."},
		{Name: "voice.piper_voice", Type: registry.String, Default: "", Choices: func() []string { return voice.Piper{Dir: piperDir()}.Voices() },
			Help: "The Piper voice: a model's name in ~/.oriel/piper. Empty uses the first there."},
	}
	for i := range out {
		out[i].Scopes = registry.ScopeUser
	}
	return out
}

func (x *Voice) commands() []registry.Command {
	return []registry.Command{
		{ID: "voice.download", Title: "Download a Whisper {model} for dictation: base.en, small.en or medium.en; the configured one by default",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.download(a.String("model")) }},
		{ID: "voice.speak", Title: "Say {text} out loud, as voice.speech says", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.speak(a.String("text"))
		}},
	}
}

// settle provides the Dictator while voice.dictation is on, starting the model's download in the background.
func (x *Voice) settle() {
	on := x.on("voice.dictation")
	switch {
	case on && x.provided == nil:
		x.provided = plugin.ProvideUI[widget.Dictator](x.h, x)
	case !on && x.provided != nil:
		x.Stop()
		x.provided.Dispose()
		x.provided = nil
	}
	if on && x.Config.Transcriber == nil {
		plugin.Go(x.h, "the Whisper model's prefetch", func() {
			x.whisper.Prefetch()
			x.h.Do(x.refresh)
		})
	}
	x.refresh()
}

// refresh reads whether the model is ready, and keeps a waiting start's toast up to date.
func (x *Voice) refresh() {
	if x.Config.Transcriber != nil {
		x.notReady = ""
		return
	}
	r := x.whisper.Readiness()
	x.notReady = ""
	if r.State != voice.Ready {
		x.notReady = r.String()
	}
	if !x.waiting {
		return
	}
	switch r.State {
	case voice.Downloading:
		x.h.Notify(core.Notice{ID: "voice.ready", Severity: core.SevInfo, Title: "Preparing voice", Text: r.String()})
	case voice.Ready:
		x.waiting = false
		x.h.Notify(core.Notice{ID: "voice.ready", Severity: core.SevInfo, Title: "Voice", Text: "Dictation is ready: click the mic in a text field."})
	default:
		x.waiting = false
		x.h.Notify(core.Notice{ID: "voice.ready", Severity: core.SevError, Title: "Voice", Text: r.String()})
	}
}

func (x *Voice) mic() voice.Mic {
	if m := x.Config.Mic; m != nil {
		return m
	}
	return voice.Microphone{Recorder: kit.Unless(x.str("voice.recorder"), "oriel")}
}

func (x *Voice) transcriber() voice.Transcriber {
	if t := x.Config.Transcriber; t != nil {
		return t
	}
	return x.whisper
}

func (x *Voice) piper() voice.Piper {
	return voice.Piper{Dir: piperDir(), Voice: x.str("voice.piper_voice")}
}

func piperDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oriel", "piper")
}

// vocab is the words Whisper should spell Oriel's way: its name and the projects of the session in front.
func (x *Voice) vocab() string {
	words := []string{"Oriel"}
	if se, ok := x.h.State().Session(x.h.State().Focus); ok {
		for _, p := range se.Snapshot().Projects {
			if !slices.Contains(words, p.Name) && len(words) < 40 {
				words = append(words, p.Name)
			}
		}
	}
	return strings.Join(words, ", ") + "."
}

// Start dictates into target: a widget.Prose's mic or shortcut. Until the model is ready it says why, starting or
// retrying its download, and records nothing.
func (x *Voice) Start(target ui.ID) error {
	x.Stop()
	if x.Config.Transcriber == nil {
		if r := x.whisper.Prepare(); r.State != voice.Ready {
			x.notReady = r.String()
			x.waiting = r.State == voice.Downloading
			n := core.Notice{ID: "voice.ready", Severity: core.SevInfo, Title: "Preparing voice", Text: r.String()}
			if !x.waiting {
				n.Severity, n.Title, n.AutoClear = core.SevError, "Voice", true
			}
			x.tell(n)
			return errors.New(r.String())
		}
	}
	s, err := x.mic().Open(x.work.Ctx())
	if err != nil {
		x.tell(x.problem(err, true))
		return err
	}
	x.running = slices.DeleteFunc(x.running, func(d *voice.Dictation) bool {
		select {
		case <-d.Done():
			return true
		default:
			return false
		}
	})
	x.gen++
	gen := x.gen
	x.target, x.listening, x.level, x.tr, x.text, x.live = target, true, 0, voice.Transcript{}, "", 0
	mine := func(fn func()) {
		x.h.Do(func() {
			if x.gen == gen && x.listening {
				fn()
			}
		})
	}
	x.dict = voice.Dictate(s, voice.ListenOpts{Transcriber: x.transcriber(), Prompt: x.vocab(),
		EndAfterSilence: voiceSilence, StartTimeout: voiceNothing,
		OnLevel: func(l float64) { mine(func() { x.level = l }) },
		OnText:  func(text string, final bool) { mine(func() { x.pass(text, final) }) },
		OnEnd:   func(_, _ string, err error) { mine(func() { x.ended(err) }) }})
	x.running = append(x.running, x.dict)
	return nil
}

// Stop ends the dictation; the field keeps what it shows.
func (x *Voice) Stop() {
	if x.dict != nil {
		x.dict.Cancel()
	}
	x.dict, x.listening, x.level = nil, false, 0
	x.gen++
}

// State is target's dictation as its field shows it.
func (x *Voice) State(target ui.ID) widget.DictationState {
	if target != x.target || x.gen == 0 {
		return widget.DictationState{NotReady: x.notReady}
	}
	return widget.DictationState{Listening: x.listening, Level: x.level, Text: x.text, Live: x.live, NotReady: x.notReady}
}

// Dictating reports whether a dictation is listening.
func (x *Voice) Dictating() bool { return x.listening }

// pass applies one pass at the phrase to the text.
func (x *Voice) pass(text string, final bool) {
	e := x.tr.Pass(text, final)
	r := []rune(x.text)
	x.text = string(r[:max(0, len(r)-e.Delete)]) + e.Insert
	x.live = min(e.Live, utf8.RuneCountInString(x.text))
}

func (x *Voice) ended(err error) {
	x.dict, x.listening, x.level, x.live = nil, false, 0, 0
	if err != nil {
		x.h.Notify(x.problem(err, false))
	}
}

// tell notifies from a widget's build, after it.
func (x *Voice) tell(n core.Notice) { x.h.Do(func() { x.h.Notify(n) }) }

// problem is why voice didn't work, with the way to fix it when there's one. autoClear is for a failure in answer to
// what the user just did; one offering a fix stays, for them to take it.
func (x *Voice) problem(err error, autoClear bool) core.Notice {
	n := core.Notice{ID: "voice", Severity: core.SevError, Title: "Voice", Text: err.Error()}
	var noModel *voice.NoModelError
	switch {
	case errors.Is(err, voice.ErrMicDenied):
		n.ID = "voice.mic"
		if runtime.GOOS == "darwin" {
			n.Actions = []core.Action{{Title: "Open Privacy settings", Command: "system.open", Args: map[string]any{"url": voice.PrivacySettings}}}
		}
	case errors.As(err, &noModel):
		n.Text = "Dictation needs the Whisper model " + noModel.Name + " (a one-off download)."
		n.Actions = []core.Action{{Title: "Download", Command: "voice.download", Args: map[string]any{"model": noModel.Name}}}
	}
	n.AutoClear = autoClear && len(n.Actions) == 0
	return n
}

func (x *Voice) download(model string) error {
	if model == "" {
		_, model = x.whisper.ModelPath(x.str("voice.model"))
	}
	if _, err := x.whisper.Download(model); err != nil {
		return err
	}
	x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevInfo, Title: "Voice", Text: "Downloading the Whisper model " + model + "…"})
	x.work.Loop(func(ctx context.Context) {
		for {
			st, _ := x.whisper.DownloadState(model)
			if st.Done {
				if st.Error != "" {
					x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevError, Title: "Voice", Text: "The download failed: " + st.Error})
				} else {
					x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevInfo, Title: "Voice",
						Text: "The Whisper model " + model + " is ready: click the mic in a text field to dictate."})
				}
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	})
	return nil
}

func (x *Voice) speaker() (voice.Speaker, bool) {
	if s := x.Config.Speaker; s != nil {
		return s, x.str("voice.speech") != "off"
	}
	switch x.str("voice.speech") {
	case "off":
		return nil, false
	case "piper":
		return x.piper(), true
	}
	return voice.System{}, true
}

func (x *Voice) speak(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("nothing to say")
	}
	s, ok := x.speaker()
	if !ok {
		return errors.New("speech is off: voice.speech")
	}
	plugin.Go(x.h, "speech", func() {
		if err := s.Speak(context.Background(), text); err != nil {
			x.h.Notify(x.problem(err, false))
		}
	})
	return nil
}
