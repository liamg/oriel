package widget

import (
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

var (
	tDateFg      = theme.Declare("datepicker.day.fg", "fg")
	tDateOther   = theme.Declare("datepicker.day.other", "fg.ghost")
	tDateRange   = theme.Declare("datepicker.range", "accent.bg")
	tDateEnd     = theme.Declare("datepicker.end", "accent")
	tDateEndFg   = theme.Declare("datepicker.end.fg", "accent.ink")
	tDateCursor  = theme.Declare("datepicker.cursor", "fg.muted")
	tDateToday   = theme.Declare("datepicker.today", "accent")
	tDateWeekday = theme.Declare("datepicker.weekday", "fg.faint")
)

// DateRange is a range of whole days: From and To are midnights, To the last day in it.
type DateRange struct {
	From, To time.Time
}

// IsZero says no range is set.
func (r DateRange) IsZero() bool { return r.From.IsZero() }

// String is the range as it's typed: 2026-09-01..2026-09-30.
func (r DateRange) String() string {
	if r.IsZero() {
		return ""
	}
	return r.From.Format(time.DateOnly) + ".." + r.To.Format(time.DateOnly)
}

// ParseDateRange reads a range as it's typed, 2026-09-01..2026-09-30 (either order), or one day, in loc.
func ParseDateRange(s string, loc *time.Location) (DateRange, error) {
	a, b, ok := strings.Cut(strings.TrimSpace(s), "..")
	if !ok {
		b = a
	}
	from, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(a), loc)
	if err != nil {
		return DateRange{}, fmt.Errorf("a range is 2026-09-01..2026-09-30: %w", err)
	}
	to, err := time.ParseInLocation(time.DateOnly, strings.TrimSpace(b), loc)
	if err != nil {
		return DateRange{}, fmt.Errorf("a range is 2026-09-01..2026-09-30: %w", err)
	}
	if to.Before(from) {
		from, to = to, from
	}
	return DateRange{From: from, To: to}, nil
}

type DatePickerOpts struct {
	Label    string         // its accessible name
	Help     string         // its help id
	Today    time.Time      // the day marked as today, where the calendar opens with no range; the UI's clock by default
	Location *time.Location // the days' zone; time.Local by default
}

type DatePickerResult struct {
	Picked    bool // a range was picked this frame, into *r
	Cancelled bool // Escape: *r is as it was
}

type datePickerState struct {
	open   bool      // built last frame
	cursor time.Time // the day the keys move
	anchor time.Time // the start picked, waiting for the end
	text   string
	bad    bool // the typed range doesn't parse
}

