// Package widget is the toolkit's widgets: buttons, chips, inputs, lists, trees, tabs, menus, context menus,
// popovers, modals, toasts and sliders. They're built only on ui's public API, as a plugin's widgets would be, and
// paint with theme tokens they declare.
//
// The surfaces that close take one of two contracts. One given an open *bool (Modal, Page) closes itself, setting it
// false on Escape or a click outside. One that isn't (Dialog, Scrim) returns closed, and its caller closes it. Each
// takes its title and accessible name in its Opts.
package widget

import (
	"image/color"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Component tokens, defaulting to semantic ones; the values follow the mockups' base.css.
var (
	tButtonBg       = theme.Declare("button.bg", "bg.overlay")
	tButtonHover    = theme.Declare("button.bg.hover", "bg.strong")
	tButtonPressed  = theme.Declare("button.bg.pressed", "line.strong")
	tButtonFg       = theme.Declare("button.fg", "fg")
	tButtonBorder   = theme.Declare("button.border", "line.strong")
	tPrimaryBg      = theme.Declare("button.primary.bg", "accent")
	tPrimaryHover   = theme.Declare("button.primary.bg.hover", "lighten(accent, 4%)")
	tPrimaryPressed = theme.Declare("button.primary.bg.pressed", "darken(accent, 6%)")
	tPrimaryFg      = theme.Declare("button.primary.fg", "accent.ink")
	tDisabledFg     = theme.Declare("control.disabled.fg", "fg.ghost")
	tInputBg        = theme.Declare("input.bg", "bg.base")
	tInputBorder    = theme.Declare("input.border", "line.strong")
	tInputFocus     = theme.Declare("input.border.focus", "accent")
	tInputInvalid   = theme.Declare("input.border.invalid", "error")
	tInputFg        = theme.Declare("input.fg", "fg")
	tInputHint      = theme.Declare("input.placeholder", "fg.faint")
	tInputCaret     = theme.Declare("input.caret", "accent")
	tInputSelection = theme.Declare("input.selection", "text.selection")
	tRowHover       = theme.DeclareOn("list.row.hover", "alpha(fg, 4%)", "bg.surface")
	tRowSelected    = theme.Declare("list.row.selected", "accent.bg")
	tRowEdge        = theme.Declare("list.row.selected.edge", "accent")
	tRowPicked      = theme.DeclareOn("list.row.picked", "alpha(accent, 16%)", "bg.base") // a SelectRow's, over rows tinted their own way
	tRowFg          = theme.Declare("list.row.fg", "fg.muted")
	tRowSelectedFg  = theme.Declare("list.row.selected.fg", "fg")
	tTabBg          = theme.Declare("tab.bg", "bg.surface")
	tTabActiveBg    = theme.Declare("tab.active.bg", "bg.raised")
	tTabEdge        = theme.Declare("tab.active.edge", "accent")
	tTabFg          = theme.Declare("tab.fg", "fg.faint")
	tTabActiveFg    = theme.Declare("tab.active.fg", "fg")
	tMenuBg         = theme.Declare("menu.bg", "bg.raised")
	tMenuBorder     = theme.Declare("menu.border", "line.strong")
	tMenuHover      = theme.Declare("menu.item.hover", "bg.overlay")
	tMenuFg         = theme.Declare("menu.fg", "fg")
	tMenuShortcut   = theme.Declare("menu.shortcut", "fg.faint")
	tMenuSeparator  = theme.Declare("menu.separator", "line")
	tScrim          = theme.Declare("modal.scrim", "alpha(#030508, 72%)")
	tModalBg        = theme.Declare("modal.bg", "bg.surface")
	tPageBg         = theme.Declare("page.bg", "bg.base")
	tModalBorder    = theme.Declare("modal.border", "line.strong")
	tToastBg        = theme.Declare("toast.bg", "bg.raised")
	tToastBorder    = theme.Declare("toast.border", "line.strong")
	tToastFg        = theme.Declare("toast.fg", "fg")
	tToastMuted     = theme.Declare("toast.fg.muted", "fg.faint")
	tLabel          = theme.Declare("label.fg", "fg.faint")
	tModalTitle     = theme.Declare("modal.title", "fg")
	tSegBorder      = theme.Declare("segmented.border", "line.strong")
	tSegFg          = theme.Declare("segmented.fg", "fg.faint")
	tSegOnBg        = theme.Declare("segmented.active.bg", "accent.bg")
	tSegOnFg        = theme.Declare("segmented.active.fg", "accent")
	tIconButtonFg   = theme.Declare("iconbutton.fg", "fg.faint")
)

func init() { theme.Readable("input.selection", "input.bg", 1.5) }

func col(c *ui.Ctx, id theme.TokenID) color.NRGBA { return c.Theme().C(id) }

// The widgets' motion: what fades, and where from.
var (
	fade    = &ui.Transition{Fade: true}
	dropIn  = &ui.Transition{Fade: true, Shift: render.Point{Y: -4}} // a menu or popover, from its anchor above
	riseIn  = &ui.Transition{Fade: true, Shift: render.Point{Y: 8}}  // a modal's card, rising a little
	slideIn = &ui.Transition{Fade: true, Shift: render.Point{X: 24}} // a toast, from the right edge
)

func uiText(c *ui.Ctx, size float32, w typeset.Weight, fg color.NRGBA) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Weight: w, Size: size}, Color: fg}
}

func monoText(size float32, w typeset.Weight, fg color.NRGBA, tracking float32) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Weight: w, Size: size, Tracking: tracking}, Color: fg}
}

func alpha(c color.NRGBA, a float64) color.NRGBA { return theme.WithAlpha(c, a*float64(c.A)/255) }

// passOn hands the keys id was given that take doesn't take up to what it's in: Escape on a focused button reaches
// its page.
func passOn(c *ui.Ctx, id ui.ID, keys []render.KeyEvent, take func(k render.KeyEvent) bool) {
	var rest []render.KeyEvent
	for _, k := range keys {
		if !take(k) {
			rest = append(rest, k)
		}
	}
	c.PassKeys(id, rest)
}

func activates(k render.KeyEvent) bool { return pressedKey([]render.KeyEvent{k}) }

// pressedKey reports whether keys hold one that activates a control: Enter or Space.
func pressedKey(keys []render.KeyEvent) bool {
	for _, k := range keys {
		if k.Mods == 0 && (k.Key == "CR" || k.Key == " ") {
			return true
		}
	}
	return false
}

func hasKey(keys []render.KeyEvent, names ...string) bool {
	for _, k := range keys {
		for _, n := range names {
			if k.Mods&^render.ModShift == 0 && k.Key == n {
				return true
			}
		}
	}
	return false
}
