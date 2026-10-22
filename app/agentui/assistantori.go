package agentui

import (
	"math"
	"time"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// Ori is the assistant's face, drawn from the logo in a 16-unit square: the logo's O as its head, and two of its
// cursor bars as eyes. Its eyes blink now and then, as a caret does, glance side to side while it works, and a dot
// lights by its head while something it said is unread.
var (
	tOriRing  = theme.Declare("assistant.ori.ring", "logo.ring")
	tOriFace  = theme.Declare("assistant.ori.face", "bg.base")
	tOriEye   = theme.Declare("assistant.ori.eye", "logo.cursor")
	tOriSmile = theme.Declare("assistant.ori.smile", "logo.ring")
	tOriLit   = theme.Declare("assistant.ori.lit", "logo.cursor")
)

const (
	oriBlinkCycle  = 16 * time.Second
	OriBlinkFor    = 140 * time.Millisecond
	oriGlanceEvery = 1400 * time.Millisecond
	oriGlanceSteps = 21 // 15 a second
)

// oriBlinks are when it blinks in each oriBlinkCycle: unevenly, and now and then twice.
var oriBlinks = []time.Duration{0, 5200 * time.Millisecond, 5550 * time.Millisecond, 11 * time.Second}

// OriBlink says whether its eyes are shut at now, and when that next changes.
func OriBlink(now time.Time) (bool, time.Time) {
	into := time.Duration(now.UnixNano() % int64(oriBlinkCycle))
	for _, at := range oriBlinks {
		if into < at {
			return false, now.Add(at - into)
		}
		if into < at+OriBlinkFor {
			return true, now.Add(at + OriBlinkFor - into)
		}
	}
	return false, now.Add(oriBlinkCycle - into)
}

type oriMood struct {
	busy, unread bool
	still        bool // a face in the log, that doesn't blink
}

// ori draws Ori as a size-pixel square.
func ori(c *ui.Ctx, key any, size float32, m oriMood) {
	th := c.Theme()
	ring, face, eye, smile, lit := th.C(tOriRing), th.C(tOriFace), th.C(tOriEye), th.C(tOriSmile), th.C(tOriLit)
	now := c.Now()
	glance, blink := float32(0), false
	switch {
	case m.still:
	case m.busy:
		phase, _ := c.Decorative(oriGlanceEvery, oriGlanceSteps)
		glance = float32(math.Sin(phase * 2 * math.Pi))
	case !c.Still() && c.WindowFocused() && c.WindowVisible():
		var next time.Time
		blink, next = OriBlink(now)
		c.WakeAt(next)
	}
	c.Paint(key, ui.Px(size), ui.Px(size), func(f render.Frame, r render.Rect) {
		u := r.Dx() / 16
		pt := func(x, y float32) render.Point { return render.Point{X: r.Min.X + x*u, Y: r.Min.Y + y*u} }
		box := func(x, y, w, h float32) render.Rect { return render.XYWH(r.Min.X+x*u, r.Min.Y+y*u, w*u, h*u) }

		f.Path(render.Ellipse(box(1.8, 1.8, 12.4, 12.4)), render.Style{Fill: face})
		f.Path(render.Ellipse(box(2.5, 2.5, 11, 11)), render.Style{Stroke: ring, Width: 1.4 * u})

		h := float32(3.6)
		if blink {
			h = .5
		}
		for _, x := range []float32{5.4, 9.3} {
			e := box(x+glance*1.1, 7.7-h/2, 1.3, h)
			f.Effect(render.Glow, e, 2*u, theme.WithAlpha(eye, .45))
			var p render.Path // a path, not a rect, so the bars aren't snapped to pixels, as the logo's isn't
			p.MoveTo(e.Min).LineTo(render.Point{X: e.Max.X, Y: e.Min.Y}).LineTo(e.Max).LineTo(render.Point{X: e.Min.X, Y: e.Max.Y}).Close()
			f.Path(&p, render.Style{Fill: eye})
		}

		var mouth render.Path
		mouth.MoveTo(pt(7.1, 11.1)).QuadTo(pt(8, 11.8), pt(8.9, 11.1))
		f.Path(&mouth, render.Style{Stroke: smile, Width: max(.7*u, .8)})

		if m.unread {
			dot := box(11.9, 1.5, 2.6, 2.6)
			f.Effect(render.GlowDisc, dot, 2*u, theme.WithAlpha(lit, .7))
			f.Path(render.Ellipse(dot), render.Style{Fill: lit})
		}
	})
}
