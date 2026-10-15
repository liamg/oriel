package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/voice"
)

// Voice (PL-11, crit-reference.md "Voice"): dictation with ⌃⇧Space into the focused text field, through a recorder
// and whisper.cpp or an OpenAI-compatible endpoint; an optional wake word; spoken replies.
const (
	VoiceKeyMode = "voice"
	voiceHelp    = "agent/voice"
)

// VoiceConfig swaps dictation's parts; nil ones are the real ones, from the voice.* options.
type VoiceConfig struct {
	Mic         voice.Mic
	Transcriber voice.Transcriber
	Speaker     voice.Speaker
}

// VoiceWake is the VoiceWake hook's event: the wake word was heard, and Text is what followed it. A Decide hook
// takes the request (an assistant); a rewrite changes it; a veto drops it.
type VoiceWake struct {
	Text string `json:"text"`
}

var wakeWordRe = regexp.MustCompile(`^[a-z]{2,20}$`)

type voiceExt struct {
	disposer
	sh *shell
	h  ext.Host

	whisper *voice.Whisper
	ctx     context.Context

	// The dictation, on the loop.
	dict    *voice.Dictation
	focus   ui.ID // the field it types into
	typed   int   // runes of the live phrase typed so far
	phrases int   // phrases committed
	label   string
	hearing bool

	mu       sync.Mutex
	waker    *voice.Waker
	awaiting core.SessionID // the session a spoken request went to, whose reply is spoken
}

func (x *voiceExt) Manifest() ext.Manifest { return ext.Manifest{ID: "voice"} }

func (x *voiceExt) opt(name string) any {
	v, _, _ := x.h.Options().Get(name, registry.At{})
	return v
}

func (x *voiceExt) str(name string) string { s, _ := x.opt(name).(string); return s }
func (x *voiceExt) on(name string) bool    { b, _ := x.opt(name).(bool); return b }

