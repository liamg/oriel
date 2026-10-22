package kit

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/keymap"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var TMeterContext = theme.Declare("agent.meter.context", "amber")

// ProviderName is what an agent provider is called, as its plugin registered it (registry.AgentProvider).
func ProviderName(r registry.Contributions, p string) string { return registry.ProviderTitle(r, p) }

// ProviderNameIn is ProviderName from the registry c is drawn with.
func ProviderNameIn(c *ui.Ctx, p string) string {
	r, _ := ui.Service[registry.Registry](c)
	return registry.ProviderTitle(r, p)
}

// Tokens is a count as the session stats show it: 950, 38k, 1.42M.
func Tokens(n int64) string {
	switch {
	case n >= 1_000_000:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	case n >= 10_000:
		return fmt.Sprintf("%dk", n/1000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprint(n)
}

const OverlayPrefix = "app.overlay." // views named so float over the window: the omnibox, which-key

const HUDHelp = "shell/hud"

func HUDDot(c *ui.Ctx, key any, tone render.Color) {
	c.Box(key, ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: tone}, nil)
}

const MissionHelp = "shell/mission"

// StartingHelp is the help on an agent starting.
const StartingHelp = "agent/console#starting"

// MissionCell is one part of mission control's strip: a small heading over a readout. Indicators in the mission
// slot draw theirs with it.
func MissionCell(c *ui.Ctx, key any, title string, body func(c *ui.Ctx)) {
	th := c.Theme()
	c.Row(key, ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Column("cell", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(18, 12), Gap: 8, Role: ui.RoleGroup, Label: title}, func(c *ui.Ctx) {
			c.Text("t", strings.ToUpper(title), KickerFont(th.C(theme.FgFaint)))
			body(c)
		})
		c.Box("rule", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
	})
}

// OpenURL opens url in the OS's default app for it (system.open).
func OpenURL(h plugin.Host, url string) error {
	return h.Run(context.Background(), "system.open", registry.Args{"url": url})
}

// CopyText puts s on the system clipboard (system.copy).
func CopyText(h plugin.Host, s string) {
	if err := h.Run(context.Background(), "system.copy", registry.Args{"text": s}); err != nil {
		h.Log().Warn("copy", "err", err)
	}
}

// ProjectInitials are a project's two letters for the narrow sidebar: "admin-ui" is AD.
func ProjectInitials(name string) string {
	var rs []rune
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			rs = append(rs, r)
		}
		if len(rs) == 2 {
			break
		}
	}
	return string(rs)
}

// BoundKey is the key bound to command with args right now, as the keyboard shows it ("⌘⇧E"), or "".
func BoundKey(h plugin.Host, command string, args registry.Args) string {
	for _, k := range h.KeymapsFor(command) {
		if (k.Mode == "global" || k.Mode == "n") && SameArgs(k.Args, args) {
			return KeyLabel(k.Keys)
		}
	}
	return ""
}

func SameArgs(a, b registry.Args) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if fmt.Sprint(b[k]) != fmt.Sprint(v) {
			return false
		}
	}
	return true
}

const TitleRight = "titlebar.right."

// Notation writes a key press as a canonical keymap token: "x", "X", "<C-x>", "<D-s>", "<CR>".
func Notation(ev render.KeyEvent) string {
	single := utf8.RuneCountInString(ev.Key) == 1
	mods := ev.Mods
	// A character's shift is in the character; Space's isn't, so ⌃⇧Space keeps it (⇧Space alone is still a space). A
	// control-letter's case is folded (⌃C is ⌃c), so ⌃⇧ and a letter keeps its shift too: <C-S-c>, Linux's copy.
	ctrlLetter := single && mods&render.ModShift != 0 && mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == render.ModCtrl &&
		unicode.IsLetter([]rune(ev.Key)[0])
	if ctrlLetter {
		ev.Key = strings.ToLower(ev.Key)
	}
	if single && !ctrlLetter && (ev.Key != " " || mods&(render.ModCtrl|render.ModAlt|render.ModSuper) == 0) {
		mods &^= render.ModShift
	}
	if single && mods == 0 {
		return ev.Key
	}
	s := "<"
	for _, m := range []struct {
		mod    render.Mods
		prefix string
	}{{render.ModCtrl, "C-"}, {render.ModAlt, "M-"}, {render.ModSuper, "D-"}, {render.ModShift, "S-"}} {
		if mods&m.mod != 0 {
			s += m.prefix
		}
	}
	name := ev.Key
	if f := keymap.Format([]string{ev.Key}); single && f != ev.Key {
		name = f[1 : len(f)-1] // " " is <Space>, "<" is <lt>
	}
	if toks := keymap.Parse(s + name + ">"); len(toks) == 1 {
		return toks[0]
	}
	return s + name + ">"
}

