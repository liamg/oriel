package app

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// QuitKeyMode is the keymap mode of the dialog shown while Oriel waits to quit.
const QuitKeyMode = "quit"

const quitHelp = "shell/quit"

// quitWait is how long a quit waits for agents mid-turn before cutting their turns short.
var quitWait = 60 * time.Second

var errQuitCancelled = errors.New("quit cancelled")

// quitDrain is Oriel quitting with sessions running: once none of its agents is mid-turn, quitWait has passed, or
// the user says now, it hands them over (stops them for the next start to resume) and the window closes.
type quitDrain struct {
	deadline time.Time
	confirm  atomic.Bool // waiting for Quit: scheduled jobs and nothing mid-turn
	now      chan struct{}
	nowOnce  sync.Once
	cancel   chan struct{} // Cancel
	cnclOnce sync.Once
	handing  atomic.Bool // the wait is over
	mu       sync.Mutex
	busy     []core.SessionState // sessions mid-turn, as last polled
	jobs     []worker.Job        // scheduled jobs, which don't run while Oriel is closed
	polled   bool

	hidden, focus bool // the dialog's; the loop's only
}

func (r *quitDrain) goNow() { r.nowOnce.Do(func() { close(r.now) }) }
func (r *quitDrain) cancelQuit() {
	if !r.handing.Load() {
		r.cnclOnce.Do(func() { close(r.cancel) })
	}
}

func newDrain() *quitDrain {
	return &quitDrain{deadline: time.Now().Add(quitWait), now: make(chan struct{}), cancel: make(chan struct{}), focus: true}
}

func (sh *shell) closeWindow() {
	if sh.closeWin != nil {
		sh.closeWin()
	}
}

// requestQuit is the window asked to close: the session host hands its agents over first, then the window closes.
// Asked again while it waits, it doesn't wait.
func (sh *shell) requestQuit() {
	if r := sh.draining.Load(); r != nil {
		r.goNow()
		r.hidden = false
		return
	}
	if sh.cfg.Worker == nil {
		sh.closeWindow()
		return
	}
	sh.drain(newDrain())
}

func (sh *shell) drain(r *quitDrain) {
	if !sh.draining.CompareAndSwap(nil, r) {
		return
	}
	sh.poke()
	go func() {
		err := sh.handOver(r)
		sh.draining.Store(nil)
		if errors.Is(err, errQuitCancelled) {
			sh.relaunch.Store(nil)
		} else {
			sh.closeWindow()
		}
		sh.poke()
	}()
}

// poll reads which sessions are mid-turn, and which jobs are scheduled.
func (r *quitDrain) poll(w *worker.Worker, st core.State) []core.SessionState {
	busy := slices.DeleteFunc(slices.Clone(st.Sessions), func(se core.SessionState) bool { return !se.Agent.Working })
	jobs := slices.DeleteFunc(w.Jobs(), func(j worker.Job) bool { return j.Schedule.Every == "" })
	r.mu.Lock()
	r.busy, r.jobs, r.polled = busy, jobs, true
	r.mu.Unlock()
	return busy
}

func (r *quitDrain) shown() (busy []core.SessionState, jobs []worker.Job, polled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy, r.jobs, r.polled
}

func (sh *shell) handOver(r *quitDrain) error {
	w := sh.cfg.Worker
	wait := time.NewTimer(time.Until(r.deadline))
	defer wait.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	if busy := r.poll(w, sh.cfg.Store.State()); len(busy) == 0 && len(r.jobs) > 0 {
		r.confirm.Store(true)
		sh.poke()
		select {
		case <-r.now:
		case <-r.cancel:
			return errQuitCancelled
		}
	}
waiting:
	for len(r.poll(w, sh.cfg.Store.State())) > 0 {
		sh.poke()
		select {
		case <-r.now:
			break waiting
		case <-r.cancel:
			return errQuitCancelled
		case <-wait.C:
			break waiting
		case <-tick.C:
		}
	}
	r.handing.Store(true)
	sh.poke()
	w.HandOver()
	return nil
}

