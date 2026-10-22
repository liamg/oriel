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
	tBusyFg        = theme.Declare("busy.fg", "fg.faint")
	tBusyClock     = theme.Declare("busy.clock", "fg.ghost")
	tProgressTrack = theme.DeclareOn("progress.track", "alpha(fg, 10%)", "bg.surface")
	tProgressBar   = theme.Declare("progress.bar", "accent")
	tSkeletonFill  = theme.DeclareOn("skeleton.fill", "alpha(fg, 7%)", "bg.surface")
	tAsyncError    = theme.Declare("async.error", "warn")
	tAsyncEmpty    = theme.Declare("async.empty", "fg.faint")
	tButtonBusyFg  = theme.Declare("button.busy.fg", "fg.muted")
)

// Phase and LoadState live in ui, below the loaders that report them.
type (
	Phase     = ui.Phase
	LoadState = ui.LoadState
)

const (
	Idle    = ui.Idle
	Pending = ui.Pending
	Ready   = ui.Ready
	Failed  = ui.Failed
)

// Elapsed is how long the current load has run.
func Elapsed(now, since time.Time) string {
	d := now.Sub(since).Truncate(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm %02ds", int(d.Minutes()), int(d.Seconds())%60)
}

const defaultClock = 3 * time.Second

type BusyOpts struct {
	Label string        // what's loading; the "…" is added
	Size  float32       // the spinner's; the text follows. 12 when 0
	Tone  color.NRGBA   // the spinner's; spinner.arc when zero
	Clock time.Duration // show the elapsed time after this long; 0 is 3s, below 0 never
	Help  string
}

// Busy is the one inline loading line: a spinner, the label, what the load says it's doing, and how long it's run.
func Busy(c *ui.Ctx, key any, s LoadState, o BusyOpts) ui.ID {
	size := o.Size
	if size == 0 {
		size = 12
	}
	text := strings.TrimSuffix(o.Label, "…")
	if text != "" {
		text += "…"
	}
	if s.Note != "" {
		text = strings.TrimSpace(text + " " + s.Note)
	}
	clock := ""
	if o.Clock >= 0 && !s.Since.IsZero() {
		after := o.Clock
		if after == 0 {
			after = defaultClock
		}
		now := c.Now()
		if el := now.Sub(s.Since); el >= after {
			clock = Elapsed(now, s.Since)
			c.WakeAt(now.Truncate(time.Second).Add(time.Second))
		} else {
			c.WakeAt(s.Since.Add(after))
		}
	}
	name := text
	if clock != "" {
		name += " " + clock
	}
	return c.Row(key, ui.Style{Gap: 7, Cross: ui.Center, Role: ui.RoleStatus, Label: name, Help: o.Help}, func(c *ui.Ctx) {
		Spinner(c, "spin", SpinnerOpts{Size: size, Tone: o.Tone, Help: o.Help})
		if text != "" {
			c.Text("t", text, uiText(c, size, typeset.Normal, col(c, tBusyFg)))
		}
		if clock != "" {
			c.Text("clock", "· "+clock, monoText(size-1.5, typeset.Normal, col(c, tBusyClock), 0))
		}
	})
}

type ProgressOpts struct {
	Frac   float64 // 0..1; below 0 slides back and forth with no known end
	Height float32 // 2 for a bar along an edge, 6 for one on its own; 4 when 0
	Width  ui.Size // Flex(1) when zero
	Tone   color.NRGBA
	Track  color.NRGBA
	Label  string // read out, and shown above a bar 6px or taller
	Help   string
}

const progressSweep = 1500 * time.Millisecond

// ProgressBar is a loading bar: filled to Frac, or sliding when the end isn't known. With reduced motion, or the window
// unfocused (ui.Ctx.Decorative), a bar with no known end is a still, striped one.
func ProgressBar(c *ui.Ctx, key any, o ProgressOpts) ui.ID {
	h := o.Height
	if h == 0 {
		h = 4
	}
	w := o.Width
	if w == (ui.Size{}) {
		w = ui.Flex(1)
	}
	tone, track := o.Tone, o.Track
	if tone == (color.NRGBA{}) {
		tone = col(c, tProgressBar)
	}
	if track == (color.NRGBA{}) {
		track = col(c, tProgressTrack)
	}
	value := ""
	if o.Frac >= 0 {
		value = fmt.Sprintf("%.0f%%", math.Min(o.Frac, 1)*100)
	}
	if o.Frac >= 0 {
		o.Frac = float64(c.Ease(easedKey{key}, float32(math.Min(o.Frac, 1))))
	}
	phase, moving := 0.0, false
	if o.Frac < 0 {
		phase, moving = c.Decorative(progressSweep, 30)
	}
	still := !moving
	return c.Column(key, ui.Style{W: w, Gap: 4, Role: ui.RoleStatus, Label: strings.TrimSpace(o.Label + " " + value), Help: o.Help}, func(c *ui.Ctx) {
		if h >= 6 && o.Label != "" {
			c.Row("label", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
				c.Text("t", o.Label, uiText(c, 11, typeset.Normal, col(c, tBusyFg)))
				if value != "" {
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("v", value, monoText(10, typeset.Normal, col(c, tBusyClock), 0))
				}
			})
		}
		c.Paint("bar", ui.Flex(1), ui.Px(h), func(f render.Frame, r render.Rect) {
			rad := h / 2
			f.Rect(r, render.Style{Fill: track, Radius: rad})
			switch {
			case o.Frac >= 0:
				fill := r
				fill.Max.X = r.Min.X + r.Dx()*float32(math.Max(0, math.Min(o.Frac, 1)))
				if fill.Dx() > 0 {
					f.Rect(fill, render.Style{Fill: tone, Radius: rad})
				}
			case still:
				f.PushClip(r, rad)
				for x := r.Min.X; x < r.Max.X; x += 12 {
					f.Rect(render.Rect{Min: render.Point{X: x, Y: r.Min.Y}, Max: render.Point{X: min(x+6, r.Max.X), Y: r.Max.Y}}, render.Style{Fill: alpha(tone, .55)})
				}
				f.Pop()
			default:
				// A third of the track, eased from one end to the other and back.
				seg := r.Dx() / 3
				t := (1 - math.Cos(2*math.Pi*phase)) / 2
				x := r.Min.X + float32(t)*(r.Dx()-seg)
				f.Rect(render.Rect{Min: render.Point{X: x, Y: r.Min.Y}, Max: render.Point{X: x + seg, Y: r.Max.Y}}, render.Style{Fill: tone, Radius: rad})
			}
		})
	})
}

