package widget

import (
	"image/color"
	"slices"
	"testing"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/raster"
	"github.com/liamg/oriel/render/snapshot"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// fakeDictator dictates into one field at a time; a test says what it has heard with hear.
type fakeDictator struct {
	target       ui.ID
	listening    bool
	text         string
	live         int
	level        float64
	starts, stop int
}

func (d *fakeDictator) Start(t ui.ID) error {
	d.target, d.listening, d.text, d.live = t, true, "", 0
	d.starts++
	return nil
}

func (d *fakeDictator) Stop() { d.listening = false; d.stop++ }

func (d *fakeDictator) State(t ui.ID) DictationState {
	if t != d.target {
		return DictationState{}
	}
	return DictationState{Listening: d.listening, Level: d.level, Text: d.text, Live: d.live}
}

func (d *fakeDictator) hear(text string, live int) { d.text, d.live = text, live }

type proseScene struct {
	h      *harness
	d      *fakeDictator
	texts  [2]string
	ids    [2]ui.ID
	res    [2]InputResult
	fields int
}

func newProse(t *testing.T, fields int, d Dictator) *proseScene {
	s := &proseScene{fields: fields}
	s.h = newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		c.Column("col", ui.Style{Pad: ui.Pad(10), W: ui.Px(400), Gap: 10}, func(c *ui.Ctx) {
			for i := range s.fields {
				s.ids[i] = c.Next(i)
				s.res[i] = Prose(c, i, &s.texts[i], ProseOpts{Label: "Prompt", Help: "test/prose"})
			}
		})
	})
	if d != nil {
		ui.Provide(s.h.u, d)
	}
	if fd, ok := d.(*fakeDictator); ok {
		s.d = fd
	}
	s.h.frame()
	return s
}

func (s *proseScene) mic(i int) ui.ID { return ui.Child(s.ids[i], "mic") }

func (s *proseScene) micShown(i int) bool { return s.h.access(s.mic(i)) != nil }

func (s *proseScene) status(i int) string {
	if n := s.h.access(ui.Child(s.ids[i], "status")); n != nil {
		return n.Name
	}
	return ""
}

func (s *proseScene) clickMic(i int) {
	s.h.t.Helper()
	if !s.micShown(i) {
		s.h.t.Fatalf("field %d shows no mic", i)
	}
	s.h.click(mid(s.h.rect(s.mic(i))))
}

// glyphsIn reports whether this frame drew text in c.
func (s *proseScene) glyphsIn(c render.Color) bool {
	for _, op := range s.h.list.Ops {
		if op.Kind == render.OpGlyphs && op.Color == c {
			return true
		}
	}
	return false
}

// The mic is shown when idle, hidden while typing, and back 1.5s after the last key.
func TestProseMicHidesWhileTyping(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	if !s.micShown(0) {
		t.Fatal("an idle field shows no mic")
	}
	s.h.click(mid(s.h.rect(s.ids[0])))
	s.h.keys("h", "i")
	if s.micShown(0) {
		t.Fatal("the mic shows while typing")
	}
	s.h.frame()
	if next := s.h.u.NextFrame(); !next.Equal(s.h.now.Add(micReturn)) {
		t.Errorf("next frame at %v; want 1.5s after the last key, %v", next, s.h.now.Add(micReturn))
	}
	s.h.now = s.h.now.Add(micReturn - time.Millisecond)
	s.h.frame()
	if s.micShown(0) {
		t.Error("the mic is back before 1.5s")
	}
	s.h.now = s.h.now.Add(time.Millisecond)
	s.h.frame()
	if !s.micShown(0) {
		t.Error("the mic isn't back 1.5s after the last key")
	}
	if s.texts[0] != "hi" {
		t.Errorf("text %q; want what was typed", s.texts[0])
	}
}

// With no voice provider registered (voice off, or a plugin replaced it), no mic is drawn.
func TestProseNoMicWithoutDictator(t *testing.T) {
	s := newProse(t, 1, nil)
	s.h.now = s.h.now.Add(time.Minute)
	s.h.frame()
	if s.micShown(0) {
		t.Fatal("a mic shows with no Dictator")
	}
	s.h.click(mid(s.h.rect(s.ids[0])))
	s.h.keys("a", "CR")
	if s.texts[0] != "a\n" {
		t.Errorf("text %q; it should edit as a multi-line field", s.texts[0])
	}
}

