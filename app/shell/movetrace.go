package shell

import (
	"log/slog"
	"time"
)

// moveTrace is where a move's time went, from the cover going up to the first frame laid out on the new
// directory: logged once it's drawn, so a slow switch says which part was slow.
type moveTrace struct {
	log                         *slog.Logger
	dir                         string
	asked, started, left, fired time.Time
	arrived, laidOut            time.Time
	held                        bool // it waited on a hold (Window.Hold)
	cancel                      func()
}

// traceMove starts the trace of the move to to, on the loop as it starts.
func (sh *shell) traceMove(log *slog.Logger, to reopenTo) *moveTrace {
	if sh.moving != nil && sh.moving.cancel != nil {
		sh.moving.cancel()
	}
	m := &moveTrace{log: log, dir: to.dir, asked: to.asked, started: time.Now()}
	if m.asked.IsZero() {
		m.asked = m.started
	}
	sh.moving = m
	return m
}

// laid notes the new directory laid out, and logs the trace once the frame after is drawn.
func (sh *shell) laid(m *moveTrace) {
	if m == nil || m.log == nil {
		return
	}
	m.laidOut = time.Now()
	m.cancel = sh.framed.On(func() {
		m.cancel()
		if sh.moving == m {
			sh.moving = nil
		}
		drawn := time.Now()
		m.log.Info("the window moved", "dir", m.dir, "total", drawn.Sub(m.asked).Round(time.Millisecond),
			"loop", m.started.Sub(m.asked).Round(time.Millisecond), "leave", m.left.Sub(m.started).Round(time.Millisecond),
			"hooks", m.fired.Sub(m.left).Round(time.Millisecond), "holds", m.arrived.Sub(m.fired).Round(time.Millisecond),
			"held", m.held, "layout", m.laidOut.Sub(m.arrived).Round(time.Millisecond),
			"frame", drawn.Sub(m.laidOut).Round(time.Millisecond))
	})
}
