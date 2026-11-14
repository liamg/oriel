package shell

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// QuitKeyMode is the keymap mode of the dialog shown while Oriel waits to quit.
const QuitKeyMode = "quit"

const quitHelp = "shell/quit"

// quitWait is how long a quit waits for agents mid-turn before cutting their turns short.
const quitWait = 60 * time.Second

var errQuitCancelled = errors.New("quit cancelled")

// quitDrain is Oriel quitting with sessions running: once none of its agents is mid-turn, quitWait has passed, or
// the user says now, it hands them over (stops them for the next start to resume) and the window closes.
type quitDrain struct {
	deadline time.Time
	confirm  atomic.Bool // waiting for Quit: scheduled jobs and nothing mid-turn
	ask      bool        // asks about scheduled jobs: someone's there to answer
	now      chan struct{}
	nowOnce  sync.Once
	cancel   chan struct{} // Cancel
	cnclOnce sync.Once
	handing  atomic.Bool // the wait is over
	mu       sync.Mutex
	busy     []core.SessionState // sessions mid-turn, as last polled
	jobs     []host.Job          // scheduled jobs, which don't run while Oriel is closed
	polled   bool
	stopping []core.SessionID // the sessions whose agents were running when the wait ended, which the hand-over stops

	hidden, focus bool // the dialog's; the loop's only
}

func (r *quitDrain) goNow() { r.nowOnce.Do(func() { close(r.now) }) }
func (r *quitDrain) cancelQuit() {
	if !r.handing.Load() {
		r.cnclOnce.Do(func() { close(r.cancel) })
	}
}

// newDrain is a quit asked at now, which waits until quitWait after it.
func newDrain(ask bool, now time.Time) *quitDrain {
	return &quitDrain{deadline: now.Add(quitWait), now: make(chan struct{}), cancel: make(chan struct{}), focus: true, ask: ask}
}

func (sh *shell) closeWindow() {
	if sh.closeWin != nil {
		sh.closeWin()
	}
}

// quit is the window asked to close: the session host hands its agents over first, then the window closes.
// Asked again while it waits, it doesn't wait; ask is whether it asks about scheduled jobs.
func (sh *shell) quit(ask bool) {
	if r := sh.draining.Load(); r != nil {
		r.goNow()
		r.hidden = false
		return
	}
	if !sh.holdsClose && !kit.HasSessions(sh.cfg.Registry) {
		sh.closeWindow()
		return
	}
	sh.drain(newDrain(ask, sh.now()))
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
func (r *quitDrain) poll(w host.JobCalls, st core.State) []core.SessionState {
	busy := slices.DeleteFunc(slices.Clone(st.Sessions), func(se core.SessionState) bool { return !se.Agent.Working })
	jobs := slices.DeleteFunc(w.Jobs(), func(j host.Job) bool { return j.Schedule.Every == "" })
	r.mu.Lock()
	r.busy, r.jobs, r.polled = busy, jobs, true
	r.mu.Unlock()
	return busy
}

func (r *quitDrain) shown() (busy []core.SessionState, jobs []host.Job, polled bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.busy, r.jobs, r.polled
}

func (r *quitDrain) stoppingNow() []core.SessionID {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stopping
}

func (sh *shell) handOver(r *quitDrain) error {
	expired := make(chan struct{})
	wait := windowClock{sh}.AfterFunc(r.deadline.Sub(sh.now()), func() { close(expired) })
	defer wait.Stop()
	tick := kit.NewTicker(sh.polls, 200*time.Millisecond)
	defer tick.Stop()
	// A quit as the window opens waits for the sessions plugin to bring the host's calls.
	w, err := kit.SessionCalls(sh.cfg.Registry)
	for err != nil && sh.holdsClose {
		select {
		case <-r.now:
			return nil
		case <-r.cancel:
			return errQuitCancelled
		case <-expired:
			return nil
		case <-tick.C:
		}
		w, err = kit.SessionCalls(sh.cfg.Registry)
	}
	if err != nil {
		return nil
	}
	jobs, err := kit.JobCalls(sh.cfg.Registry)
	if err != nil {
		return nil
	}
	if busy := r.poll(jobs, sh.store.State()); len(busy) == 0 && len(r.jobs) > 0 && r.ask {
		r.confirm.Store(true)
		sh.poke()
		select {
		case <-r.now:
		case <-r.cancel:
			return errQuitCancelled
		}
	}
waiting:
	for len(r.poll(jobs, sh.store.State())) > 0 {
		sh.poke()
		select {
		case <-r.now:
			break waiting
		case <-r.cancel:
			return errQuitCancelled
		case <-expired:
			break waiting
		case <-tick.C:
		}
	}
	var live []core.SessionID
	for _, se := range sh.store.State().Sessions {
		if se.Agent.Live() {
			live = append(live, se.ID)
		}
	}
	r.mu.Lock()
	r.stopping = live
	r.mu.Unlock()
	r.handing.Store(true)
	sh.poke()
	done := make(chan struct{})
	go func() {
		// The dialog shows each agent as it stops.
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				sh.poke()
			}
		}
	}()
	w.HandOver()
	close(done)
	return nil
}

// relaunchAfter starts Oriel again as Run returns, when an update's restart asked for it.
func (sh *shell) relaunchAfter(err *error) {
	f := sh.relaunch.Load()
	if f == nil || *f == nil || *err != nil {
		return
	}
	*err = (*f)()
}

// quitDialog is the dialog while Oriel waits to quit: while it's up it keeps the keys, so nothing else opens over it
// or runs.
type quitDialog struct {
	kit.Disposer
	draining *atomic.Pointer[quitDrain] // the window's quit, while one waits
	h        plugin.Host
}

