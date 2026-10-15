package app

import (
	"cmp"
	"context"
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

// WorkerDrainKeyMode is the keymap mode of the dialog shown while this machine's worker drains.
const WorkerDrainKeyMode = "workerdrain"

const (
	workerRestartHelp = "agent/worker#restart"
	workerQuitHelp    = "agent/worker#quit"
)

// workerDrainWait is how long a drain waits for agents mid-turn before cutting their turns short.
var workerDrainWait = 60 * time.Second

var errQuitCancelled = errors.New("quit cancelled")

// workerDrain is this machine's worker handing over (sessions.md → As built): once none of its agents is mid-turn,
// workerDrainWait has passed, or the user says now, it stops them for the next worker to resume, and stops. A
// restart for another build then starts this build's worker; a quit closes the window.
type workerDrain struct {
	old      worker.Client
	worker   core.WorkerID
	deadline time.Time
	quit     atomic.Bool // Oriel quits after
	restart  bool        // for another build: a quit asked for meanwhile can be called off
	asked    bool        // a restart the user asked for, not a rebuild
	confirm  atomic.Bool // a quit waiting for Quit: scheduled jobs and nothing mid-turn
	now      chan struct{}
	nowOnce  sync.Once
	cancel   chan struct{} // a quit's Cancel
	cnclOnce sync.Once
	gone     chan struct{} // the old worker's events have stopped
	goneOnce sync.Once
	handing  atomic.Bool // the wait is over
	mu       sync.Mutex
	busy     []core.SessionState // its sessions mid-turn, as it last said
	jobs     []worker.Job        // a quit's scheduled jobs, which don't run while Oriel is closed
	polled   bool

	hidden, focus bool // the dialog's; the loop's only
}

func (r *workerDrain) goNow() { r.nowOnce.Do(func() { close(r.now) }) }
func (r *workerDrain) lost()  { r.goneOnce.Do(func() { close(r.gone) }) }
func (r *workerDrain) cancelQuit() {
	if !r.handing.Load() {
		r.cnclOnce.Do(func() { close(r.cancel) })
	}
}

func newDrain(old worker.Client, id core.WorkerID) *workerDrain {
	return &workerDrain{old: old, worker: id, deadline: time.Now().Add(workerDrainWait), now: make(chan struct{}),
		cancel: make(chan struct{}), gone: make(chan struct{}), focus: true}
}

// restartWorker hands old, from another build, over to a worker of this build. One drain runs at a time.
func (sh *shell) restartWorker(old worker.Client, id core.WorkerID) {
	r := newDrain(old, id)
	r.restart = true
	sh.drain(r)
}

func (sh *shell) closeWindow() {
	if sh.closeWin != nil {
		sh.closeWin()
	}
}

// requestQuit is the window asked to close: this machine's worker drains first, then the window closes. Asked
// again while it waits, it doesn't wait.
func (sh *shell) requestQuit() {
	if r := sh.draining.Load(); r != nil {
		if r.quit.Swap(true) {
			r.goNow()
		}
		r.hidden = false
		return
	}
	var local worker.Client
	ok := false
	if x := sh.sessions; x != nil && sh.cfg.LocalWorker != nil {
		local, ok = x.byWorker()[core.LocalWorker]
	}
	if !ok {
		sh.closeWindow()
		return
	}
	r := newDrain(local, core.LocalWorker)
	r.quit.Store(true)
	sh.drain(r)
}

func (sh *shell) drain(r *workerDrain) {
	if !sh.draining.CompareAndSwap(nil, r) {
		return
	}
	sh.poke()
	go func() {
		err := sh.handOver(r)
		sh.draining.Store(nil)
		switch {
		case errors.Is(err, errQuitCancelled):
		case r.quit.Load():
			sh.closeWindow()
		case err != nil:
			sh.whenSessions(func(x *sessions) {
				x.h.Notify(core.Notice{ID: "worker.restart", Severity: core.SevError, Source: "worker:" + string(r.worker), Time: time.Now(),
					Title: "Worker restart", Text: err.Error()})
			})
		}
		sh.poke()
	}()
}

// poll asks the old worker which of its sessions are mid-turn, and, for a quit, which jobs are scheduled.
func (r *workerDrain) poll(ctx context.Context) []core.SessionState {
	all, err := r.old.Sessions(ctx)
	if err != nil {
		return nil
	}
	busy := slices.DeleteFunc(all, func(se core.SessionState) bool { return !worker.Busy(se) })
	var jobs []worker.Job
	if r.quit.Load() {
		all, _ := r.old.Jobs(ctx)
		jobs = slices.DeleteFunc(all, func(j worker.Job) bool { return j.Schedule.Every == "" })
	}
	r.mu.Lock()
	r.busy, r.jobs, r.polled = busy, jobs, true
	r.mu.Unlock()
	return busy
}

func (r *workerDrain) shown() (busy []core.SessionState, jobs []worker.Job, polled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy, r.jobs, r.polled
}

func (sh *shell) handOver(r *workerDrain) error {
	ctx, cancel := context.WithTimeout(context.Background(), workerDrainWait+30*time.Second)
	defer cancel()
	wait := time.NewTimer(time.Until(r.deadline))
	defer wait.Stop()
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	if busy := r.poll(ctx); len(busy) == 0 && r.quit.Load() && len(r.jobs) > 0 {
		r.confirm.Store(true)
		sh.poke()
		select {
		case <-r.now:
		case <-r.cancel:
			return errQuitCancelled
		}
	}
waiting:
	for len(r.poll(ctx)) > 0 {
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
	if err := r.old.HandOver(ctx); err != nil {
		return fmt.Errorf("the worker didn't hand over: %w", err)
	}
	select {
	case <-r.gone:
	case <-ctx.Done():
		return fmt.Errorf("the worker didn't stop")
	}
	if r.quit.Load() {
		return nil
	}
	c, err := sh.cfg.LocalWorker(ctx)
	if err != nil {
		return fmt.Errorf("starting this build's worker: %w", err)
	}
	have, err := c.Sessions(ctx)
	if err != nil {
		return err
	}
	for _, se := range sh.cfg.Store.State().Sessions {
		if cmp.Or(se.Worker, core.LocalWorker) == r.worker && !slices.ContainsFunc(have, func(n core.SessionState) bool { return n.ID == se.ID }) {
			_ = sh.cfg.Store.Apply(core.SessionClosed{ID: se.ID})
		}
	}
	sh.whenSessions(func(x *sessions) {
		for i := range sh.cfg.Workers {
			if sh.cfg.Workers[i].Conn == r.old.Conn {
				sh.cfg.Workers[i] = c
			}
		}
		x.serve(x.ctx, x.h, c, "")
	})
	return nil
}

// whenSessions runs fn on the loop with the sessions extension, if it's active.
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

// drainView is the dialog while a drain waits or hands over, unless it's hidden. A quit with nothing to wait for
// shows none.
func (x *sessions) drainView(c *ui.Ctx) {
	r := x.sh.draining.Load()
	if r == nil || r.hidden {
		return
	}
	busy, jobs, polled := r.shown()
	quit := r.quit.Load()
	if quit && (!polled || len(busy) == 0 && len(jobs) == 0) {
		return
	}
	title, label, help, dismiss := "RESTARTING THE WORKER", "Restarting the worker", workerRestartHelp, "Hide"
	hide := func() { r.hidden = true }
	if quit {
		title, label, help, dismiss = "QUITTING ORIEL", "Quitting Oriel", workerQuitHelp, "Cancel"
		hide = func() {
			if r.restart {
				r.quit.Store(false)
				return
			}
			r.cancelQuit()
		}
	}
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	closed := widget.Scrim(c, "workerdrain", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: help}, func(c *ui.Ctx) {
		box := c.Next("box")
		if r.focus {
			c.Focus(box)
			r.focus = false
		}
		w := min(520, size.X-32)
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(tOmniBg), Stroke: th.C(tOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: WorkerDrainKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: label, Help: help}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: help}) {
					hide()
				}
			})
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 18, B: 20}, Gap: 12}, func(c *ui.Ctx) {
				x.drainBody(c, r, busy, jobs, w-48)
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 12, Cross: ui.Center, Main: ui.End}, func(c *ui.Ctx) {
				if widget.Button(c, "dismiss", dismiss, widget.ButtonOpts{Key: "esc", Disabled: quit && r.handing.Load(), Help: help}) {
					hide()
				}
				now := "Restart now"
				switch {
				case quit && r.confirm.Load():
					now = "Quit"
				case quit:
					now = "Quit now"
				}
				if widget.Button(c, "now", now, widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: r.handing.Load(), Help: help}) {
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

func (x *sessions) drainBody(c *ui.Ctx, r *workerDrain, busy []core.SessionState, jobs []worker.Job, width float32) {
	th := c.Theme()
	quit := r.quit.Load()
	if r.handing.Load() {
		text := "Starting this build's worker. The agents come back on it."
		if r.asked {
			text = "Starting the worker again. The agents come back on it."
		}
		if quit {
			text = "Stopping the worker. Its agents come back when Oriel opens again."
		}
		widget.Wrap(c, "t", text, width, uiFont(12.5, th.C(theme.Fg)))
		return
	}
	if len(busy) > 0 {
		remaining := max(r.deadline.Sub(c.Now()), 0)
		left := (remaining + time.Second - 1).Truncate(time.Second)
		if remaining > 0 {
			c.WakeAt(c.Now().Add(remaining - left + time.Second)) // the next second's count
		}
		lead, then := "Oriel was rebuilt; the worker running your sessions is from the earlier build.",
			fmt.Sprintf("It restarts when these turns end, or in %s, and their agents come back on the new one.", left)
		if r.asked {
			lead = "The worker running your sessions restarts."
		}
		if quit {
			lead, then = "Oriel stops the worker running your sessions as it quits.",
				fmt.Sprintf("It quits when these turns end, or in %s; their agents come back when Oriel opens again.", left)
		}
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
	if quit && len(jobs) > 0 {
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