// Clicking the mic starts dictation; the live phrase is provisional (fg.muted) and replaced as it's
// corrected, then committed; it lands through the field's edits, so one undo takes the dictation back.
func TestProseDictates(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	s.texts[0] = "Hi"
	s.h.frame()
	s.clickMic(0)
	if s.d.starts != 1 || s.d.target != s.ids[0] {
		t.Fatalf("clicking the mic: %d starts into %v; want one into the field", s.d.starts, s.d.target)
	}
	if s.h.u.Focused() != s.ids[0] {
		t.Error("the field hasn't focus while it's dictated into")
	}
	if got := s.status(0); got != "Listening" {
		t.Errorf("status %q; want Listening read out", got)
	}
	if n := s.h.access(s.mic(0)); n == nil || n.Name != "Stop dictating" {
		t.Errorf("the mic while listening: %+v; want Stop dictating", n)
	}
	muted := col2(s.h, theme.FgMuted)

	s.d.hear("hello word", 10)
	s.h.frame()
	if s.texts[0] != "Hi hello word" {
		t.Fatalf("text %q; want the phrase after a space", s.texts[0])
	}
	if !s.glyphsIn(muted) {
		t.Error("the live phrase isn't drawn muted")
	}
	s.d.hear("hello world", 11)
	s.h.frame()
	if s.texts[0] != "Hi hello world" {
		t.Fatalf("corrected: text %q", s.texts[0])
	}
	s.d.hear("hello world.", 0)
	s.h.frame()
	if s.texts[0] != "Hi hello world." {
		t.Fatalf("committed: text %q", s.texts[0])
	}
	if s.glyphsIn(muted) {
		t.Error("a committed phrase is still drawn muted")
	}
	s.d.hear("hello world. Bye", 4)
	s.h.frame()
	if s.texts[0] != "Hi hello world. Bye" {
		t.Fatalf("second phrase: text %q", s.texts[0])
	}

	s.clickMic(0)
	if s.d.listening || s.d.stop != 1 {
		t.Fatalf("clicking the mic again: listening %v, %d stops", s.d.listening, s.d.stop)
	}
	if got := s.status(0); got != "Stopped" {
		t.Errorf("status %q; want Stopped read out", got)
	}
	if s.texts[0] != "Hi hello world. Bye" {
		t.Errorf("stopped: text %q; it keeps what was heard", s.texts[0])
	}
	if !s.h.u.Key(render.KeyEvent{Key: "z", Mods: platform.primary}) {
		t.Fatal("nothing takes undo")
	}
	s.h.frame()
	if s.texts[0] != "Hi" {
		t.Errorf("undo: text %q; want the dictation taken back in one", s.texts[0])
	}
}

// Any key press stops it, and the key still goes to the field.
func TestProseKeyStopsDictation(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	s.clickMic(0)
	s.d.hear("hello", 5)
	s.h.frame()
	s.h.keys("x")
	if s.d.listening || s.d.stop != 1 {
		t.Fatalf("a key: listening %v, %d stops; want it stopped", s.d.listening, s.d.stop)
	}
	if s.texts[0] != "hellox" {
		t.Fatalf("text %q; want the phrase kept and the key typed", s.texts[0])
	}
	s.d.hear("hello there", 0)
	s.h.frame()
	if s.texts[0] != "hellox" {
		t.Errorf("text %q; nothing more lands once stopped", s.texts[0])
	}
	if s.glyphsIn(col2(s.h, theme.FgMuted)) {
		t.Error("the phrase is still muted once stopped")
	}
}

// Esc stops dictation and drops the phrase still being heard; it doesn't leave the field.
func TestProseEscapeDropsLivePhrase(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	s.clickMic(0)
	s.d.hear("one", 0)
	s.h.frame()
	s.d.hear("one two", 4)
	s.h.frame()
	if s.texts[0] != "one two" {
		t.Fatalf("text %q", s.texts[0])
	}
	s.h.keys("Esc")
	if s.d.listening {
		t.Fatal("Esc didn't stop it")
	}
	if s.texts[0] != "one" {
		t.Errorf("text %q; want the live phrase dropped", s.texts[0])
	}
	if s.res[0].Cancelled || s.h.u.Focused() != s.ids[0] {
		t.Errorf("Esc while dictating cancelled the field (%v) or took its focus (%v)", s.res[0].Cancelled, s.h.u.Focused())
	}
}

