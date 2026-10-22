package kit

import (
	"sync"
	"time"

	"github.com/liamg/oriel/ui"
)

// PacedRedraw asks for frames from a goroutine that changes what's shown often, as a terminal's output does: at
// most one an Interval (ui.FrameInterval when 0), and one at the interval's end for changes inside it.
type PacedRedraw struct {
	Interval time.Duration
	Redraw   func()

	mu    sync.Mutex
	last  time.Time
	timer *time.Timer // set while the trailing frame is waiting
}

// Ask notes a change to draw; safe from any goroutine.
func (p *PacedRedraw) Ask() {
	p.mu.Lock()
	if p.timer != nil {
		p.mu.Unlock()
		return
	}
	iv := p.Interval
	if iv == 0 {
		iv = ui.FrameInterval
	}
	if wait := iv - time.Since(p.last); wait > 0 {
		p.timer = time.AfterFunc(wait, p.fire)
		p.mu.Unlock()
		return
	}
	p.last = time.Now()
	p.mu.Unlock()
	p.Redraw()
}

func (p *PacedRedraw) fire() {
	p.mu.Lock()
	p.timer, p.last = nil, time.Now()
	p.mu.Unlock()
	p.Redraw()
}

// Stop drops a trailing frame still waiting.
func (p *PacedRedraw) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.timer != nil {
		p.timer.Stop()
		p.timer = nil
	}
}