// DatePicker picks a range of days on a month's calendar (weeks from Monday). The arrows move the day under the
// cursor (← → a day, ↑ ↓ a week, PageUp/PageDown a month), ⏎ picks it: the first pick is one end, the next the other.
// A click picks a day the same way; ‹ › page through months. A range can be typed, 2026-09-01..2026-09-30, and
// picked with ⏎. Escape cancels. It takes focus when it first shows; *r changes only when a range is picked.
func DatePicker(c *ui.Ctx, key any, r *DateRange, o DatePickerOpts) DatePickerResult {
	loc := o.Location
	if loc == nil {
		loc = time.Local
	}
	midnight := func(t time.Time) time.Time {
		t = t.In(loc)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, loc)
	}
	today := o.Today
	if today.IsZero() {
		today = c.Now()
	}
	today = midnight(today)
	id := c.Next(key)
	st := ui.State[datePickerState](c, id)
	grid := ui.Child(id, "grid")
	if !st.open {
		*st = datePickerState{open: true, cursor: today, text: r.String()}
		if !r.IsZero() {
			st.cursor = midnight(r.To)
		}
		c.Focus(grid)
	}
	var res DatePickerResult
	pick := func(d time.Time) {
		if st.anchor.IsZero() {
			st.anchor, st.cursor = d, d
			return
		}
		from, to := st.anchor, d
		if to.Before(from) {
			from, to = to, from
		}
		*r = DateRange{From: from, To: to}
		st.anchor, st.cursor, st.text, st.bad = time.Time{}, d, r.String(), false
		res.Picked = true
	}
	in := c.Interaction(grid)
	var rest []render.KeyEvent
	for _, k := range in.Keys {
		switch {
		case k.Mods != 0:
			rest = append(rest, k)
		case k.Key == "Left":
			st.cursor = st.cursor.AddDate(0, 0, -1)
		case k.Key == "Right":
			st.cursor = st.cursor.AddDate(0, 0, 1)
		case k.Key == "Up":
			st.cursor = st.cursor.AddDate(0, 0, -7)
		case k.Key == "Down":
			st.cursor = st.cursor.AddDate(0, 0, 7)
		case k.Key == "PageUp":
			st.cursor = st.cursor.AddDate(0, -1, 0)
		case k.Key == "PageDown":
			st.cursor = st.cursor.AddDate(0, 1, 0)
		case k.Key == "CR" || k.Key == " ":
			pick(st.cursor)
		case k.Key == "Esc":
			res.Cancelled = true
		default:
			rest = append(rest, k)
		}
	}
	c.PassKeys(grid, rest)
	month := time.Date(st.cursor.Year(), st.cursor.Month(), 1, 0, 0, 0, 0, loc)
	shown := *r
	if !st.anchor.IsZero() {
		shown = DateRange{From: st.anchor, To: st.cursor}
		if shown.To.Before(shown.From) {
			shown.From, shown.To = shown.To, shown.From
		}
	}
	c.Column(key, ui.Style{Gap: 8, Role: ui.RoleGroup, Label: o.Label, Help: o.Help}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Px(7 * dayW), Cross: ui.Center}, func(c *ui.Ctx) {
			if IconButton(c, "prev", "‹", IconButtonOpts{Label: "Previous month", Help: o.Help}) {
				st.cursor = st.cursor.AddDate(0, -1, 0)
			}
			c.Row("m", ui.Style{W: ui.Flex(1), Main: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", month.Format("January 2006"), uiText(c, 12.5, typeset.SemiBold, col(c, tDateFg)))
			})
			if IconButton(c, "next", "›", IconButtonOpts{Label: "Next month", Help: o.Help}) {
				st.cursor = st.cursor.AddDate(0, 1, 0)
			}
		})
		c.Column("grid", ui.Style{Focusable: true, KeyMode: "datepicker", Help: o.Help, Role: ui.RoleGroup, Label: cmpStr(o.Label, "Range") + ", calendar"}, func(c *ui.Ctx) {
			c.Row("wd", ui.Style{H: ui.Px(20), Cross: ui.Center}, func(c *ui.Ctx) {
				for i, d := range []string{"Mo", "Tu", "We", "Th", "Fr", "Sa", "Su"} {
					c.Row(i, ui.Style{W: ui.Px(dayW), Main: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", d, monoText(9.5, typeset.SemiBold, col(c, tDateWeekday), .06))
					})
				}
			})
			start := month.AddDate(0, 0, -((int(month.Weekday()) + 6) % 7))
			for w := range 6 {
				c.Row(w, ui.Style{H: ui.Px(dayH)}, func(c *ui.Ctx) {
					for i := range 7 {
						d := start.AddDate(0, 0, w*7+i)
						if dayCell(c, i, d, d.Month() == month.Month(), d.Equal(st.cursor) && in.Focused, d.Equal(today), shown, o.Help) {
							pick(d)
						}
					}
				})
			}
		})
		text := st.text
		ir := Input(c, "typed", &text, InputOpts{Label: cmpStr(o.Label, "Range") + ", typed", Placeholder: "2026-09-01..2026-09-30",
			Width: ui.Px(7 * dayW), Invalid: st.bad, Help: o.Help, TabMoves: true})
		st.text = text
		if ir.Changed {
			st.bad = false
		}
		if ir.Cancelled {
			res.Cancelled = true
		}
		if ir.Submitted {
			if v, err := ParseDateRange(st.text, loc); err != nil {
				st.bad = true
			} else {
				*r, st.anchor, st.cursor, st.text = v, time.Time{}, v.To, v.String()
				res.Picked = true
			}
		}
	})
	if res.Cancelled {
		st.anchor = time.Time{}
	}
	return res
}

