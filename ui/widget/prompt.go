package widget

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"strings"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tPromptBg       = theme.Declare("prompt.bg", "bg.surface")
	tPromptFg       = theme.Declare("prompt.fg", "fg")
	tPromptMuted    = theme.Declare("prompt.fg.muted", "fg.faint")
	tPromptWhy      = theme.Declare("prompt.why.fg", "fg.muted")
	tPromptWhyEdge  = theme.Declare("prompt.why.edge", "line.strong")
	tPromptDetailBg = theme.Declare("prompt.detail.bg", "bg.base")
	tPromptDetailFg = theme.Declare("prompt.detail.fg", "fg")
	tPromptLine     = theme.Declare("prompt.line", "line")
	tPromptOptionOn = theme.Declare("prompt.option.selected", "bg.overlay")
	tPromptKey      = theme.Declare("prompt.option.key", "fg.muted")
	tPromptDanger   = theme.Declare("prompt.option.danger", "error")
)

// The prompt card's measures.
const (
	promptWidth   = 760
	promptMargin  = 24 // kept clear of the window's edges
	promptDetail  = .4 // the most of the window's height its detail takes
	promptRowH    = 19 // a line of the detail
	promptPad     = 22 // its sides
	promptWhyMax  = 3  // the lines of its why shown until "more"
	promptScroll  = 3  // the lines ⇧↓ and ⇧↑ scroll
	promptOptionH = 34
)

// PromptOption is one of a prompt card's answers: a row of its list.
type PromptOption struct {
	Key    string // typed while the card has focus, it picks it; an upper-case letter is ⇧ and the letter
	Label  string
	Note   string // at the row's right: the rule it makes
	Danger bool   // a refusal, in the error colour
}

// PromptLine is a line of a prompt card's detail, wrapped to its width: Lead (a "$", a key, a diff's gutter) in its own
// colour, then Text, its wrapped lines indented past Lead. Zero colours are the detail's own.
type PromptLine struct {
	Lead, Text     string
	LeadTone, Tone color.NRGBA
	Fill           color.NRGBA // the line's background: a diff's added or removed line
}

type PromptCardOpts struct {
	Kicker string // a small upper-case line at its top: "PRICING-ROUNDING · PERMISSION"
	Count  string // at the kicker's right: "1 OF 3"
	Title  string
	Where  string // a muted line under the title
	Why    string // what led to it, under WHY: three lines, then "more"
	Tone   color.NRGBA
	// Detail is what it's about, in a box that scrolls inside itself once it's taller than 40% of the window: the
	// wheel, ⇧↑/⇧↓ and PgUp/PgDn scroll it. Only the lines in view are built.
	Detail      []PromptLine
	DetailLabel string // the detail's accessible name
	// Scopes are a choice over the options (a host or its domain), *Scope the one chosen: a click or ←/→ picks.
	Scopes  []string
	Scope   *int
	Options []PromptOption
	Help    string
	Label   string // its accessible name; the title by default
	// Dismiss puts a ✕ at its top right; it, or Escape, dismisses the card.
	Dismiss bool
	// TakeFocus gives it keyboard focus as it appears; for CardKeyGuard after, its keys answer nothing.
	TakeFocus bool
}

type PromptCardResult struct {
	Option    int // the option picked, or -1
	Dismissed bool
}

// promptState is a prompt card's highlighted option and whether its why is open.
type promptState struct {
	sel  int
	more bool
}

// promptRows are a detail's lines wrapped to a width, kept while neither changes.
type promptRows struct {
	sum   uint64
	width float32
	rows  []promptRow
}

type promptRow struct {
	lead, text string
	line       int // the PromptLine it's from
	first      bool
}

// PromptWidth is how wide a prompt card is drawn in the window: 760, less in a narrow one.
func PromptWidth(c *ui.Ctx) float32 {
	return min(promptWidth, max(c.Size().X-2*promptMargin, 240))
}