// Only one dictation at a time: starting in another field stops the first.
func TestProseOneDictationAtATime(t *testing.T) {
	s := newProse(t, 2, &fakeDictator{})
	s.clickMic(0)
	s.d.hear("first", 0)
	s.h.frame()
	s.h.now = s.h.now.Add(time.Second)
	s.clickMic(1)
	if s.d.target != s.ids[1] || !s.d.listening {
		t.Fatal("the second field isn't dictated into")
	}
	s.d.hear("second", 2)
	s.h.frame()
	if s.texts != [2]string{"first", "second"} {
		t.Errorf("texts %q; each keeps its own", s.texts)
	}
	if s.status(0) != "Stopped" || s.status(1) != "Listening" {
		t.Errorf("statuses %q, %q; want the first stopped", s.status(0), s.status(1))
	}
	if n := s.h.access(s.mic(0)); n == nil || n.Name != "Dictate" {
		t.Errorf("the first field's mic: %+v; want it idle", n)
	}
}

// A dictation that ends by itself (silence) keeps its last phrase; a click elsewhere, which takes focus, stops one.
func TestProseStoppedFromOutside(t *testing.T) {
	s := newProse(t, 2, &fakeDictator{})
	s.clickMic(0)
	s.d.hear("all done", 3)
	s.h.frame()
	s.d.hear("all done.", 0)
	s.d.listening = false
	s.h.frame()
	if s.texts[0] != "all done." || s.status(0) != "Stopped" {
		t.Errorf("silence: text %q, status %q", s.texts[0], s.status(0))
	}

	s.clickMic(0)
	s.d.hear("more", 4)
	s.h.frame()
	s.h.click(mid(s.h.rect(s.ids[1])))
	if s.d.listening {
		t.Error("a click in another field didn't stop it")
	}
	if s.texts[0] != "all done. more" {
		t.Errorf("text %q", s.texts[0])
	}
}

// The shortcut starts and stops dictating in a focused field (⌘⇧Space on macOS, Ctrl⇧Space elsewhere), at the cursor.
func TestProseShortcut(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	s.h.click(mid(s.h.rect(s.ids[0])))
	s.h.keys("a", "b", "Left")
	key := render.KeyEvent{Key: " ", Mods: platform.primary | render.ModShift}
	s.h.u.Key(key)
	s.h.frame()
	if !s.d.listening {
		t.Fatal("the shortcut didn't start it")
	}
	s.d.hear("x", 0)
	s.h.frame()
	if s.texts[0] != "a xb" {
		t.Errorf("text %q; want it dictated at the cursor", s.texts[0])
	}
	s.h.u.Key(key)
	s.h.frame()
	if s.d.listening || s.texts[0] != "a xb" {
		t.Errorf("the shortcut again: listening %v, text %q", s.d.listening, s.texts[0])
	}
}

// The border breathes with the mic level, not on a timer: when the mic is quiet, nothing redraws.
func TestProseListeningIsIdle(t *testing.T) {
	s := newProse(t, 1, &fakeDictator{})
	s.clickMic(0)
	s.h.pointer(render.PointerMove, 0, 590, 390) // off the mic, so no tooltip waits
	s.d.level = .5
	for range 3 {
		s.h.frame()
	}
	if next := s.h.u.NextFrame(); !next.IsZero() {
		t.Errorf("listening at a steady level asks for a frame at %v; want none", next)
	}
	rec := col2(s.h, theme.Recording)
	border := func() (c render.Color, ok bool) {
		for _, op := range s.h.list.Ops {
			if op.Kind == render.OpRect && op.Style.Stroke.A > 0 && op.Style.Stroke.R == rec.R && op.Style.Stroke.G == rec.G && op.Style.Stroke.B == rec.B {
				return op.Style.Stroke, true
			}
		}
		return render.Color{}, false
	}
	quiet, ok := border()
	if !ok {
		t.Fatal("no recording border while listening")
	}
	s.d.level = 1
	s.h.frame()
	if loud, _ := border(); loud.A <= quiet.A {
		t.Errorf("border alpha %d loud, %d at half; want it stronger with the level", loud.A, quiet.A)
	}
}

