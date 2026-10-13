package widget

import (
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var tLink = theme.Declare("link.fg", "accent")

// Wrap adds text as lines no wider than width, breaking at spaces (and inside a word only when the word alone
// is wider). It returns how many lines it added.
func Wrap(c *ui.Ctx, key any, s string, width float32, ts ui.TextStyle) int {
	lines := WrapLines(c.Shaper(), s, width, ts.Font)
	c.Column(key, ui.Style{Gap: 3}, func(c *ui.Ctx) {
		for i, ln := range lines {
			c.Text(i, ln, ts)
		}
	})
	return len(lines)
}

// WrapLines breaks s into lines that fit width, keeping its own newlines.
func WrapLines(sh typeset.Shaper, s string, width float32, f typeset.FontSpec) []string {
	var out []string
	fits := func(t string) bool { return sh.Shape(t, f).Advance <= width }
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, w := range strings.Fields(para) {
			next := w
			if line != "" {
				next = line + " " + w
			}
			if fits(next) {
				line = next
				continue
			}
			if line != "" {
				out = append(out, line)
			}
			for !fits(w) && len([]rune(w)) > 1 {
				rs := []rune(w)
				n := len(rs) - 1
				for n > 1 && !fits(string(rs[:n])) {
					n--
				}
				out = append(out, string(rs[:n]))
				w = string(rs[n:])
			}
			line = w
		}
		out = append(out, line)
	}
	return out
}

// Link is text in the link colour, underlined; it returns true on the frame it's clicked or activated.
func Link(c *ui.Ctx, key any, label string, help string) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	passOn(c, id, in.Keys, activates)
	fg := col(c, tLink)
	c.Column(key, ui.Style{Gap: 1, Focusable: true, Cursor: render.CursorPointer, Help: help, KeyMode: "button", Role: ui.RoleButton,
		Label: label}, func(c *ui.Ctx) {
		c.Text("label", label, uiText(c, 12, typeset.Medium, fg))
		c.Box("underline", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: alpha(fg, .7)}, nil)
	})
	return in.Clicked || pressedKey(in.Keys)
}

type NoticeCardOpts struct {
	Title   string // uppercase in the card's head
	Source  string // beside the title
	Body    string
	Age     string // at the head's right, when it can't be closed
	Hint    string // at the foot's right
	Tone    color.NRGBA
	Actions []string // the first is a link, the rest buttons
	Close   bool     // a ✕ in the head
	Width   float32  // 440 by default
	Help    string
}

type NoticeCardResult struct {
	Action  int // the action clicked, or -1
	Closed  bool
	Hovered bool
}

// NoticeCard is a notification's card (37-errors): a toned border and dot, a head, the text, and its actions.
// Escape or x while it's focused closes it.
func NoticeCard(c *ui.Ctx, key any, o NoticeCardOpts) NoticeCardResult {
	res := NoticeCardResult{Action: -1}
	id := c.Next(key)
	in := c.Interaction(id)
	res.Hovered = in.Hovered
	if hasKey(in.Keys, "Esc", "x") {
		res.Closed = true
	}
	w := o.Width
	if w == 0 {
		w = 440
	}
	c.Column(key, ui.Style{W: ui.Px(w), Pad: ui.PadXY(14, 12), Gap: 9, Fill: col(c, tToastBg), Stroke: alpha(o.Tone, .55), Border: 1, Radius: 8,
		Interactive: true, Focusable: true, KeyMode: "toast", Help: o.Help, Role: ui.RoleStatus, Label: o.Title + ": " + o.Body}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: o.Tone}, nil)
			c.Text("title", strings.ToUpper(o.Title), monoText(9.5, typeset.SemiBold, o.Tone, .14))
			if o.Source != "" {
				c.Text("source", o.Source, monoText(9.5, typeset.Normal, col(c, tToastMuted), 0))
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if o.Age != "" {
				c.Text("age", o.Age, monoText(9.5, typeset.Normal, col(c, tToastMuted), 0))
			}
			if o.Close {
				x := c.Next("close")
				if c.Interaction(x).Clicked {
					res.Closed = true
				}
				c.Box("close", ui.Style{Pad: ui.PadXY(2, 0), Interactive: true, Help: o.Help, Cursor: render.CursorPointer, Role: ui.RoleButton, Label: "Dismiss"}, func(c *ui.Ctx) {
					c.Text(nil, "✕", uiText(c, 11, typeset.Normal, col(c, tToastMuted)))
				})
			}
		})
		Wrap(c, "body", o.Body, w-28, uiText(c, 12.5, typeset.Normal, col(c, tToastFg)))
		if len(o.Actions) > 0 || o.Hint != "" {
			c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				for i, a := range o.Actions {
					if i == 0 && Link(c, i, a, o.Help) || i > 0 && Button(c, i, a, ButtonOpts{Help: o.Help}) {
						res.Action = i
					}
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if o.Hint != "" {
					c.Text("hint", o.Hint, monoText(9.5, typeset.Normal, col(c, tToastMuted), 0))
				}
			})
		}
	})
	return res
}
