package widget

import (
	"fmt"
	"image/color"
	"math"
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tCardBg     = theme.Declare("card.bg", "bg.surface")
	tCardFg     = theme.Declare("card.fg", "fg")
	tCardMuted  = theme.Declare("card.fg.muted", "fg.faint")
	tCardCodeBg = theme.Declare("card.code.bg", "bg.base")
	tCardCode   = theme.Declare("card.code.fg", "fg")
	tCardLit    = theme.DeclareOn("card.selected.bg", "alpha(accent, 5%)", "bg.surface")
	tMeterTrack = theme.Declare("meter.track", "line")
)

// CardAction is one of an action card's buttons.
type CardAction struct {
	Label   string
	Key     string // typed while the card has focus, it does the same; an upper-case letter is ⇧ and the letter
	Primary bool
}

type ActionCardOpts struct {
	Kicker   string // a small upper-case line over the title: "SANDBOX · NETWORK"
	Title    string
	Meta     string // at the right of the kicker, or of the title without one
	Code     string // in a code box under the title: a command
	Detail   string // a muted line under the title
	Tone     color.NRGBA
	Actions  []CardAction
	Note     string // at the foot's right: the rule "Always" would make
	Inline   bool   // the actions at the title's right, for a one-line card
	Width    ui.Size
	Help     string
	Label    string // its accessible name; the title by default
	Selected bool   // lit, as the selected block of a list
	// Icon is drawn in a ring before the title, in its tone: a security key.
	Icon func(c *ui.Ctx, tone color.NRGBA)
	// Dismiss puts a ✕ at its top right; it, or Escape while the card has focus, dismisses the card.
	Dismiss bool
	// Countdown draws a ring at its top right that empties as Left runs out of it, with Left as m:ss inside.
	Countdown, Left time.Duration
}

type ActionCardResult struct {
	Action    int // the action clicked or typed, or -1
	Dismissed bool
}