// whenSessions runs fn on the loop with the sessions plugin, if it's active.
func (sh *shell) whenSessions(fn func(x *sessions)) {
	sh.todoMu.Lock()
	sh.todo = append(sh.todo, func() {
		if sh.sessions != nil {
			fn(sh.sessions)
		}
	})
	sh.todoMu.Unlock()
	sh.poke()
}

// drainView is the dialog while a quit waits or hands over, unless it's hidden. A quit with nothing to wait for
// shows none.
func (x *sessions) drainView(c *ui.Ctx) {
	r := x.sh.draining.Load()
	if r == nil || r.hidden {
		return
	}
	busy, jobs, polled := r.shown()
	if !polled || len(busy) == 0 && len(jobs) == 0 {
		return
	}
	hide := r.cancelQuit
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	closed := widget.Scrim(c, "quit", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: quitHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if r.focus {
			c.Focus(box)
			r.focus = false
		}
		w := min(520, size.X-32)
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: QuitKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "Quitting Oriel", Help: quitHelp}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", "QUITTING ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: quitHelp}) {
					hide()
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 18, B: 20}, Gap: 12}, func(c *ui.Ctx) {
				x.drainBody(c, r, busy, jobs, w-48)
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 12, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "dismiss", "Cancel", widget.ButtonOpts{Key: "esc", Disabled: r.handing.Load(), Help: quitHelp}) {
					hide()
				}
				now := "Quit now"
				if r.confirm.Load() {
					now = "Quit"
				}
				if widget.Button(c, "now", now, widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: r.handing.Load(), Help: quitHelp}) {
					r.goNow()
				}
			})
		})
		for _, k := range c.Interaction(box).Keys {
			if notation(k) == "<Esc>" {
				hide()
			}
		}
	})
	if closed {
		hide()
	}
}

func (x *sessions) drainBody(c *ui.Ctx, r *quitDrain, busy []core.SessionState, jobs []worker.Job, width float32) {
	th := c.Theme()
	if r.handing.Load() {
		widget.Wrap(c, "t", "Stopping the agents. They come back when Oriel opens again.", width, uiFont(12.5, th.C(theme.Fg)))
		return
	}
	if len(busy) > 0 {
		remaining := max(r.deadline.Sub(c.Now()), 0)
		left := (remaining + time.Second - 1).Truncate(time.Second)
		if remaining > 0 {
			c.WakeAt(c.Now().Add(remaining - left + time.Second)) // the next second's count
		}
		lead, then := "Oriel stops the agents running your sessions as it quits.",
			fmt.Sprintf("It quits when these turns end, or in %s; their agents come back when Oriel opens again.", left)
		widget.Wrap(c, "t", lead, width, uiFont(12.5, th.C(theme.Fg)))
		widget.Wrap(c, "when", then, width, uiFont(12, th.C(theme.FgMuted)))
		c.Column("busy", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			for _, se := range busy {
				c.Row(string(se.ID), ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("name", cmpOr(se.Name, string(se.ID)), monoFont(11.5, th.C(theme.Fg)))
					c.Text("state", "mid-turn", monoFont(10.5, th.C(theme.FgFaint)))
				})
			}
		})
	}
	if len(jobs) > 0 {
		widget.Wrap(c, "jobs", "Scheduled jobs don't run while Oriel is closed: one that comes due runs once when it opens again.",
			width, uiFont(12, th.C(theme.Warn)))
		c.Column("scheduled", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 2}}, func(c *ui.Ctx) {
			for _, j := range jobs {
				c.Row(j.Name, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("name", cmpOr(j.Title, j.Name), monoFont(11.5, th.C(theme.Fg)))
					c.Text("when", j.Schedule.String(), monoFont(10.5, th.C(theme.FgFaint)))
				})
			}
		})
	}
}