// PromptCard is a decision that holds something until it's answered (the permission prompt): a kicker with its place
// in the queue and ✕, a title, where it is, why it's asked, its detail scrolling inside itself, and its answers as
// a list: ↑/↓ move the highlight and ↵ picks it, an option's key picks it, and a click picks a row. It never grows
// past the window: only the detail gives way.
func PromptCard(c *ui.Ctx, key any, o PromptCardOpts) PromptCardResult {
	res := PromptCardResult{Option: -1}
	id := c.Next(key)
	in := c.Interaction(id)
	st := ui.State[promptState](c, id)
	up := ui.State[cardUp](c, ui.Child(id, "up"))
	if !up.seen {
		up.at, up.seen = c.Now(), true
	}
	settling := o.TakeFocus && c.Now().Sub(up.at) < CardKeyGuard
	if settling {
		c.WakeAt(up.at.Add(CardKeyGuard))
	}
	detail := ui.Child(ui.Child(ui.Child(id, "detailbox"), "frame"), "detail")
	_, view := c.ScrollExtent(detail)
	st.sel = clampInt(st.sel, 0, max(len(o.Options)-1, 0))
	passOn(c, id, in.Keys, func(k render.KeyEvent) bool {
		if settling {
			return true // typed as it appeared: taken, so it reaches nothing under the card either
		}
		switch {
		case o.Dismiss && k.Mods == 0 && k.Key == "Esc":
			res.Dismissed = true
		case k.Mods == render.ModShift && (k.Key == "Up" || k.Key == "Down"):
			d := float32(promptScroll * promptRowH)
			if k.Key == "Up" {
				d = -d
			}
			c.ScrollBy(detail, render.Point{Y: d})
		case k.Mods == 0 && (k.Key == "PageUp" || k.Key == "PageDown"):
			d := max(view.Y-promptRowH, promptRowH)
			if k.Key == "PageUp" {
				d = -d
			}
			c.ScrollBy(detail, render.Point{Y: d})
		case k.Mods == 0 && k.Key == "Up":
			st.sel = max(st.sel-1, 0)
		case k.Mods == 0 && k.Key == "Down":
			st.sel = min(st.sel+1, max(len(o.Options)-1, 0))
		case k.Mods == 0 && k.Key == "CR" && len(o.Options) > 0:
			res.Option = st.sel
		case k.Mods == 0 && (k.Key == "Left" || k.Key == "Right") && o.Scope != nil && len(o.Scopes) > 0:
			if k.Key == "Left" {
				*o.Scope = max(*o.Scope-1, 0)
			} else {
				*o.Scope = min(*o.Scope+1, len(o.Scopes)-1)
			}
		default:
			for i, a := range o.Options {
				if a.Key != "" && cardKey(k) == a.Key {
					res.Option = i
					return true
				}
			}
			return false
		}
		return true
	})
	win := c.Size()
	width := PromptWidth(c)
	inner := width - 2*promptPad
	label := o.Label
	if label == "" {
		label = o.Title
	}
	// The card fits the window: what isn't the detail is drawn whole, and the detail takes what's left, up to 40%.
	most := max(win.Y-2*promptMargin, 120)
	rest := float32(0)
	if cr, ok := c.Rect(id); ok {
		dr, _ := c.Rect(ui.Child(id, "detailbox"))
		rest = cr.Dy() - dr.Dy()
	}
	room := min(win.Y*promptDetail, most-rest)
	muted := monoText(11, typeset.Medium, col(c, tPromptMuted), 0)
	c.Column(key, ui.Style{W: ui.Px(width), H: ui.Size{Kind: ui.Fit}.Clamp(0, most), Pad: ui.Insets{T: 16, B: 18}, Fill: col(c, tPromptBg),
		Stroke: o.Tone, Border: 1, Radius: 12, Clip: true, Focusable: true, FocusOnOpen: o.TakeFocus, KeyMode: "card", Help: o.Help,
		Role: ui.RoleGroup, Label: label}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: 14}, Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("kicker", strings.ToUpper(o.Kicker), monoText(10.5, typeset.SemiBold, o.Tone, .18))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if o.Count != "" {
				c.Text("count", strings.ToUpper(o.Count), monoText(10.5, typeset.Medium, col(c, tPromptMuted), .1))
			}
			if o.Dismiss && IconButton(c, "dismiss", "✕", IconButtonOpts{Label: "Dismiss", Tooltip: "Dismiss (Esc): it waits on in the inbox", Help: o.Help,
				Size: 22}) {
				res.Dismissed = true
			}
		})
		c.Column("titles", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: promptPad, T: 10, B: 14}, Gap: 6}, func(c *ui.Ctx) {
			Wrap(c, "title", o.Title, inner, uiText(c, 17, typeset.SemiBold, col(c, tPromptFg)))
			if o.Where != "" {
				c.Text("where", o.Where, muted)
			}
		})
		if o.Why != "" {
			promptWhy(c, o, st, inner)
		}
		if len(o.Detail) > 0 {
			promptDetailBox(c, o, inner, max(room, promptRowH+50))
		}
		if len(o.Scopes) > 0 && o.Scope != nil {
			c.Row("scopes", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: promptPad, B: 12}}, func(c *ui.Ctx) {
				Segmented(c, "scope", o.Scopes, o.Scope, SegmentedOpts{Help: o.Help, Label: "Allow"})
			})
		}
		if i := promptOptions(c, o, st); i >= 0 {
			res.Option = i
		}
	})
	return res
}