// ActionCard is a card that wants an answer (12-inbox, 15-agent): a toned edge and dot, a kicker and title, a code
// box, whatever body adds, and its actions as buttons with their keys. Typing an action's key while the card or
// anything in it has focus picks it.
func ActionCard(c *ui.Ctx, key any, o ActionCardOpts, body func(c *ui.Ctx)) ActionCardResult {
	res := ActionCardResult{Action: -1}
	id := c.Next(key)
	in := c.Interaction(id)
	passOn(c, id, in.Keys, func(k render.KeyEvent) bool {
		if o.Dismiss && k.Mods == 0 && k.Key == "Esc" {
			res.Dismissed = true
			return true
		}
		for i, a := range o.Actions {
			if a.Key != "" && cardKey(k) == a.Key {
				res.Action = i
				return true
			}
		}
		return false
	})
	w := o.Width
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	label := o.Label
	if label == "" {
		label = o.Title
	}
	fill := col(c, tCardBg)
	if o.Selected {
		fill = col(c, tCardLit)
	}
	button := func(c *ui.Ctx, i int) {
		a := o.Actions[i]
		if Button(c, i, a.Label, ButtonOpts{Primary: a.Primary, Help: o.Help, Key: keyLabel(a.Key)}) {
			res.Action = i
		}
	}
	// The width the card had last frame says whether the actions fit beside the title, and where they wrap.
	avail := float32(0)
	if r, ok := c.Rect(id); ok {
		avail = r.Dx() - 28
	}
	widths := make([]float32, len(o.Actions))
	all := float32(0)
	for i, a := range o.Actions {
		widths[i] = buttonWidth(c, a.Label, keyLabel(a.Key))
		all += widths[i] + 8
	}
	title := c.Shaper().Shape(o.Title, uiText(c, 12.5, typeset.SemiBold, col(c, tCardFg)).Font).Advance
	inline := o.Inline && (avail == 0 || title+all+40 < avail)
	var rows [][]int
	line := float32(0)
	for i := range o.Actions {
		if len(rows) == 0 || avail > 0 && line+widths[i] > avail {
			rows, line = append(rows, nil), 0
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], i)
		line += widths[i] + 8
	}
	noteBeside := o.Note == "" || avail == 0 || line+c.Shaper().Shape(o.Note, monoText(9.5, typeset.Normal, col(c, tCardMuted), 0).Font).Advance+16 < avail
	muted := monoText(9.5, typeset.Normal, col(c, tCardMuted), 0)
	corner := func(c *ui.Ctx) {
		if o.Countdown > 0 {
			countdownRing(c, o.Left, o.Countdown, o.Tone)
		}
		if o.Dismiss && IconButton(c, "dismiss", "✕", IconButtonOpts{Label: "Dismiss", Tooltip: "Dismiss (Esc): it waits on in the inbox", Help: o.Help, Size: 18}) {
			res.Dismissed = true
		}
	}
	c.Column(key, ui.Style{W: w, Pad: ui.PadXY(14, 12), Gap: 9, Fill: fill, Stroke: alpha(o.Tone, .55), Border: 1, Radius: 8, Focusable: true,
		KeyMode: "card", Help: o.Help, Role: ui.RoleGroup, Label: label}, func(c *ui.Ctx) {
		if o.Kicker != "" {
			c.Row("kicker", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("k", strings.ToUpper(o.Kicker), monoText(9.5, typeset.SemiBold, o.Tone, .14))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if o.Meta != "" {
					c.Text("meta", o.Meta, muted)
				}
				corner(c)
			})
		}
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			if o.Icon != nil {
				c.Row("icon", ui.Style{W: ui.Px(30), H: ui.Px(30), Radius: 15, Stroke: alpha(o.Tone, .6), Border: 1, Fill: alpha(o.Tone, .1), Main: ui.Center,
					Cross: ui.Center}, func(c *ui.Ctx) {
					o.Icon(c, o.Tone)
				})
			} else {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: o.Tone}, nil)
			}
			c.Column("titles", ui.Style{W: ui.Flex(1), Gap: 3}, func(c *ui.Ctx) {
				c.Text("title", o.Title, uiText(c, 12.5, typeset.SemiBold, col(c, tCardFg)))
				if o.Detail != "" {
					c.Text("detail", o.Detail, muted)
				}
			})
			if o.Kicker == "" && o.Meta != "" {
				c.Text("meta", o.Meta, muted)
			}
			if o.Kicker == "" {
				corner(c)
			}
			if inline {
				c.Row("actions", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					for i := range o.Actions {
						button(c, i)
					}
				})
			}
		})
		if o.Code != "" {
			c.Column("code", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 7), Gap: 2, Fill: col(c, tCardCodeBg), Stroke: col(c, tInputBorder), Border: 1,
				Radius: 5, Clip: true}, func(c *ui.Ctx) {
				for i, l := range strings.Split(o.Code, "\n") {
					c.Text(i, l, monoText(12, typeset.Normal, col(c, tCardCode), 0))
				}
			})
		}
		if body != nil {
			body(c)
		}
		if inline || len(o.Actions) == 0 && o.Note == "" {
			return
		}
		for r, row := range rows {
			c.Row(fmt.Sprint("foot", r), ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				for _, i := range row {
					button(c, i)
				}
				if r == len(rows)-1 && noteBeside && o.Note != "" {
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("note", o.Note, muted)
				}
			})
		}
		if !noteBeside || len(rows) == 0 && o.Note != "" {
			c.Text("note", o.Note, muted)
		}
	})
	return res
}

// countdownRing is a small ring emptying as left runs out of all, with left as m:ss inside.
func countdownRing(c *ui.Ctx, left, all time.Duration, tone color.NRGBA) {
	frac := min(max(float32(left)/float32(all), 0), 1)
	secs := int(left.Round(time.Second) / time.Second)
	label := fmt.Sprintf("%d:%02d", secs/60, secs%60)
	track := col(c, tMeterTrack)
	c.Row("ring", ui.Style{W: ui.Px(32), H: ui.Px(32), Main: ui.Center, Cross: ui.Center, Role: ui.RoleStatus, Label: label + " left"}, func(c *ui.Ctx) {
		c.Box("arc", ui.Style{Float: &ui.Float{Fill: true}, W: ui.Flex(1), H: ui.Flex(1), Z: ui.LayerUnder}, func(c *ui.Ctx) {
			c.Paint("arc", ui.Flex(1), ui.Flex(1), func(f render.Frame, r render.Rect) {
				mid := render.Point{X: (r.Min.X + r.Max.X) / 2, Y: (r.Min.Y + r.Max.Y) / 2}
				rad := min(r.Dx(), r.Dy())/2 - 1.5
				arc := func(to float32, tone color.NRGBA) {
					const steps = 48
					n := int(to*steps + .5)
					if n == 0 {
						return
					}
					var p render.Path
					for i := 0; i <= n; i++ {
						a := -math.Pi/2 + 2*math.Pi*float64(i)/steps
						pt := render.Point{X: mid.X + rad*float32(math.Cos(a)), Y: mid.Y + rad*float32(math.Sin(a))}
						if i == 0 {
							p.MoveTo(pt)
						} else {
							p.LineTo(pt)
						}
					}
					f.Path(&p, render.Style{Stroke: tone, Width: 2})
				}
				arc(1, track)
				arc(frac, tone)
			})
		})
		c.Text("t", label, monoText(8.5, typeset.SemiBold, col(c, tCardFg), 0))
	})
}