const (
	dayW = 30
	dayH = 26
)

// dayCell is one day on the calendar, filled when it's in r and solid at r's ends. It reports a click.
func dayCell(c *ui.Ctx, key any, d time.Time, inMonth, cursor, today bool, r DateRange, help string) bool {
	fg, fill := col(c, tDateFg), render.Color{}
	if !inMonth {
		fg = col(c, tDateOther)
	}
	in := !r.IsZero() && !d.Before(r.From) && !d.After(r.To)
	end := in && (d.Equal(r.From) || d.Equal(r.To))
	switch {
	case end:
		fill, fg = col(c, tDateEnd), col(c, tDateEndFg)
	case in:
		fill = col(c, tDateRange)
	}
	stroke := render.Color{}
	switch {
	case cursor:
		stroke = col(c, tDateCursor)
	case today && !end:
		stroke = alpha(col(c, tDateToday), .6)
	}
	radius := float32(4)
	if in && !end {
		radius = 0
	}
	weight := typeset.Normal
	if today {
		weight = typeset.SemiBold
	}
	return Clickable(c, key, ui.Style{Dir: ui.Row, W: ui.Px(dayW), H: ui.Px(dayH), Main: ui.Center, Cross: ui.Center, Radius: radius, Fill: fill,
		Stroke: stroke, Border: 1, Help: help, Label: d.Format(time.DateOnly), Selected: in}, func(c *ui.Ctx) {
		c.Text("t", fmt.Sprint(d.Day()), monoText(11, weight, fg, 0))
	}).Clicked
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// DatePopover shows a DatePicker in a popover under anchor while *open: a pick or Escape closes it, as does a click
// outside. It reports a pick, into *r.
func DatePopover(c *ui.Ctx, key any, anchor ui.ID, open *bool, r *DateRange, o DatePickerOpts) bool {
	picked := false
	Popover(c, key, anchor, open, PopoverOpts{Label: o.Label, Help: o.Help}, func(c *ui.Ctx) {
		res := DatePicker(c, "picker", r, o)
		picked = res.Picked
		if res.Picked || res.Cancelled {
			*open = false
		}
	})
	return picked
}

type DateSelectOpts struct {
	Label       string // its accessible name, and the picker's
	Placeholder string // what the button says with no range
	Help        string
	Disabled    bool
	Today       time.Time
	Location    *time.Location
}

type dateSelectState struct{ open bool }

// DateSelect is a button showing r ("Sep 1 – Sep 30") that opens a DatePicker under it. It returns the range picked
// and true on the frame one is; the caller keeps the value.
func DateSelect(c *ui.Ctx, key any, r DateRange, o DateSelectOpts) (DateRange, bool) {
	id := c.Next(key)
	st := ui.State[dateSelectState](c, id)
	label := o.Placeholder
	if !r.IsZero() {
		label = r.From.Format("Jan 2") + " – " + r.To.Format("Jan 2")
	}
	var picked bool
	c.Box(key, ui.Style{}, func(c *ui.Ctx) {
		b := c.Next("drop")
		if Button(c, "drop", label, ButtonOpts{Disabled: o.Disabled, Help: o.Help}) {
			st.open = true
		}
		picked = DatePopover(c, "pop", b, &st.open, &r, DatePickerOpts{Label: o.Label, Help: o.Help, Today: o.Today, Location: o.Location})
	})
	return r, picked
}
