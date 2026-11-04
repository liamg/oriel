package widget

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"sync"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var tLink = theme.Declare("link.fg", "accent")

// Wrap adds text as lines no wider than width, breaking at spaces (and inside a word only when the word alone
// is wider). It returns how many lines it added.
func Wrap(c *ui.Ctx, key any, s string, width float32, ts ui.TextStyle) int {
	lines := wrapped(c, s, width, ts, false)
	c.Column(key, ui.Style{Gap: 3}, func(c *ui.Ctx) {
		for i, ln := range lines {
			if ln.soft {
				c.WrappedText(i, ln.text, ts, " ")
			} else {
				c.Text(i, ln.text, ts)
			}
		}
	})
	return len(lines)
}

// WrapCode adds code as lines no wider than width, every space kept: a line too long breaks after its last space
// that fits, else anywhere, gap apart. Copied, a wrapped line joins the one before as it was. It returns how many
// lines it added.
func WrapCode(c *ui.Ctx, key any, s string, width, gap float32, ts ui.TextStyle) int {
	lines := wrapped(c, s, width, ts, true)
	c.Column(key, ui.Style{Gap: gap}, func(c *ui.Ctx) {
		for i, ln := range lines {
			if ln.soft {
				c.WrappedText(i, ln.text, ts, "")
			} else {
				c.Text(i, ln.text, ts)
			}
		}
	})
	return len(lines)
}

// wrapLine is a line of wrapped text; soft when it wraps on from the line before.
type wrapLine struct {
	text string
	soft bool
}

// wrapped is s wrapped to width, as prose or as code, kept for the UI.
func wrapped(c *ui.Ctx, s string, width float32, ts ui.TextStyle, code bool) []wrapLine {
	wraps := ui.Keep(c, func() *wrapCache { return &wrapCache{cur: map[wrapKey][]wrapLine{}} })
	k := wrapKey{c.Shaper(), s, width, ts.Font.Family, ts.Font.Weight, ts.Font.Italic, ts.Font.Size, ts.Font.Tracking, fmt.Sprint(ts.Font.Features), code}
	lines, ok := wraps.get(k)
	if !ok {
		if code {
			lines = codeLines(c.Shaper(), s, width, ts.Font)
		} else {
			lines = proseLines(c.Shaper(), s, width, ts.Font)
		}
		wraps.put(k, lines)
	}
	return lines
}

// WrapLines breaks s into lines that fit width, keeping its own newlines. Each paragraph is shaped once and
// its breaks found over the glyph advances; Wrap keeps the results, by text, width and font, for its UI.
func WrapLines(sh typeset.Shaper, s string, width float32, f typeset.FontSpec) []string {
	var out []string
	for _, ln := range proseLines(sh, s, width, f) {
		out = append(out, ln.text)
	}
	return out
}

func proseLines(sh typeset.Shaper, s string, width float32, f typeset.FontSpec) []wrapLine {
	var out []wrapLine
	for _, para := range strings.Split(s, "\n") {
		for i, ln := range wrapPara(nil, sh, strings.Join(strings.Fields(para), " "), width, f) {
			out = append(out, wrapLine{ln, i > 0})
		}
	}
	return out
}

// codeLines breaks each line of code to fit width, tabs as four spaces.
func codeLines(sh typeset.Shaper, s string, width float32, f typeset.FontSpec) []wrapLine {
	var out []wrapLine
	for _, line := range strings.Split(s, "\n") {
		rs := []rune(strings.ReplaceAll(line, "\t", "    "))
		if len(rs) == 0 {
			out = append(out, wrapLine{})
			continue
		}
		xs := typeset.PenXs(sh.Shape(string(rs), f), len(rs))
		for a := 0; a < len(rs); {
			b := a + 1
			for b < len(rs) && xs[b+1]-xs[a] <= width {
				b++
			}
			if b < len(rs) {
				// after the last space that follows some text: never inside the indent
				text := a
				for text < b && rs[text] == ' ' {
					text++
				}
				for k := b; k > text+1; k-- {
					if rs[k-1] == ' ' {
						b = k
						break
					}
				}
			}
			out = append(out, wrapLine{string(rs[a:b]), a > 0})
			a = b
		}
	}
	return out
}

type wrapKey struct {
	sh       typeset.Shaper
	text     string
	width    float32
	family   string
	weight   typeset.Weight
	italic   bool
	size     float32
	tracking float32
	features string
	code     bool
}

// wrapCache is a UI's wrapped texts, in two generations, so a full cache drops what wasn't used recently.
type wrapCache struct {
	mu       sync.Mutex
	cur, old map[wrapKey][]wrapLine
}

const maxWraps = 512

func (w *wrapCache) get(k wrapKey) ([]wrapLine, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if v, ok := w.cur[k]; ok {
		return v, true
	}
	v, ok := w.old[k]
	if ok {
		w.cur[k] = v
	}
	return v, ok
}

func (w *wrapCache) put(k wrapKey, v []wrapLine) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.cur) >= maxWraps {
		w.cur, w.old = map[wrapKey][]wrapLine{}, w.cur
	}
	w.cur[k] = v
}

// wrapPara appends the lines of p, whose words are single-spaced, to out.
func wrapPara(out []string, sh typeset.Shaper, p string, width float32, f typeset.FontSpec) []string {
	rs := []rune(p)
	if len(rs) == 0 {
		return append(out, "")
	}
	xs := typeset.PenXs(sh.Shape(p, f), len(rs))
	fits := func(a, b int) bool { return xs[b]-xs[a] <= width }
	for a := 0; a < len(rs); {
		// The furthest word end that fits, checked against a real shape since kerning can cross a break.
		end := -1
		for b := a; b <= len(rs); b++ {
			if b == len(rs) || rs[b] == ' ' {
				if b > a && !fits(a, b) {
					break
				}
				end = b
			}
		}
		for end > a && sh.Shape(string(rs[a:end]), f).Advance > width {
			e := end - 1
			for e > a && rs[e] != ' ' {
				e--
			}
			end = e
		}
		if end > a {
			out = append(out, string(rs[a:end]))
			a = end + 1
			continue
		}
		// The word alone is too wide: break it after the most runes that fit, at least one.
		wordEnd := a
		for wordEnd < len(rs) && rs[wordEnd] != ' ' {
			wordEnd++
		}
		n := sort.Search(wordEnd-a, func(n int) bool { return !fits(a, a+n+1) })
		n = max(n, 1)
		for n > 1 && sh.Shape(string(rs[a:a+n]), f).Advance > width {
			n--
		}
		if a+n == wordEnd {
			out = append(out, string(rs[a:wordEnd]))
			a = wordEnd + 1
			continue
		}
		out = append(out, string(rs[a:a+n]))
		a += n
	}
	return out
}

// Link is text in the link colour, underlined; it returns true on the frame it's clicked or activated.
func Link(c *ui.Ctx, key any, label string, help string) bool {
	id := c.Next(key)
	in := c.Interaction(id)
	passOn(c, id, in.Keys, activates)
	fg := col(c, tLink)
	c.Column(key, ui.Style{Gap: 1, Focusable: true, Cursor: render.CursorPointer, Help: help, KeyMode: "button", Role: ui.RoleLink,
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
