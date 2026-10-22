package kit

import (
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// The popups at the editor's cursor, and code in them.
var (
	TPopupBg   = theme.Declare("lsp.popup.bg", "bg.raised")
	TPopupLine = theme.Declare("lsp.popup.border", "line.strong")
	CodeMono   = typeset.FontSpec{Family: typeset.Mono, Size: 12}
)

// UIFontSize is ui.font_size's default: the size the mockups are drawn at, zoom 1.
const UIFontSize = 13.0

// Popup is a card floating over the editor: a hover's, a completion menu's, an answer's.
func Popup(c *ui.Ctx, key any, st ui.Style, body func(c *ui.Ctx)) {
	th := c.Theme()
	st.Fill, st.Stroke, st.Border, st.Radius = th.C(TPopupBg), th.C(TPopupLine), 1, 6
	c.Column(key, st, body)
}

// PartScale is what a part with its own size option is drawn at over the UI; 0 when it follows the UI.
func PartScale(o registry.Options, name string) float32 {
	v, l, err := o.Get(name, registry.At{})
	if err != nil || l == registry.LayerDefault {
		return 0
	}
	if ui, _, err := o.Get("ui.font_size", registry.At{}); err == nil && ui.(float64) > 0 {
		return float32(v.(float64) / ui.(float64))
	}
	return float32(v.(float64) / UIFontSize)
}
