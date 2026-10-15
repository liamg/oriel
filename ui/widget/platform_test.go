package widget

import (
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

type testClip struct{ text string }

func (c *testClip) SetClipboard(s string)         { c.text = s }
func (c *testClip) ReadClipboard() (string, bool) { return c.text, c.text != "" }

// The editing keys of a text field, on macOS (⌘ line and clipboard, ⌥ word) and elsewhere (Ctrl).
func TestInputEditingKeys(t *testing.T) {
	for _, tc := range []struct {
		goos              string
		primary, word     render.Mods
		lineStart, delSOL []render.KeyEvent
	}{
		{"darwin", render.ModSuper, render.ModAlt, []render.KeyEvent{{Key: "Left", Mods: render.ModSuper}}, []render.KeyEvent{{Key: "BS", Mods: render.ModSuper}}},
		{"linux", render.ModCtrl, render.ModCtrl, []render.KeyEvent{{Key: "Home"}}, nil},
	} {
		t.Run(tc.goos, func(t *testing.T) {
			was := platform
			platform = platformMods(tc.goos)
			t.Cleanup(func() { platform = was })
			text := ""
			var id ui.ID
			clip := &testClip{}
			h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
				id = c.Next("q")
				Input(c, "q", &text, InputOpts{Label: "Q", Help: "test/input"})
			})
			h.u.SetClipboard(clip)
			h.u.Focus(id)
			h.frame()
			press := func(evs ...render.KeyEvent) {
				t.Helper()
				for _, ev := range evs {
					if !h.u.Key(ev) {
						t.Fatalf("nothing takes %v", ev)
					}
					h.frame()
				}
			}
			typed := func(s string) {
				for _, r := range s {
					press(render.KeyEvent{Key: string(r)})
				}
			}
			typed("hello big world")
			press(render.KeyEvent{Key: "Left", Mods: tc.word}, render.KeyEvent{Key: "BS", Mods: tc.word})
			if text != "hello world" {
				t.Errorf("word left, delete word back: %q", text)
			}
			press(tc.lineStart...)
			typed(">")
			if text != ">hello world" {
				t.Errorf("line start, then >: %q", text)
			}
			press(render.KeyEvent{Key: "Right", Mods: tc.word}, render.KeyEvent{Key: "Left", Mods: tc.word | render.ModShift},
				render.KeyEvent{Key: "c", Mods: tc.primary})
			if clip.text != "hello" {
				t.Errorf("selecting a word back and copying: clipboard %q", clip.text)
			}
			press(render.KeyEvent{Key: "a", Mods: tc.primary}, render.KeyEvent{Key: "x", Mods: tc.primary})
			if text != "" || clip.text != ">hello world" {
				t.Errorf("select all, cut: text %q, clipboard %q", text, clip.text)
			}
			press(render.KeyEvent{Key: "v", Mods: tc.primary}, render.KeyEvent{Key: "v", Mods: tc.primary})
			if text != ">hello world>hello world" {
				t.Errorf("paste twice: %q", text)
			}
			if tc.delSOL != nil {
				press(tc.delSOL...)
				if text != "" {
					t.Errorf("delete to line start: %q", text)
				}
			}
		})
	}
}

// A secret's field (a webhook being pasted in) shows dots to the eye and to assistive technology, and copy and cut
// leave the clipboard alone; the text it edits is what was typed.
func TestInputSecret(t *testing.T) {
	text := ""
	var id ui.ID
	clip := &testClip{text: "https://hooks.slack.com/services/T/B/x"}
	h := newHarness(t, theme.Dark, small, func(c *ui.Ctx) {
		id = c.Next("s")
		Input(c, "s", &text, InputOpts{Label: "Webhook", Help: "test/input", Secret: true})
	})
	h.u.SetClipboard(clip)
	h.u.Focus(id)
	h.frame()
	for _, ev := range []render.KeyEvent{{Key: "v", Mods: platform.primary}, {Key: "a", Mods: platform.primary}, {Key: "c", Mods: platform.primary}} {
		h.u.Key(ev)
		h.frame()
	}
	clip.text = "other"
	h.u.Key(render.KeyEvent{Key: "x", Mods: platform.primary})
	h.frame()
	if text != "https://hooks.slack.com/services/T/B/x" || clip.text != "other" {
		t.Errorf("text %q, clipboard %q", text, clip.text)
	}
	if v := h.access(id).Value; v != "••••••••••••••••••••••••••••••••••••••" {
		t.Errorf("assistive technology reads %q", v)
	}
}