func (x *voiceExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	var cancel context.CancelFunc
	x.ctx, cancel = context.WithCancel(context.Background())
	x.add(onDispose(cancel))
	home, _ := os.UserHomeDir()
	x.whisper = voice.New(func() voice.Settings { return voice.Settings{Model: x.str("voice.model"), URL: x.str("voice.url")} })
	x.whisper.Dir = filepath.Join(home, ".oriel", "whisper")
	x.add(onDispose(x.whisper.Close))
	for _, o := range []registry.OptionSpec{
		{Name: "voice.dictation", Type: registry.Bool, Default: true,
			Help: "Dictation: ⌃⇧Space types what you say into the focused text field. It needs a recorder and whisper.cpp's whisper-server, or voice.url."},
		{Name: "voice.model", Type: registry.String, Default: "", Choices: func() []string { return []string{"base.en", "small.en", "medium.en"} },
			Help: "The Whisper model: base.en, small.en, medium.en or a path to a ggml model. Empty picks one for the machine: " + voice.DefaultModel() + " here."},
		{Name: "voice.url", Type: registry.String, Default: "", Validate: func(v any) error {
			if s := v.(string); s != "" && !strings.HasPrefix(s, "http://") && !strings.HasPrefix(s, "https://") {
				return errors.New("the transcription URL must be http:// or https://")
			}
			return nil
		}, Help: "An OpenAI-compatible transcription endpoint used instead of a local whisper-server: the server, or its /v1/audio/transcriptions."},
		{Name: "voice.recorder", Type: registry.String, Default: "", Choices: voice.RecorderNames,
			Help: "The program that captures audio: one of " + strings.Join(voice.RecorderNames(), ", ") + ". Empty uses the first installed."},
		{Name: "voice.wake", Type: registry.Bool, Default: false,
			Help: "Listen for the wake word: said after a pause, what follows it goes to the session in front's agent, and its reply is spoken."},
		{Name: "voice.wake_word", Type: registry.String, Default: voice.DefaultWakeWord, Validate: func(v any) error {
			if !wakeWordRe.MatchString(v.(string)) {
				return errors.New("the wake word is one word of letters, up to 20")
			}
			return nil
		}, Help: "The word that calls Oriel, said after a pause."},
		{Name: "voice.speech", Type: registry.Enum, Values: []string{"system", "piper", "off"}, Default: "system",
			Help: "How replies are spoken: system (say, spd-say or espeak), piper (a voice in ~/.oriel/piper), or off."},
		{Name: "voice.piper_voice", Type: registry.String, Default: "", Choices: func() []string { return x.piper().Voices() },
			Help: "The Piper voice: a model's name in ~/.oriel/piper. Empty uses the first there."},
	} {
		o.Scopes = registry.ScopeUser
		x.add(h.Option(o))
	}
	x.add(h.DeclareEvent(registry.EventInfo{Name: "VoiceWake", Fired: true, Payload: VoiceWake{},
		Doc: "The wake word was heard. A Decide hook takes the request; otherwise it goes to the session in front's agent, or the omnibox."}))
	dictating := func(registry.Args) bool { return x.dict != nil }
	for _, c := range []registry.Command{
		{ID: "voice.dictate", Title: "Dictate into the focused text field, or stop dictating: what you say is typed as you say it", Run: x.toggle},
		{ID: "voice.cancel", Title: "Stop dictating and drop the phrase being heard", Available: dictating,
			Run: func(registry.Ctx, registry.Args) error {
				if x.dict != nil {
					x.dict.Cancel()
				}
				return nil
			}},
		{ID: "voice.download", Title: "Download a Whisper {model} for dictation: base.en, small.en or medium.en; the configured one by default",
			Run: func(_ registry.Ctx, a registry.Args) error { return x.download(a.String("model")) }},
		{ID: "voice.speak", Title: "Say {text} out loud, as voice.speech says", Run: func(_ registry.Ctx, a registry.Args) error {
			return x.speak(a.String("text"))
		}},
	} {
		x.add(h.Command(c))
	}
	keys := []registry.Keymap{
		{Mode: "global", Keys: "<C-S-Space>", Command: "voice.dictate", Desc: "Dictate"},
		{Mode: VoiceKeyMode, Keys: "<C-S-Space>", Command: "voice.dictate", Desc: "Stop dictating"},
		{Mode: VoiceKeyMode, Keys: "<Esc>", Command: "voice.cancel", Desc: "Cancel dictation"},
	}
	if runtime.GOOS == "darwin" {
		keys = append(keys, registry.Keymap{Mode: "global", Keys: "<D-S-Space>", Command: "voice.dictate", Desc: "Dictate"},
			registry.Keymap{Mode: VoiceKeyMode, Keys: "<D-S-Space>", Command: "voice.dictate", Desc: "Stop dictating"})
	}
	for _, k := range keys {
		x.add(h.Keymap(k))
	}
	x.add(h.Contribute(registry.KeyLayer{Name: "voice", Mode: VoiceKeyMode, Active: func() bool { return x.dict != nil }, Source: "builtin:voice"}))
	x.add(h.View(OverlayPrefix+"voice", registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "voice", Fn: x.badge} })))
	x.add(h.Hook(registry.Hook{Event: "AgentTurnEnd", Label: "speak the reply to a spoken request", Fn: func(_ registry.Ctx, ev any) (registry.Verdict, error) {
		if e, ok := ev.(AgentTurnEnd); ok {
			x.replied(core.SessionID(e.Session))
		}
		return registry.Verdict{}, nil
	}}))
	x.add(onDispose(h.Options().Watch("voice.wake", x.listenForWake)))
	x.add(onDispose(h.Options().Watch("voice.wake_word", x.listenForWake)))
	x.add(onDispose(func() {
		x.stopWake()
		if x.dict != nil {
			x.dict.Cancel()
			<-x.dict.Done()
		}
	}))
	x.listenForWake()
	return nil
}

func (x *voiceExt) mic() voice.Mic {
	if m := x.sh.cfg.Voice.Mic; m != nil {
		return m
	}
	return voice.Recorder{Name: x.str("voice.recorder")}
}

func (x *voiceExt) transcriber() voice.Transcriber {
	if t := x.sh.cfg.Voice.Transcriber; t != nil {
		return t
	}
	return x.whisper
}

func (x *voiceExt) piper() voice.Piper {
	home, _ := os.UserHomeDir()
	return voice.Piper{Dir: filepath.Join(home, ".oriel", "piper"), Voice: x.str("voice.piper_voice")}
}

