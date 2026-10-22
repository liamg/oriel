package kit

import (
	"math"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The editor column of 01-ide: a 26 px breadcrumb, then lines of editor.font_size mono (13.5 px, 21 px apart by
// default), with a 64 px gutter.
const (
	CrumbH   = 26
	CodeSize = 13.5
	CodePadT = 6
	GutterW  = 64
)

// The editor's tokens, for code drawn as the editor draws it.
var (
	TEditorBg     = theme.Declare("editor.bg", "bg.base")
	TGutterFg     = theme.Declare("editor.gutter.fg", "fg.ghost")
	TCodeFg       = theme.Declare("editor.fg", "fg")
	TStatusStrong = theme.Declare("statusline.fg.strong", "fg")
)

// CodeFont is code at the default size, for code shown outside the editor (help, search).
var CodeFont = typeset.FontSpec{Family: typeset.Mono, Size: CodeSize}

// PaneMetrics are where things are in a pane, for the layout's neighbour finding and the pointer.
type PaneMetrics struct {
	CharW, LineH, CodeTop, TextX float32
	Font                         typeset.FontSpec // the code's, from editor.font_size
}

// Metrics are a pane's for editor.font_size and editor.line_height now; prev, when they haven't moved.
func Metrics(c *ui.Ctx, o registry.Options, prev PaneMetrics) PaneMetrics {
	size, lh := float32(CodeSize), float32(1.55)
	if v, _, err := o.Get("editor.font_size", registry.At{}); err == nil {
		size = float32(v.(float64))
	}
	if v, _, err := o.Get("editor.line_height", registry.At{}); err == nil {
		lh = float32(v.(float64))
	}
	if prev.CharW == 0 || prev.Font.Size != size || prev.LineH != float32(math.Round(float64(size*lh))) {
		font := typeset.FontSpec{Family: typeset.Mono, Size: size}
		return PaneMetrics{CharW: c.Shaper().Shape("M", font).Advance, LineH: float32(math.Round(float64(size * lh))),
			CodeTop: CrumbH + CodePadT, TextX: GutterW + 4, Font: font}
	}
	return prev
}

// StatusText is the status line's text in tok.
func StatusText(c *ui.Ctx, tok theme.TokenID) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Medium}, Color: c.Theme().C(tok)}
}