// cardKey is a key press as an action's Key names it: "y", or "N" for ⇧n.
func cardKey(k render.KeyEvent) string {
	switch k.Mods {
	case 0:
		return k.Key
	case render.ModShift:
		return strings.ToUpper(k.Key)
	}
	return ""
}

// keyLabel is how a key cap shows an action's Key: "⇧n" for "N".
func keyLabel(k string) string {
	if len(k) == 1 && k != strings.ToLower(k) {
		return "⇧" + strings.ToLower(k)
	}
	return k
}

type MeterOpts struct {
	Tone   color.NRGBA
	Width  ui.Size // fills by default
	Height float32 // 4 by default
	Label  string  // its accessible name
	// Then are more parts after frac's, each in its own tone: the review ledger's skimmed after its read.
	Then []MeterPart
	// To hatches on from frac to where it's headed, with a tick there: a plan window's share at its reset.
	To float64
}

// MeterPart is a stretch of a meter: its share (0–1) and its tone.
type MeterPart struct {
	Frac float64
	Tone color.NRGBA
}

// Meter is a thin bar filled to frac (0–1): a context window, a plan's usage; with Then, a stacked bar of parts.
func Meter(c *ui.Ctx, key any, frac float64, o MeterOpts) {
	frac = min(max(frac, 0), 1)
	label := fmt.Sprintf("%s %d%%", o.Label, int(frac*100+.5))
	frac = float64(c.Ease(easedKey{key}, float32(frac)))
	w := o.Width
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	h := o.Height
	if h == 0 {
		h = 4
	}
	track := alpha(col(c, tMeterTrack), 1)
	parts := append([]MeterPart{{Frac: frac, Tone: o.Tone}}, o.Then...)
	to := min(max(o.To, frac), 1)
	hatch, tick := col(c, tMeterProjection), col(c, tMeterProjectMark)
	c.Box(key, ui.Style{W: w, Role: ui.RoleStatus, Label: label}, func(c *ui.Ctx) {
		c.Paint("bar", ui.Flex(1), ui.Px(h), func(f render.Frame, r render.Rect) {
			f.Rect(r, render.Style{Fill: track, Radius: min(h/2, 2)})
			if to > frac {
				pr := render.Rect{Min: render.Point{X: r.Min.X + r.Dx()*float32(frac), Y: r.Min.Y}, Max: render.Point{X: r.Min.X + r.Dx()*float32(to), Y: r.Max.Y}}
				f.PushClip(pr, 0)
				var p render.Path
				for x := pr.Min.X - r.Dy(); x < pr.Max.X; x += 4 {
					p.MoveTo(render.Point{X: x, Y: r.Max.Y}).LineTo(render.Point{X: x + r.Dy(), Y: r.Min.Y})
				}
				f.Path(&p, render.Style{Stroke: hatch, Width: 1.5})
				f.Pop()
				f.Rect(render.XYWH(pr.Max.X-.5, r.Min.Y-3, 1, r.Dy()+6), render.Style{Fill: tick})
			}
			at := float64(0)
			for _, p := range parts {
				from := min(max(at, 0), 1)
				at += p.Frac
				to := min(max(at, 0), 1)
				if to > from {
					x0, x1 := r.Min.X+r.Dx()*float32(from), r.Min.X+r.Dx()*float32(to)
					f.Rect(render.Rect{Min: render.Point{X: x0, Y: r.Min.Y}, Max: render.Point{X: x1, Y: r.Max.Y}}, render.Style{Fill: p.Tone, Radius: min(h/2, 2)})
				}
			}
		})
	})
}

// easedKey is a widget's value eased on the motion spring (Ctx.Ease), keyed by the widget's own key.
type easedKey struct{ key any }

// buttonWidth is how wide Button draws label with a key cap.
func buttonWidth(c *ui.Ctx, label, key string) float32 {
	w := c.Shaper().Shape(label, uiText(c, 12, typeset.SemiBold, col(c, tButtonFg)).Font).Advance + 22
	if key != "" {
		w += c.Shaper().Shape(key, monoText(9.5, typeset.Medium, col(c, tButtonFg), 0).Font).Advance + 16
	}
	return w
}
