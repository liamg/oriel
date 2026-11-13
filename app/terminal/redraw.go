package terminal

import (
	"sync"
	"time"

	"github.com/liamg/oriel/internal/clock"
	"github.com/liamg/oriel/ui"
)

// pacedRedraw asks for frames from a goroutine that changes what's shown often, as a terminal's output does: at
// most one an Interval (ui.FrameInterval when 0), and one at the interval's end for changes inside it.
type pacedRedraw struct {
	Interval time.Duration
	Redraw   func()
	Clock    clock.Clock // the system's when nil

	mu    sync.Mutex
	last  time.Time
	timer clock.Timer // set while the trailing frame is waiting
}

// Ask notes a change to draw; safe from any goroutine.
func (p *pacedRedraw) Ask() {
	p.mu.Lock()
	if p.timer != nil {
		p.mu.Unlock()
		return
	}
	iv := p.Interval
	if iv == 0 {
		iv = ui.FrameInterval
	}
	c := clock.Or(p.Clock)
	if wait := iv - c.Now().Sub(p.last); wait > 0 {
		p.timer = c.AfterFunc(wait, p.fire)
		p.mu.Unlock()
		return
	}
	p.last = c.Now()
	p.mu.Unlock()
	p.Redraw()
}

func (p *pacedRedraw) fire() {
	p.mu.Lock()
	p.timer, p.last = nil, clock.Or(p.Clock).Now()
	p.mu.Unlock()
	p.Redraw()
}

// Stop drops a trailing frame still waiting.
func (p *pacedRedraw) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}
