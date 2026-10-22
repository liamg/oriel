package editor

import (
	"cmp"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/vim"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/widget"
)

// modeSegment is the status line's mode chip: the current window's engine and its mode.
func (x *Editor) modeSegment(c *ui.Ctx) {
	th := c.Theme()
	if f := x.h.Layout().Focus; f != nil && f.ID != kit.EditorView {
		if v, ok := x.h.ViewByID(f.ID); ok {
			if m, ok := uireg.As[uireg.Moded](v); ok {
				label, style := m.ModeLabel(f.Instance)
				c.Row("mode", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: kit.TokenColour(th, style, th.C(tModeNormal)),
					Role: ui.RoleStatus, Label: "Mode"}, func(c *ui.Ctx) {
					c.Text("m", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Bold, Tracking: .12},
						Color: th.C(tModeFg)})
				})
				return
			}
		}
	}
	if !x.shown() {
		return
	}
	mode := x.e.Mode()
	label := modeName(mode)
	if st := x.inputFor(x.current()); !isVim(st) {
		mode, label = "insert", st.Mode().Label
	}
	if _, pr, ok := x.presenting(x.current()); ok {
		mode, label = "", strings.ToUpper(cmp.Or(pr.Label, pr.Name))
	}
	bg := map[string]theme.TokenID{"insert": tModeInsert, "replace": tModeInsert, "visual": tModeVisual, "visual-line": tModeVisual,
		"visual-block": tModeVisual, "cmdline": tModeCommand}[mode]
	if bg == 0 {
		bg = tModeNormal
	}
	c.Row("modes", ui.Style{H: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("mode", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(12, 0), Cross: ui.Center, Fill: th.C(bg), Role: ui.RoleStatus, Label: "Mode"}, func(c *ui.Ctx) {
			c.Text("m", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5, Weight: typeset.Bold, Tracking: .12},
				Color: th.C(tModeFg)})
		})
		if m, ok := x.e.Multi(); ok {
			c.Row("multi", ui.Style{H: ui.Flex(1), Pad: ui.PadXY(10, 0), Cross: ui.Center, Fill: th.C(tModeVisual), Role: ui.RoleStatus, Label: "Cursors"},
				func(c *ui.Ctx) {
					c.Text("n", fmt.Sprintf("%d CURSORS", len(m.Regions)), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5,
						Weight: typeset.Bold, Tracking: .12}, Color: th.C(tModeFg)})
				})
		}
	})
}

// shown reports whether any editor window is in the layout.
func (x *Editor) shown() bool {
	for _, id := range x.e.WindowIDs() {
		if x.h.Layout().Find(windowRef(id)) != nil {
			return true
		}
	}
	return false
}

func (x *Editor) current() vim.Pane {
	p, _ := x.e.Pane(x.e.WindowID())
	return p
}

// fileSegment is the file's language, encoding and line endings: "Go · utf-8 · LF".
func (x *Editor) fileSegment(c *ui.Ctx) {
	if !x.shown() {
		return
	}
	p := x.current()
	parts := []string{}
	if h, ok := x.docs[p.Buffer()]; ok && h.lang != "" {
		parts = append(parts, strings.ToUpper(h.lang[:1])+h.lang[1:])
	}
	enc, eol := "utf-8", "LF"
	if f := x.e.File(); f != nil {
		if f.Format.BOM {
			enc = "utf-8 bom"
		}
		if f.Format.CRLF {
			eol = "CRLF"
		}
	}
	parts = append(parts, enc, eol)
	c.Text("file", strings.Join(parts, " · "), kit.StatusText(c, kit.TStatusFg))
}

// positionSegment is the cursor's line and column, and how far through the file it is.
func (x *Editor) positionSegment(c *ui.Ctx) {
	if !x.shown() {
		return
	}
	p := x.current()
	cur := p.Cursor()
	n := p.Buf().Lines()
	c.Row("pos", ui.Style{Gap: 16, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("lc", fmt.Sprintf("%d:%d", cur.Line+1, p.VCol(cur.Line, cur.Col)+1), kit.StatusText(c, kit.TStatusFg))
		c.Text("pct", fmt.Sprintf("≡ %d%%", (cur.Line+1)*100/max(n, 1)), kit.StatusText(c, kit.TStatusFg))
	})
}

// wrapSegment says whether long lines wrap, and switches it.
func (x *Editor) wrapSegment(c *ui.Ctx) {
	if !x.shown() {
		return
	}
	on := x.current().Wrap()
	tok, tip := theme.FgFaint, "Long lines run off the right: click to wrap them"
	if on {
		tok, tip = kit.TStatusStrong, "Long lines wrap: click to stop wrapping them"
	}
	in := widget.Clickable(c, "wrap", ui.Style{H: ui.Flex(1), Cross: ui.Center, Role: ui.RoleButton, Label: "Wrap", Tooltip: tip,
		Help: "editor/pane#wrap"}, func(c *ui.Ctx) {
		c.Text("t", "wrap", kit.StatusText(c, tok))
	})
	if in.Clicked {
		x.e.Run("set wrap!")
	}
}

// modeName is the engine's mode as the status line chip says it.
func modeName(m string) string {
	switch m {
	case "insert":
		return "INSERT"
	case "replace":
		return "REPLACE"
	case "visual":
		return "VISUAL"
	case "visual-line":
		return "V-LINE"
	case "visual-block":
		return "V-BLOCK"
	case "cmdline":
		return "COMMAND"
	case "op-pending":
		return "O-PENDING"
	}
	return "NORMAL"
}

// keyMode is the keymap mode the editor takes keys in: vim's mode letters, so :nmap is mode "n".
func keyMode(m string) string {
	switch m {
	case "insert", "replace":
		return "i"
	case "visual", "visual-line", "visual-block":
		return "x"
	case "cmdline":
		return "c"
	case "op-pending":
		return "o"
	}
	return "n"
}
