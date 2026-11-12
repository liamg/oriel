package widget

import (
	"math"
	"time"

	"github.com/liamg/oriel/ui"
)

type PulseOpts struct {
	Times  int           // pulses before it settles: 3 when 0
	Period time.Duration // one pulse: 1.2s when 0
	Rest   float32       // the strength it settles at, 0..1: 0.45 when 0
}

const (
	pulseTimes  = 3
	pulsePeriod = 1200 * time.Millisecond
	pulseRest   = 0.45
	pulseSteps  = 12
)

type pulseState struct {
	call  any
	since time.Time
}

// Pulse is how strongly something calling for attention (a tab whose session asks for you) shows now, from Rest up
// to 1: Times pulses from the first frame it's asked with call, keyed by key under this node, then Rest for as long
// as it's asked. A new call pulses again. It holds at Rest under reduced motion, while the window is unfocused or
// hidden, and while what's building it was out of sight (Ctx.Decorative).
func Pulse(c *ui.Ctx, key, call any, o PulseOpts) float32 {
	times, period, rest := o.Times, o.Period, o.Rest
	if times <= 0 {
		times = pulseTimes
	}
	if period <= 0 {
		period = pulsePeriod
	}
	if rest <= 0 {
		rest = pulseRest
	}
	st := ui.State[pulseState](c, c.Next(struct{ pulse any }{key}))
	now := c.Now()
	if st.since.IsZero() || st.call != call {
		st.call, st.since = call, now
	}
	end := st.since.Add(time.Duration(times) * period)
	if !now.Before(end) {
		return rest
	}
	if _, moving := c.Decorative(period, pulseSteps); !moving {
		return rest
	}
	c.WakeAt(end)
	phase := float64(now.Sub(st.since)%period) / float64(period)
	return rest + (1-rest)*float32(0.5-0.5*math.Cos(2*math.Pi*phase))
}