func col2(h *harness, id theme.TokenID) render.Color { return h.u.Theme().C(id) }

// Two fields: one idle with its mic, one listening with a phrase still being heard.
func TestProseSnapshot(t *testing.T) {
	s := newProse(t, 2, &fakeDictator{})
	s.h.size = render.Point{X: 420, Y: 260}
	s.texts[0] = "Explain why the build fails on Linux"
	s.h.frame()
	s.clickMic(1)
	s.h.pointer(render.PointerMove, 0, 410, 250)
	s.d.level = .6
	s.d.hear("Add a test for the empty case and then", 8)
	s.h.frame()
	s.h.frame()
	snapshot.GoldenPNG(t, "testdata/prose.png", raster.Render(s.h.list, s.h.size, 1, resolved(t, theme.Dark).C(theme.BgBase)), 0.02, 0.001)
}

// A field scrolled out of its view takes its mic with it: nothing of it is drawn or hit outside the view.
func TestProseMicClippedToScroll(t *testing.T) {
	bar := render.Color{R: 200, G: 30, B: 30, A: 255}
	var text string
	var field, view ui.ID
	h := newHarness(t, theme.Dark, render.Point{X: 420, Y: 200}, func(c *ui.Ctx) {
		c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Box("bar", ui.Style{W: ui.Flex(1), H: ui.Px(40), Fill: bar}, nil)
			view = c.Column("view", ui.Style{W: ui.Flex(1), H: ui.Px(100), ScrollY: true}, func(c *ui.Ctx) {
				c.Box("above", ui.Style{H: ui.Px(200)}, nil)
				field = c.Next("prose")
				Prose(c, "prose", &text, ProseOpts{Label: "Reply"})
				c.Box("below", ui.Style{H: ui.Px(200)}, nil)
			})
		})
	})
	ui.Provide[Dictator](h.u, &fakeDictator{})
	h.frame()
	mic := ui.Child(field, "mic")
	h.u.ScrollTo(view, render.Point{Y: 200})
	h.frame()
	h.frame()
	r := h.rect(mic)
	if r.Min.Y < 40 || r.Max.Y > 140 {
		t.Fatalf("mic at %v; want it in view to start", r)
	}
	h.wheel(render.Point{X: 200, Y: 100}, 0) // the pointer rests in the view
	cases := []struct {
		name   string
		scroll float32
	}{
		{"partly out", 200 + (r.Min.Y+r.Max.Y)/2 - 40},
		{"fully out", 200 + r.Max.Y - 40 + 30},
	}
	for _, tc := range cases {
		h.u.ScrollTo(view, render.Point{Y: tc.scroll})
		h.frame()
		h.frame()
		m := h.rect(mic)
		if m.Min.Y >= 40 {
			t.Fatalf("%s: mic at %v; want it past the view's top", tc.name, m)
		}
		img := raster.Render(h.list, h.size, 1, resolved(t, theme.Dark).C(theme.BgBase))
		for y := 0; y < 40; y++ {
			for x := int(m.Min.X) - 4; x < int(m.Max.X)+4; x++ {
				if got := img.RGBAAt(x, y); !near(got, color.RGBA{R: bar.R, G: bar.G, B: bar.B, A: 255}, 2) || got.B > bar.B+2 {
					t.Fatalf("%s: drew %v over the bar at %d,%d; want the mic clipped to its view", tc.name, got, x, y)
				}
			}
		}
		for _, id := range h.u.Hit(render.Point{X: (m.Min.X + m.Max.X) / 2, Y: max(m.Min.Y+1, 0)}) {
			if id == mic || id == ui.Child(mic, "glyph") {
				t.Errorf("%s: the mic is hit at %v, above its view", tc.name, m)
			}
		}
		if tc.name == "partly out" {
			in := h.u.Hit(render.Point{X: (m.Min.X + m.Max.X) / 2, Y: m.Max.Y - 2})
			if !slices.Contains(in, mic) {
				t.Errorf("%s: the mic's part in view isn't hit: %v", tc.name, in)
			}
		}
	}
}
