package kit

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// UIFont is the UI face at size in col.
func UIFont(size float32, col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: size}, Color: col}
}

// MonoFont is the mono face at size in col.
func MonoFont(size float32, col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: col}
}

// KickerFont is the small spaced-out caps over a section.
func KickerFont(col render.Color) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14}, Color: col}
}

// MonoTok is the mono face at size in a theme token.
func MonoTok(c *ui.Ctx, size float32, tok theme.TokenID) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: c.Theme().C(tok)}
}

// Heading is a section's caption, with a note at its right.
func Heading(c *ui.Ctx, key any, title, note string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Cross: ui.Center, Pad: ui.Insets{B: 8}}, func(c *ui.Ctx) {
		Caption(c, "cap", title)
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if note != "" {
			c.Text("note", note, MonoFont(10, th.C(theme.FgFaint)))
		}
	})
}

// Mono is the mono face at size and weight.
func Mono(size float32, w typeset.Weight, c color.NRGBA) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size, Weight: w}, Color: c}
}

// KbdKey is a key as a hint shows it.
func KbdKey(c *ui.Ctx, k string) {
	th := c.Theme()
	c.Row("kbd", ui.Style{Pad: ui.PadXY(5, 1), Fill: th.C(theme.BgRaised), Stroke: th.C(theme.LineStrong), Border: 1, Radius: 4}, func(c *ui.Ctx) {
		c.Text(nil, k, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: th.C(theme.FgMuted)})
	})
}

// Caption is a section's small upper-case title.
func Caption(c *ui.Ctx, key any, s string) {
	c.Text(key, strings.ToUpper(s), KickerFont(c.Theme().C(theme.FgFaint)))
}

// Keycap is a key drawn as a small key.
func Keycap(c *ui.Ctx, key any, k string) {
	th := c.Theme()
	c.Box(key, ui.Style{Pad: ui.PadXY(7, 2), Radius: 4, Fill: th.C(theme.BgRaised), Stroke: th.C(theme.LineStrong), Border: 1}, func(c *ui.Ctx) {
		c.Text("k", k, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.Medium}, Color: th.C(theme.FgMuted)})
	})
}

// Note is a faint aside.
func Note(c *ui.Ctx, key any, s string) {
	c.Text(key, s, MonoFont(10, c.Theme().C(theme.FgGhost)))
}

// PaneNote says why a pane is empty, centred in it.
func PaneNote(c *ui.Ctx, key, text string) {
	c.Column(key, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.PadXY(16, 0)}, func(c *ui.Ctx) {
		widget.Wrap(c, "t", text, 520, UIFont(12.5, c.Theme().C(theme.FgFaint)))
	})
}

// KickerRow is a section's kicker on a row of its own.
func KickerRow(c *ui.Ctx, key, text string) {
	c.Row(key, ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
		c.Text("k", text, KickerFont(c.Theme().C(theme.FgFaint)))
	})
}

// NoSessionNote says there's no session in front, and what shows once there is.
func NoSessionNote(c *ui.Ctx, what string) {
	c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.Pad(16)}, func(c *ui.Ctx) {
		c.Text("t", "No session in front: "+what, UIFont(12.5, c.Theme().C(theme.FgFaint)))
	})
}