var TOmniBg = theme.Declare("omnibox.bg", "bg.surface")

var TOmniBorder = theme.Declare("omnibox.border", "line.strong")

var TScrimOmni = theme.Declare("omnibox.scrim", "alpha(#030508, 60%)")

// The window's bars: the title bar's height, the status line's, and the sidebar's narrow strip's width.
const (
	TitleH  = 38
	StatusH = 26
	RailW   = 44
)

var TLogoRing = theme.Declare("logo.ring", "accent")

var TLogoCursor = theme.Declare("logo.cursor", "brand")

var TWordmark = theme.Declare("logo.wordmark", "fg")

// Logo is the o▌ mark: a ring in the accent and a tall cursor in the brand orange, each with a faint glow.
func Logo(f render.Frame, r render.Rect, th *theme.Resolved) { LogoLit(f, r, th, true, 1) }

// LogoLit is the logo with its cursor shown or not, as it blinks, and its glow scaled by glow.
func LogoLit(f render.Frame, r render.Rect, th *theme.Resolved, lit bool, glow float64) {
	s := r.Dx() / 100
	cx, cy := r.Min.X+38*s, r.Min.Y+r.Dy()/2+3*s
	ring, cursor := th.C(TLogoRing), th.C(TLogoCursor)
	if r.Dx() <= 64 {
		f.Effect(render.GlowDisc, render.XYWH(cx-25*s, cy-25*s, 50*s, 50*s), 4, theme.WithAlpha(cursor, min(.22*glow, 1))) // a big logo glows from its bar alone: this would fill the ring
	}
	bar := render.XYWH(r.Min.X+68*s, r.Min.Y+r.Dy()/2-22*s, 13*s, 50*s)
	if lit {
		f.Effect(render.Glow, bar, 4, theme.WithAlpha(cursor, min(.3*glow, 1)))
	}
	f.Path(render.Ellipse(render.XYWH(cx-21*s, cy-21*s, 42*s, 42*s)), render.Style{Stroke: ring, Width: 8 * s})
	if !lit {
		return
	}
	var p render.Path // a path, not a rect, so the bar isn't snapped to pixels and stays level with the ring
	p.MoveTo(bar.Min).LineTo(render.Point{X: bar.Max.X, Y: bar.Min.Y}).LineTo(bar.Max).LineTo(render.Point{X: bar.Min.X, Y: bar.Max.Y}).Close()
	f.Path(&p, render.Style{Fill: cursor})
}

// KeyEvent reads a keymap token back into a key press: "x", "<CR>", "<C-w>", "<D-S-s>".
func KeyEvent(tok string) render.KeyEvent {
	if !strings.HasPrefix(tok, "<") || len(tok) < 3 {
		return render.KeyEvent{Key: tok}
	}
	body := tok[1 : len(tok)-1]
	var mods render.Mods
	for len(body) > 2 && body[1] == '-' {
		switch body[0] {
		case 'C':
			mods |= render.ModCtrl
		case 'M':
			mods |= render.ModAlt
		case 'D':
			mods |= render.ModSuper
		case 'S':
			mods |= render.ModShift
		}
		body = body[2:]
	}
	if toks := keymap.Parse("<" + body + ">"); len(toks) == 1 && utf8.RuneCountInString(toks[0]) == 1 {
		body = toks[0] // <Space> is " ", <lt> is "<"
	}
	return render.KeyEvent{Key: body, Mods: mods}
}