func (x *quitDialog) Manifest() plugin.Manifest { return plugin.Manifest{ID: "quit"} }

func (x *quitDialog) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(h.Keymap(registry.Keymap{Mode: QuitKeyMode, Keys: "<D-CR>", Command: "quit.now", Desc: "Don't wait for the turns"}))
	x.Add(h.Contribute(registry.ExclusiveKeys{Mode: QuitKeyMode, Source: "builtin:quit"}))
	x.Add(h.View(kit.OverlayPrefix+"quit", uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "quit", Fn: x.drainView}
	})))
	x.Add(h.Command(registry.Command{ID: "quit", Title: "Quit Oriel, handing its agents over",
		Run: func(registry.Ctx, registry.Args) error {
			if q, ok := kit.QuitOf(h); ok {
				q.Quit()
			}
			return nil
		}}))
	x.Add(h.Command(registry.Command{ID: "quit.now", Title: "Quit now, not waiting for the agents' turns",
		Available: func(registry.Args) bool { return x.draining.Load() != nil },
		Run: func(registry.Ctx, registry.Args) error {
			if r := x.draining.Load(); r != nil {
				r.goNow()
			}
			return nil
		}}))
	return nil
}

// drainView is the dialog while a quit waits or hands over, unless it's hidden. A quit with nothing to wait for
// shows none.
func (x *quitDialog) drainView(c *ui.Ctx) {
	r := x.draining.Load()
	if r == nil || r.hidden {
		return
	}
	busy, jobs, polled := r.shown()
	handing := r.handing.Load()
	if handing && len(r.stoppingNow()) == 0 || !handing && (!polled || len(busy) == 0 && len(jobs) == 0) {
		return
	}
	hide := r.cancelQuit
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	closed := widget.Scrim(c, "quit", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: quitHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if r.focus {
			c.RequestFocus(box)
			r.focus = false
		}
		w := min(520, size.X-32)
		c.Column("box", ui.Style{W: ui.Px(w), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: QuitKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "Quitting Oriel", Help: quitHelp}, func(c *ui.Ctx) {
			if handing {
				c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
					widget.Spinner(c, "spin", widget.SpinnerOpts{Name: "stopping the agents", Help: quitHelp})
					c.Text("title", "SHUTTING DOWN", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
						Color: th.C(theme.Fg)})
				})
				c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 18, B: 20}, Gap: 12}, func(c *ui.Ctx) {
					x.stoppingBody(c, r, w-48)
				})
				return
			}
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
				title := "QUITTING ORIEL"
				if handing {
					title = "SHUTTING DOWN"
				}
				c.Text("title", title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
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
			if kit.Notation(k) == "<Esc>" {
				hide()
			}
		}
	})
	if closed {
		hide()
	}
}

// stoppingBody is the shutdown: each agent the hand-over stops, until it has.
func (x *quitDialog) stoppingBody(c *ui.Ctx, r *quitDrain, width float32) {
	th := c.Theme()
	ids := r.stoppingNow()
	st := x.h.State()
	left := 0
	for _, id := range ids {
		if se, ok := st.Session(id); ok && se.Snapshot().Agent.Live() {
			left++
		}
	}
	widget.Wrap(c, "t", fmt.Sprintf("Stopping %s. They come back when Oriel opens again.", kit.Counted(left, "agent")), width,
		kit.UIFont(12.5, th.C(theme.Fg)))
	c.Column("agents", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
		for _, id := range ids {
			var se core.SessionState
			if v, ok := st.Session(id); ok {
				se = v.Snapshot()
			}
			c.Row(string(id), ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("name", cmp.Or(se.Name, string(id)), kit.MonoFont(11.5, th.C(theme.Fg)))
				if se.Agent.Live() {
					c.Text("state", "stopping", kit.MonoFont(10.5, th.C(theme.FgFaint)))
				} else {
					c.Text("state", "stopped", kit.MonoFont(10.5, th.C(kit.TPermAllow)))
				}
			})
		}
	})
}

func (x *quitDialog) drainBody(c *ui.Ctx, r *quitDrain, busy []core.SessionState, jobs []host.Job, width float32) {
	th := c.Theme()
	if len(busy) > 0 {
		remaining := max(r.deadline.Sub(c.Now()), 0)
		left := (remaining + time.Second - 1).Truncate(time.Second)
		if remaining > 0 {
			c.WakeAt(c.Now().Add(remaining - left + time.Second)) // the next second's count
		}
		lead, then := "Oriel stops the agents running your sessions as it quits.",
			fmt.Sprintf("It quits when these turns end, or in %s; their agents come back when Oriel opens again.", left)
		widget.Wrap(c, "t", lead, width, kit.UIFont(12.5, th.C(theme.Fg)))
		widget.Wrap(c, "when", then, width, kit.UIFont(12, th.C(theme.FgMuted)))
		c.Column("busy", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			for _, se := range busy {
				c.Row(string(se.ID), ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("name", kit.Label(se), kit.MonoFont(11.5, th.C(theme.Fg)))
					c.Text("state", "mid-turn", kit.MonoFont(10.5, th.C(theme.FgFaint)))
				})
			}
		})
	}
	if len(jobs) > 0 {
		widget.Wrap(c, "jobs", "Scheduled jobs don't run while Oriel is closed: one that comes due runs once when it opens again.",
			width, kit.UIFont(12, th.C(theme.Warn)))
		c.Column("scheduled", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.Insets{T: 2}}, func(c *ui.Ctx) {
			for _, j := range jobs {
				c.Row(j.Name, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("name", cmp.Or(j.Title, j.Name), kit.MonoFont(11.5, th.C(theme.Fg)))
					c.Text("when", j.Schedule.String(), kit.MonoFont(10.5, th.C(theme.FgFaint)))
				})
			}
		})
	}
}