// promptWhy is the card's WHY: three lines of it, and the rest behind "more".
func promptWhy(c *ui.Ctx, o PromptCardOpts, st *promptState, inner float32) {
	ts := uiText(c, 13, typeset.Normal, col(c, tPromptWhy))
	c.Row("why", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: promptPad, B: 14}}, func(c *ui.Ctx) {
		c.Row("box", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: col(c, tPromptWhyEdge)}, nil)
			c.Column("text", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 2, B: 2}, Gap: 4, Role: ui.RoleText, Label: "Why: " + o.Why}, func(c *ui.Ctx) {
				c.Text("k", "WHY", monoText(10, typeset.Medium, col(c, tPromptMuted), .15))
				lines := WrapLines(c.Shaper(), o.Why, inner-14, ts.Font)
				shown := lines
				if !st.more && len(lines) > promptWhyMax {
					shown = lines[:promptWhyMax]
				}
				c.Column("lines", ui.Style{Gap: 3}, func(c *ui.Ctx) {
					for i, l := range shown {
						c.Text(i, l, ts)
					}
				})
				if len(lines) > promptWhyMax {
					label := "more"
					if st.more {
						label = "less"
					}
					if Link(c, "more", label, o.Help) {
						st.more = !st.more
					}
				}
			})
		})
	})
}

// promptDetailBox is the card's detail: its lines wrapped to the box, those in view built, scrolling inside a box
// no taller than room, with how much is past its foot under it.
func promptDetailBox(c *ui.Ctx, o PromptCardOpts, inner, room float32) {
	text := inner - 28
	rows := promptWrap(c, o.Detail, text)
	h := min(float32(len(rows))*promptRowH, room-50)
	h = max(h, promptRowH)
	c.Column("detailbox", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: promptPad, B: 12}, Gap: 4}, func(c *ui.Ctx) {
		c.Column("frame", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Fill: col(c, tPromptDetailBg), Stroke: col(c, tPromptLine), Border: 1,
			Radius: 8, Clip: true}, func(c *ui.Ctx) {
			c.List("detail", ui.Style{W: ui.Flex(1), H: ui.Px(h), Role: ui.RoleText, Label: o.DetailLabel, Help: o.Help}, len(rows), promptRowH,
				func(c *ui.Ctx, i int) {
					r := rows[i]
					l := o.Detail[r.line]
					c.Row("r", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Cross: ui.Center, Fill: l.Fill}, func(c *ui.Ctx) {
						if r.lead != "" {
							lead := cmpColor(l.LeadTone, col(c, tPromptMuted))
							if !r.first {
								lead = color.NRGBA{}
							}
							c.Text("lead", r.lead, monoText(12.5, typeset.Medium, lead, 0))
						}
						c.Text("t", r.text, monoText(12.5, typeset.Medium, cmpColor(l.Tone, col(c, tPromptDetailFg)), 0))
					})
				})
		})
		id := ui.Child(c.ID(), "frame")
		content, view := c.ScrollExtent(ui.Child(id, "detail"))
		if below := content.Y - view.Y - c.Scroll(ui.Child(id, "detail")).Y; view.Y > 0 && below > 1 {
			n := int((below + promptRowH - 1) / promptRowH)
			c.Row("more", ui.Style{W: ui.Flex(1), Main: ui.End, Role: ui.RoleStatus, Label: plural(n, "more line") + " below"}, func(c *ui.Ctx) {
				c.Text("t", "↓ "+plural(n, "more line")+" · ⇧↓ PgDn", monoText(10, typeset.Medium, col(c, tPromptMuted), 0))
			})
		}
	})
}