// ready says why dictation can't run here, before the microphone opens.
func (x *voiceExt) ready() error {
	if !x.on("voice.dictation") {
		return errors.New("dictation is off: turn on voice.dictation in Settings")
	}
	if x.sh.cfg.Voice.Transcriber == nil && !x.whisper.Installed() {
		return voice.ErrNoBinary
	}
	return nil
}

// vocab is the words Whisper should spell Oriel's way: its name and the projects of the session in front.
func (x *voiceExt) vocab() string {
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

func (x *voiceExt) toggle(registry.Ctx, registry.Args) error {
	if x.dict != nil {
		x.dict.Stop()
		return nil
	}
	u := x.h.UI()
	if u == nil || !u.TakesText() {
		return errors.New("dictation types into a text field: focus one first")
	}
	if err := x.ready(); err != nil {
		return err
	}
	x.stopWake()
	s, err := x.mic().Open(context.Background())
	if err != nil {
		x.listenForWake()
		return err
	}
	x.focus, x.typed, x.phrases, x.label, x.hearing = u.Focused(), 0, 0, s.Label(), false
	x.dict = voice.Dictate(s, voice.ListenOpts{Transcriber: x.transcriber(), Prompt: x.vocab(),
		OnState: func(string) { x.h.Do(func() { x.hearing = true }); x.h.Redraw() },
		OnText:  func(text string, final bool) { x.h.Do(func() { x.write(text, final) }); x.h.Redraw() },
		OnEnd: func(reason, _ string, err error) {
			x.h.Do(func() { x.ended(reason, err) })
			x.h.Redraw()
		}})
	return nil
}

// write puts a pass at the phrase in the field: the last pass's runes taken back, this one's typed.
func (x *voiceExt) write(text string, final bool) {
	if x.dict == nil {
		return
	}
	u := x.h.UI()
	if u == nil || u.Focused() != x.focus || !u.TakesText() {
		// The field lost focus: dictation never types somewhere else.
		x.dict.Cancel()
		return
	}
	if text != "" && x.phrases > 0 {
		text = " " + text
	}
	for range x.typed {
		u.Key(render.KeyEvent{Key: "BS"})
	}
	for _, r := range text {
		u.Key(render.KeyEvent{Key: string(r)})
	}
	x.typed = utf8.RuneCountInString(text)
	if final {
		if text != "" {
			x.phrases++
		}
		x.typed = 0
	}
}

func (x *voiceExt) ended(reason string, err error) {
	if x.dict == nil {
		return
	}
	if reason == "cancelled" && x.typed > 0 {
		if u := x.h.UI(); u != nil && u.Focused() == x.focus {
			for range x.typed {
				u.Key(render.KeyEvent{Key: "BS"})
			}
		}
	}
	x.dict, x.typed, x.hearing = nil, 0, false
	if err != nil {
		x.problem(err)
	}
	x.listenForWake()
}

// problem tells the user why voice didn't work, with the way to fix it when there's one.
func (x *voiceExt) problem(err error) {
	n := core.Notice{ID: "voice", Severity: core.SevError, Title: "Voice", Key: true, Text: err.Error()}
	var noModel *voice.NoModelError
	if errors.As(err, &noModel) {
		n.Text = "Dictation needs the Whisper model " + noModel.Name + " (a one-off download)."
		n.Actions = []core.Action{{Title: "Download", Command: "voice.download", Args: map[string]any{"model": noModel.Name}}}
	}
	x.h.Notify(n)
}

func (x *voiceExt) download(model string) error {
	if model == "" {
		_, model = x.whisper.ModelPath(x.str("voice.model"))
	}
	if _, err := x.whisper.Download(model); err != nil {
		return err
	}
	x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevInfo, Title: "Voice", Text: "Downloading the Whisper model " + model + "…"})
	go func() {
		for {
			st, _ := x.whisper.DownloadState(model)
			if st.Done {
				if st.Error != "" {
					x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevError, Title: "Voice", Text: "The download failed: " + st.Error})
				} else {
					x.h.Notify(core.Notice{ID: "voice.download", Severity: core.SevInfo, Title: "Voice",
						Text: "The Whisper model " + model + " is ready: ⌃⇧Space dictates."})
				}
				return
			}
			select {
			case <-x.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	return nil
}

