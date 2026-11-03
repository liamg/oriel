package widget

import (
	"fmt"
	"time"

	"github.com/liamg/oriel/ui"
)

type AgoOpts struct {
	Text ui.TextStyle
	// Long says "5m ago" and "just now" rather than "5m" and "now".
	Long bool
	// Prefix and Suffix go either side of it, in the same text: "used " for "used 2d ago".
	Prefix, Suffix string
	Now            time.Time // the clock it counts from, for a page with its own; c.Now() when zero
}

// Ago is how long ago at was, shortly ("5m"), kept up to date; the pointer resting on it shows the full time.
func Ago(c *ui.Ctx, key any, at time.Time, o AgoOpts) {
	if at.IsZero() {
		return
	}
	now := o.Now
	if now.IsZero() {
		now = c.Now()
	}
	text := AgoLabel(now, at)
	if o.Long {
		text = AgoLong(now, at)
	}
	c.WakeAt(agoChanges(now, at))
	c.Box(key, ui.Style{Tooltip: Timestamp(at)}, func(c *ui.Ctx) { c.Text("t", o.Prefix+text+o.Suffix, o.Text) })
}

// AgoLabel is how long before now t was, shortly: now, 10m, 2h, 3d, 1w.
func AgoLabel(now, t time.Time) string {
	d := now.Sub(t)
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
	return fmt.Sprintf("%dw", int(d.Hours()/24/7))
}

// AgoLong is AgoLabel as a phrase: "10m ago", "just now".
func AgoLong(now, t time.Time) string {
	if a := AgoLabel(now, t); a != "now" {
		return a + " ago"
	}
	return "just now"
}

// Timestamp is t in full, in local time: "Mon 2 Nov 2026, 14:03:12".
func Timestamp(t time.Time) string { return t.Local().Format("Mon 2 Jan 2006, 15:04:05") }

// agoChanges is when AgoLabel(now, t) next changes.
func agoChanges(now, t time.Time) time.Time {
	d := now.Sub(t)
	unit := time.Minute
	switch {
	case d < 0:
		return t
	case d >= 7*24*time.Hour:
		unit = 7 * 24 * time.Hour
	case d >= 24*time.Hour:
		unit = 24 * time.Hour
	case d >= time.Hour:
		unit = time.Hour
	}
	return t.Add((d/unit + 1) * unit)
}

// AgoPart is a piece of an AgoLine: Text, or, when At is set, a time drawn as Ago with Text before it. A time part
// with no time is plain Text, so leave it out instead.
type AgoPart struct {
	Text   string
	At     time.Time
	Long   bool
	Suffix string
}

type AgoLineOpts struct {
	Text ui.TextStyle
	Now  time.Time // as AgoOpts.Now
}

// AgoLine draws parts in a row joined by " · ", each time an Ago with its own tooltip; empty parts are skipped.
func AgoLine(c *ui.Ctx, key any, o AgoLineOpts, parts ...AgoPart) {
	c.Row(key, ui.Style{}, func(c *ui.Ctx) {
		lead := ""
		for i, p := range parts {
			if p.At.IsZero() && p.Text == "" {
				continue
			}
			if p.At.IsZero() {
				c.Text(i, lead+p.Text, o.Text)
			} else {
				Ago(c, i, p.At, AgoOpts{Text: o.Text, Long: p.Long, Prefix: lead + p.Text, Suffix: p.Suffix, Now: o.Now})
			}
			lead = " · "
		}
	})
}
