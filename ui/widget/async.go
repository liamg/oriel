package widget

import (
	"time"

	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/ui"
)

// Policy is how a section behaves while its data loads (ui.md, "Loading"). The question that picks one: what goes
// wrong if the user acts before the data arrives?
type Policy int

const (
	Inline   Policy = iota // a page or panel: the section shows it's loading; the rest works
	Block                  // the form means nothing without it: a loading panel stands in for it
	Gate                   // fill in the rest, but the submit waits (Waiting)
	Optional               // the user may go on: the section says what's used if they do
)

type AsyncOpts struct {
	Policy   Policy
	Label    string          // what's loading: "Checking which agents are installed"
	Loading  func(c *ui.Ctx) // drawn while loading; a Busy line when nil
	Empty    string          // drawn once Ready when IsEmpty
	IsEmpty  bool
	Retry    func()        // shows Retry on a failure; nil hides it
	Fallback string        // Optional: what's used if the user goes on now
	Delay    time.Duration // nothing shows for this long; 0 is 150ms, below 0 none
	MinShow  time.Duration // once shown, loading stays this long; 0 is 300ms, below 0 none
	Width    float32       // the empty text's wrap width; 320 when 0
	Height   ui.Size       // the section's; what it holds when zero, Flex(1) for Block
	Gap      float32       // between the error line and the body; 8 when 0
	Help     string
}

const (
	asyncDelay   = 150 * time.Millisecond
	asyncMinShow = 300 * time.Millisecond
)

type asyncState struct {
	shown time.Time // when the loading view first showed, for MinShow
}

// Async draws one section by its load's state: loading (by policy), failed with Retry, empty, or body. A reload
// keeps the body, with a refresh bar along its top edge.
func Async(c *ui.Ctx, key any, s LoadState, o AsyncOpts, body func(c *ui.Ctx)) ui.ID {
	id := c.Next(key)
	st := ui.State[asyncState](c, id)
	delay, minShow := orDefault(o.Delay, asyncDelay), orDefault(o.MinShow, asyncMinShow)
	now := c.Now()

	loading := s.Loading()
	if !loading && !st.shown.IsZero() {
		if until := st.shown.Add(minShow); now.Before(until) {
			loading = true
			c.WakeAt(until)
		} else {
			st.shown = time.Time{}
		}
	}
	style := ui.Style{W: ui.Flex(1), H: o.Height, Dir: ui.Column, Gap: o.Gap}
	if style.Gap == 0 {
		style.Gap = 8
	}
	if o.Policy == Block && o.Height == (ui.Size{}) {
		style.H = ui.Flex(1)
	}
	return c.Box(key, style, func(c *ui.Ctx) {
		switch {
		case loading:
			if st.shown.IsZero() {
				if s.Phase == Idle || now.Sub(s.Since) < delay {
					if s.Phase == Pending {
						c.WakeAt(s.Since.Add(delay))
					}
					return
				}
				st.shown = now
			}
			o.loading(c, s)
		case s.Phase == Failed:
			o.failure(c, s.Err)
		default:
			if s.Err != nil {
				o.failure(c, s.Err)
			}
			refreshing := s.Stale && s.Phase == Pending
			if refreshing && now.Sub(s.Since) < delay {
				refreshing = false
				c.WakeAt(s.Since.Add(delay))
			}
			if refreshing {
				c.Box("refresh", ui.Style{W: ui.Flex(1), H: ui.Px(2), Float: &ui.Float{Fill: true}}, func(c *ui.Ctx) {
					frac := s.Progress
					if frac <= 0 {
						frac = -1
					}
					ProgressBar(c, "bar", ProgressOpts{Frac: frac, Height: 2, Label: o.Label, Help: o.Help})
				})
			}
			if o.IsEmpty {
				Wrap(c, "empty", o.Empty, orWidth(o.Width), uiText(c, 12, typeset.Normal, col(c, tAsyncEmpty)))
				return
			}
			body(c)
		}
	})
}

func (o AsyncOpts) loading(c *ui.Ctx, s LoadState) {
	busy := func(c *ui.Ctx) {
		if o.Loading != nil {
			o.Loading(c)
			return
		}
		Busy(c, "busy", s, BusyOpts{Label: orLabel(o.Label), Help: o.Help})
	}
	switch o.Policy {
	case Block:
		c.Column("block", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.Pad(24)}, busy)
	case Optional:
		c.Column("optional", ui.Style{Gap: 4}, func(c *ui.Ctx) {
			busy(c)
			if o.Fallback != "" {
				c.Text("fallback", "Will use "+o.Fallback+" if you go on now", uiText(c, 11, typeset.Normal, col(c, tBusyClock)))
			}
		})
	default:
		busy(c)
	}
}

func (o AsyncOpts) failure(c *ui.Ctx, err error) {
	msg := "Couldn't load"
	if err != nil {
		msg = err.Error()
	}
	tone := col(c, tAsyncError)
	c.Row("failed", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Role: ui.RoleStatus, Label: msg, Help: o.Help}, func(c *ui.Ctx) {
		c.Text("glyph", "⚠", uiText(c, 12, typeset.Normal, tone))
		c.Text("t", msg, uiText(c, 12, typeset.Normal, tone))
		if o.Retry != nil {
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if Button(c, "retry", "Retry", ButtonOpts{Help: o.Help}) {
				o.Retry()
			}
		}
	})
}

func orDefault(d, def time.Duration) time.Duration {
	switch {
	case d < 0:
		return 0
	case d == 0:
		return def
	}
	return d
}

func orWidth(w float32) float32 {
	if w == 0 {
		return 320
	}
	return w
}

func orLabel(s string) string {
	if s == "" {
		return "Loading"
	}
	return s
}