func (x *voiceExt) speaker() (voice.Speaker, bool) {
	if s := x.sh.cfg.Voice.Speaker; s != nil {
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

func (x *voiceExt) speak(text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("nothing to say")
	}
	s, ok := x.speaker()
	if !ok {
		return errors.New("speech is off: voice.speech")
	}
	go func() {
		if err := s.Speak(context.Background(), text); err != nil {
			x.problem(err)
		}
	}()
	return nil
}

// The wake word.

func (x *voiceExt) listenForWake() {
	x.stopWake()
	if !x.on("voice.wake") || x.dict != nil || x.ready() != nil {
		return
	}
	s, err := x.mic().Open(context.Background())
	if err != nil {
		x.problem(fmt.Errorf("the wake word can't listen: %w", err))
		return
	}
	w := voice.Wake(s, voice.WakeOpts{Transcriber: x.transcriber(), Word: x.str("voice.wake_word"), Prompt: x.vocab(),
		OnWake:  func(rest string) { x.h.Do(func() { x.woke(rest) }); x.h.Redraw() },
		OnError: func(err error) { x.problem(err) }})
	x.mu.Lock()
	x.waker = w
	x.mu.Unlock()
}

func (x *voiceExt) stopWake() {
	x.mu.Lock()
	w := x.waker
	x.waker = nil
	x.mu.Unlock()
	if w != nil {
		w.Stop()
	}
}

// woke takes what followed the wake word: a VoiceWake hook's, else the agent in front's, else the omnibox's.
func (x *voiceExt) woke(text string) {
	ev := &VoiceWake{Text: text}
	v, _ := x.h.Fire(context.Background(), "VoiceWake", "", ev)
	if v.Veto || v.Decided {
		return
	}
	ctx := context.Background()
	focus := x.h.State().Focus
	if strings.TrimSpace(ev.Text) != "" && focus != "" && x.h.Available("agent.send", nil) {
		if err := x.h.Run(ctx, "agent.send", registry.Args{"text": ev.Text}); err != nil {
			x.problem(err)
			return
		}
		x.mu.Lock()
		x.awaiting = focus
		x.mu.Unlock()
		return
	}
	_ = x.h.Run(ctx, "omnibox.open", registry.Args{"query": ev.Text})
}

// replied speaks the agent's last words when it ends the turn a spoken request started.
func (x *voiceExt) replied(id core.SessionID) {
	x.mu.Lock()
	if x.awaiting != id {
		x.mu.Unlock()
		return
	}
	x.awaiting = ""
	x.mu.Unlock()
	se, ok := x.h.State().Session(id)
	if !ok {
		return
	}
	tr := se.Snapshot().Transcript
	for i := len(tr) - 1; i >= 0; i-- {
		if it := tr[i]; it.Kind == core.ItemAssistant && it.Agent == "" && strings.TrimSpace(it.Text) != "" {
			if _, ok := x.speaker(); ok {
				_ = x.speak(it.Text)
			}
			return
		}
	}
}

// badge is "● listening" by the field dictation types into; red once it hears speech.
func (x *voiceExt) badge(c *ui.Ctx) {
	if x.dict == nil {
		return
	}
	th := c.Theme()
	r, ok := c.Rect(x.focus)
	if !ok {
		return
	}
	at := render.Point{X: r.Max.X - 6, Y: r.Min.Y - 4}
	col := th.C(theme.FgMuted)
	if x.hearing {
		col = th.C(theme.Error)
	}
	text := "● listening"
	if x.label != "" {
		text += " · " + x.label
	}
	c.Box("voicebadge", ui.Style{Pad: ui.PadXY(8, 1), Radius: 9, Fill: th.C(theme.BgOverlay), Stroke: th.C(theme.Line), Border: 1, Z: ui.LayerOverlay,
		Float: &ui.Float{Point: &at, Self: render.Point{X: 1, Y: 1}}, Role: ui.RoleStatus, Label: text}, func(c *ui.Ctx) {
		c.Text("t", text, monoFont(11, col))
	})
}