// promptWrap is a detail's lines wrapped to width, each past its lead, kept while they and the width stay the same.
func promptWrap(c *ui.Ctx, lines []PromptLine, width float32) []promptRow {
	h := fnv.New64a()
	for _, l := range lines {
		h.Write([]byte(l.Lead))
		h.Write([]byte{0})
		h.Write([]byte(l.Text))
		h.Write([]byte{1})
	}
	kept := ui.State[promptRows](c, ui.Child(c.ID(), "wrapped"))
	if kept.sum == h.Sum64() && kept.width == width && kept.rows != nil {
		return kept.rows
	}
	font := typeset.FontSpec{Family: typeset.Mono, Weight: typeset.Medium, Size: 12.5}
	var rows []promptRow
	for i, l := range lines {
		lead := c.Shaper().Shape(l.Lead, font).Advance
		pad := strings.Repeat(" ", len([]rune(l.Lead)))
		for j, w := range codeLines(c.Shaper(), l.Text, max(width-lead, 40), font) {
			r := promptRow{lead: l.Lead, text: w.text, line: i, first: j == 0}
			if j > 0 && l.Lead != "" {
				r.lead = pad
			}
			rows = append(rows, r)
		}
	}
	kept.sum, kept.width, kept.rows = h.Sum64(), width, rows
	return rows
}

// promptOptions are the card's answers as a list; it returns the one clicked, or -1.
func promptOptions(c *ui.Ctx, o PromptCardOpts, st *promptState) int {
	picked := -1
	c.Column("options", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: promptPad, R: promptPad}}, func(c *ui.Ctx) {
		c.Column("list", ui.Style{W: ui.Flex(1), Stroke: col(c, tPromptLine), Border: 1, Radius: 8, Clip: true, Role: ui.RoleList, Label: "Answers"},
			func(c *ui.Ctx) {
				for i, a := range o.Options {
					if i > 0 {
						c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: col(c, tPromptLine)}, nil)
					}
					on := i == st.sel
					fill, fg, keyFg := render.Color{}, col(c, tPromptFg), col(c, tPromptKey)
					if on {
						fill = col(c, tPromptOptionOn)
					}
					if a.Danger {
						fg, keyFg = col(c, tPromptDanger), alpha(col(c, tPromptDanger), .8)
					}
					name := a.Label
					if a.Key != "" {
						name += ", " + promptKeyLabel(a.Key)
					}
					if a.Note != "" {
						name += ", " + a.Note
					}
					if Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(promptOptionH), Pad: ui.PadXY(12, 0), Gap: 12, Cross: ui.Center, Fill: fill,
						EaseFill: true, Role: ui.RoleListItem, Label: name, Selected: on, Help: o.Help}, func(c *ui.Ctx) {
						c.Row("key", ui.Style{W: ui.Size{Kind: ui.Fit}.Clamp(22, 40), H: ui.Px(17), Pad: ui.PadXY(5, 0), Main: ui.Center, Cross: ui.Center,
							Stroke: alpha(keyFg, .45), Border: 1, Radius: 3}, func(c *ui.Ctx) {
							c.Text("k", promptKeyLabel(a.Key), monoText(10.5, typeset.Medium, keyFg, 0))
						})
						c.Text("label", a.Label, uiText(c, 13, typeset.Medium, fg))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if a.Note != "" {
							c.Text("note", a.Note, monoText(11, typeset.Medium, col(c, tPromptMuted), 0))
						}
					}).Clicked {
						st.sel, picked = i, i
					}
				}
			})
	})
	return picked
}

// promptKeyLabel is how an option's key cap shows its Key: "⇧A" for "A".
func promptKeyLabel(k string) string {
	if len(k) == 1 && k != strings.ToLower(k) {
		return "⇧" + k
	}
	return k
}

func cmpColor(c, or color.NRGBA) color.NRGBA {
	if c == (color.NRGBA{}) {
		return or
	}
	return c
}

func plural(n int, what string) string {
	if n == 1 {
		return "1 " + what
	}
	return fmt.Sprintf("%d %ss", n, what)
}