type SkeletonOpts struct {
	Rows   int       // rows of text-like bars
	RowH   float32   // 12 when 0
	Gap    float32   // between rows or blocks; 8 when 0
	Widths []float32 // each row's share of the width, repeating; 1, .8, .6 when empty
	Block  render.Point
	Count  int    // how many blocks; 1 when 0
	Dir    ui.Dir // the blocks' direction
	Width  ui.Size
	Label  string // read out; "Loading" when empty
	Help   string
}

const skeletonPulse = 1600 * time.Millisecond

// Skeleton is grey placeholders in the shape of what's coming: rows of text, or Count blocks of Block. The fill
// pulses gently, and holds still with reduced motion or the window unfocused.
func Skeleton(c *ui.Ctx, key any, o SkeletonOpts) ui.ID {
	fill := col(c, tSkeletonFill)
	if ph, moving := c.Decorative(skeletonPulse, 16); moving {
		fill = alpha(fill, .7+.3*(1+math.Cos(2*math.Pi*ph))/2)
	}
	gap := o.Gap
	if gap == 0 {
		gap = 8
	}
	label := o.Label
	if label == "" {
		label = "Loading"
	}
	w := o.Width
	if w == (ui.Size{}) && o.Block == (render.Point{}) {
		w = ui.Flex(1)
	}
	st := ui.Style{W: w, Gap: gap, Dir: ui.Column, Role: ui.RoleStatus, Label: label, Help: o.Help}
	if o.Block != (render.Point{}) {
		st.Dir = o.Dir
		return c.Box(key, st, func(c *ui.Ctx) {
			for i := range max(o.Count, 1) {
				c.Box(i, ui.Style{W: ui.Px(o.Block.X), H: ui.Px(o.Block.Y), Fill: fill, Radius: 8}, nil)
			}
		})
	}
	rowH := o.RowH
	if rowH == 0 {
		rowH = 12
	}
	widths := o.Widths
	if len(widths) == 0 {
		widths = []float32{1, .8, .6}
	}
	return c.Box(key, st, func(c *ui.Ctx) {
		for i := range max(o.Rows, 1) {
			frac := widths[i%len(widths)]
			c.Paint(i, ui.Flex(1), ui.Px(rowH), func(f render.Frame, r render.Rect) {
				r.Max.X = r.Min.X + r.Dx()*frac
				f.Rect(r, render.Style{Fill: fill, Radius: min(rowH/2, 4)})
			})
		}
	})
}

// Wait is several loads read together, for a button that needs them all.
type Wait []LoadState

// Waiting gathers loads a submit waits on.
func Waiting(s ...LoadState) Wait { return Wait(s) }

// Pending reports whether any hasn't got a first value yet.
func (w Wait) Pending() bool {
	for _, s := range w {
		if s.Loading() {
			return true
		}
	}
	return false
}

// Failed reports whether any failed with no value to fall back on.
func (w Wait) Failed() bool {
	for _, s := range w {
		if s.Phase == Failed {
			return true
		}
	}
	return false
}

// Reason says why a submit can't go yet, or "" when it can.
func (w Wait) Reason() string {
	for _, s := range w {
		if s.Phase == Failed && s.Err != nil {
			return "couldn't load: " + s.Err.Error()
		}
	}
	for _, s := range w {
		if s.Loading() {
			if s.Note != "" {
				return "still loading: " + s.Note
			}
			return "still loading"
		}
	}
	return ""
}
