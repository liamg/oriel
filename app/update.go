package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/update"
	"github.com/liamg/oriel/worker"
)

// Updates is Oriel updating itself and its workers (distribution.md, PL-6).
type Updates struct {
	Updater *update.Updater
	// Relaunch starts the installed Oriel again; it runs once the window has closed after an update's restart.
	Relaunch func() error
}

// The update card. "update ready · restart" is the indicator "update", in the status line's right slot.
const (
	UpdateCardView = OverlayPrefix + "update"
	updateHelp     = "shell/update"
)

// How often the updater asks for a newer release, and how long after start it first does.
var (
	updateFirstCheck = 30 * time.Second
	updateEvery      = time.Hour
)

// Update modes (settings → updates).
const (
	updateAutomatic = "automatic"
	updateAsk       = "ask"
	updateManual    = "manual"
)

type updateExt struct {
	disposer
	sh *shell
	h  ext.Host

	mu     sync.Mutex
	busy   string // what it's doing now: "Checking for updates", "Updating"
	card   bool
	failed string // the last check's or install's error, for the card
	stop   chan struct{}
}

func (x *updateExt) Manifest() ext.Manifest { return ext.Manifest{ID: "update"} }

func (x *updateExt) updater() *update.Updater {
	if x.sh.cfg.Updates == nil {
		return nil
	}
	return x.sh.cfg.Updates.Updater
}

func (x *updateExt) Activate(_ context.Context, h ext.Host) error {
	x.h = h
	x.add(h.Option(registry.OptionSpec{Name: "updates.mode", Type: registry.Enum, Values: []string{updateAutomatic, updateAsk, updateManual},
		Default: updateAsk, Scopes: registry.ScopeUser,
		Help: "automatic installs a new release and its workers' as it comes out; ask offers it; manual only checks when you run :UpdateCheck"}))
	x.add(h.Option(registry.OptionSpec{Name: "updates.channel", Type: registry.Enum, Values: []string{update.Stable, update.Nightly},
		Default: update.Stable, Scopes: registry.ScopeUser, Help: "stable releases, or nightly builds as well"}))
	can := func(registry.Args) bool { return x.updater() != nil }
	for _, c := range []registry.Command{
		{ID: "UpdateCheck", Title: "Check for a newer Oriel now, and show what it brings", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.checkNow(true); return nil }},
		{ID: "update.check", Title: "Check for a newer Oriel now, and show what it brings", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.checkNow(true); return nil }},
		{ID: "update.notes", Title: "Show the update card: the newer release and its notes", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.setCard(true); return nil }},
		{ID: "update.install", Title: "Install the newer release here and on every worker that's behind; Oriel runs it after a restart",
			Available: func(registry.Args) bool { _, ok := x.latest(); return ok },
			Run:       func(registry.Ctx, registry.Args) error { return x.install() }},
		{ID: "update.restart", Title: "Restart onto the installed update: this machine's worker drains first",
			Available: func(registry.Args) bool { _, ok := x.ready(); return ok },
			Run:       func(registry.Ctx, registry.Args) error { return x.restart() }},
	} {
		x.add(h.Command(c))
	}
	x.add(h.View(UpdateCardView, registry.ViewFunc(func(registry.ViewCtx) ui.Node { return &ui.Func{Key: "update", Fn: x.cardView} })))
	x.add(h.Contribute(registry.Indicator{Name: "update", Slot: registry.SlotStatusRight, Order: 70, Help: updateHelp + "#ready",
		Build: func(registry.Ctx) (ui.Node, error) {
			if _, ok := x.ready(); !ok && !x.updating() {
				return nil, nil
			}
			return &ui.Func{Key: "update", Fn: x.statusSegment}, nil
		}}))
	if u := x.updater(); u != nil {
		x.add(onDispose(x.sh.onWorkerJoined(x.joined)))
		x.stop = make(chan struct{})
		stop := x.stop
		x.add(onDispose(func() { close(stop) }))
		go x.watch(stop, updateFirstCheck, updateEvery)
	}
	return nil
}

func (x *updateExt) mode() string {
	v, _, _ := x.h.Options().Get("updates.mode", registry.At{})
	s, _ := v.(string)
	return s
}

func (x *updateExt) channel() string {
	v, _, _ := x.h.Options().Get("updates.channel", registry.At{})
	s, _ := v.(string)
	return s
}

func (x *updateExt) latest() (update.Manifest, bool) {
	if u := x.updater(); u != nil {
		return u.Latest()
	}
	return update.Manifest{}, false
}

func (x *updateExt) ready() (string, bool) {
	if u := x.updater(); u != nil {
		return u.Ready()
	}
	return "", false
}

// updating is whether an install is running.
func (x *updateExt) updating() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return strings.HasPrefix(x.busy, "Updating")
}

func (x *updateExt) setCard(open bool) {
	x.mu.Lock()
	x.card = open
	x.mu.Unlock()
	x.h.Redraw()
}

// watch checks now and then, unless updates are manual.
func (x *updateExt) watch(stop chan struct{}, first, every time.Duration) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if x.mode() != updateManual {
				x.check(false)
			}
			t.Reset(every)
		}
	}
}

func (x *updateExt) checkNow(asked bool) {
	if asked {
		x.setCard(true)
	}
	go x.check(asked)
}

// check asks for a newer release. One the user asked for opens the card whatever it finds; one on the timer offers
// a release (ask), installs it (automatic), or says nothing.
func (x *updateExt) check(asked bool) {
	u := x.updater()
	if !x.begin("Checking for updates") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	m, err := u.Check(ctx, x.channel())
	cancel()
	x.end(err)
	switch {
	case errors.Is(err, update.ErrUpToDate):
		x.setFailed("")
		return
	case err != nil:
		x.h.Log().Warn("update check", "err", err)
		return
	}
	x.setFailed("")
	if asked {
		return
	}
	if _, ok := u.Ready(); ok {
		return
	}
	switch x.mode() {
	case updateAutomatic:
		x.h.Do(func() {
			if err := x.install(); err != nil {
				x.h.Log().Warn("update install", "err", err)
			}
		})
	case updateAsk:
		x.h.Notify(core.Notice{ID: "update.available", Severity: core.SevInfo, Title: "Update",
			Text:    fmt.Sprintf("Oriel %s is out: you have %s.", m.Version, u.Version),
			Actions: []core.Action{{Title: "What's new", Command: "update.notes"}, {Title: "Update", Command: "update.install"}}})
	}
}

func (x *updateExt) begin(what string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy != "" {
		return false
	}
	x.busy = what
	x.refresh()
	return true
}

func (x *updateExt) end(err error) {
	x.mu.Lock()
	x.busy = ""
	if err != nil && !errors.Is(err, update.ErrUpToDate) {
		x.failed = err.Error()
	}
	x.mu.Unlock()
	x.refresh()
}

// refresh builds the indicator again, as what it shows has changed.
func (x *updateExt) refresh() {
	x.h.Do(func() { _ = x.h.Run(context.Background(), "hud.refresh", registry.Args{"name": "update"}) })
}

func (x *updateExt) setFailed(s string) {
	x.mu.Lock()
	x.failed = s
	x.mu.Unlock()
}

// install, on the loop, updates every connected worker that's behind, then this copy, which Oriel runs after a restart. A worker
// that fails says so and doesn't stop the rest.
func (x *updateExt) install() error {
	u := x.updater()
	if u == nil {
		return errors.New("this build of Oriel doesn't update itself")
	}
	m, ok := u.Latest()
	if !ok {
		return update.ErrNoRelease
	}
	if !x.begin("Updating to " + m.Version) {
		return errors.New("an update is already running")
	}
	x.h.Dismiss("update.available")
	var workers map[core.WorkerID]worker.Client
	if x.sh.sessions != nil {
		workers = x.sh.sessions.byWorker()
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		for id, c := range workers {
			x.pushTo(ctx, id, c)
		}
		var err error
		if u.Installer != nil {
			err = u.Install(ctx)
		}
		x.end(err)
		switch {
		case err != nil:
			x.h.Notify(core.Notice{ID: "update.failed", Severity: core.SevError, Title: "Update",
				Text: fmt.Sprintf("Oriel %s didn't install: %v", m.Version, err)})
		case u.Installer == nil:
		default:
			x.h.Notify(core.Notice{ID: "update.ready", Severity: core.SevInfo, Title: "Update",
				Text:    fmt.Sprintf("Oriel %s is installed: it runs from the next restart.", m.Version),
				Actions: []core.Action{{Title: "Restart", Command: "update.restart"}}})
		}
	}()
	return nil
}

// pushTo updates one worker that's behind the latest release.
func (x *updateExt) pushTo(ctx context.Context, id core.WorkerID, c worker.Client) {
	p, err := x.updater().PushTo(ctx, c.Conn)
	source := "worker:" + string(id)
	switch {
	case err != nil:
		x.h.Notify(core.Notice{ID: "update.worker", Severity: core.SevWarning, Source: source, Title: "Worker update",
			Text: fmt.Sprintf("%s wasn't updated: %v", id, err)})
	case p.To != "":
		x.h.Log().Info("updated a worker", "worker", id, "from", p.From, "to", p.To)
		x.h.Notify(core.Notice{ID: "update.worker", Severity: core.SevInfo, Source: source, Title: "Worker update",
			Text: fmt.Sprintf("%s is restarting on %s (it ran %s); its agents come back on it.", id, p.To, p.From)})
	}
}

// joined serves a worker that connects the releases offered to it, and updates it first if it's behind a release
// Oriel already has (distribution.md, "Version skew").
func (x *updateExt) joined(c worker.Client, info worker.Info) {
	u := x.updater()
	u.Serve(c.Conn)
	if x.mode() == updateManual {
		return
	}
	if _, ok := u.Latest(); !ok {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		x.pushTo(ctx, info.ID, c)
	}()
}

// restart quits, draining this machine's worker, and starts the installed Oriel once the window has closed.
func (x *updateExt) restart() error {
	if _, ok := x.ready(); !ok {
		return errors.New("no update is waiting for a restart")
	}
	relaunch := x.sh.cfg.Updates.Relaunch
	x.sh.relaunch.Store(&relaunch)
	x.sh.requestQuit()
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

// statusSegment is "update ready · restart" on the status line once an update is installed, and what the updater
// is doing while it works.
func (x *updateExt) statusSegment(c *ui.Ctx) {
	x.mu.Lock()
	busy := x.busy
	x.mu.Unlock()
	if v, ok := x.ready(); ok {
		label := "Update ready · restart"
		in := widget.Clickable(c, "update", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: updateHelp + "#ready", Role: ui.RoleButton,
			Label: label, Tooltip: "Oriel " + v + " is installed: restart to run it"}, func(c *ui.Ctx) {
			c.Text("t", "update ready · restart", statusText(c, theme.Accent))
		})
		if in.Clicked {
			if err := x.h.Run(context.Background(), "update.restart", nil); err != nil {
				x.h.Notify(core.Notice{ID: "update.restart", Severity: core.SevError, Title: "Update", Text: err.Error()})
			}
		}
		return
	}
	if strings.HasPrefix(busy, "Updating") {
		c.Row("update", ui.Style{H: ui.Flex(1), Cross: ui.Center, Help: updateHelp + "#install", Role: ui.RoleStatus, Label: busy}, func(c *ui.Ctx) {
			c.Text("t", strings.ToLower(busy)+" …", statusText(c, tStatusFg))
		})
	}
}

// cardView is the update card (ui.md has none; distribution.md, "Release notes"): the newer release, its notes,
// and Update, or Restart once it's installed.
func (x *updateExt) cardView(c *ui.Ctx) {
	x.mu.Lock()
	open, busy, failed := x.card, x.busy, x.failed
	x.mu.Unlock()
	if !open {
		return
	}
	u := x.updater()
	m, found := x.latest()
	title := "Updates"
	if found {
		title = "Oriel " + m.Version
	}
	th := c.Theme()
	widget.Modal(c, "updatecard", &open, title, widget.ModalOpts{Width: 620, Help: updateHelp + "#card"}, func(c *ui.Ctx) {
		ready, isReady := x.ready()
		switch {
		case u == nil:
			widget.Wrap(c, "none", "This build of Oriel doesn't update itself.", 570, uiFont(12.5, th.C(theme.Fg)))
		case found:
			sub := fmt.Sprintf("you have %s · %s channel", u.Version, x.channel())
			if !m.Published.IsZero() {
				sub += " · released " + m.Published.Format("2 Jan 2006")
			}
			heading(c, "h", "WHAT'S NEW", sub)
			notes := strings.TrimSpace(m.Notes)
			if notes == "" {
				notes = "This release has no notes."
			}
			c.Column("notes", ui.Style{W: ui.Flex(1), H: ui.Size{Kind: ui.Fit}.Clamp(0, 320), ScrollY: true, Pad: ui.Insets{R: 8}}, func(c *ui.Ctx) {
				markdownText(c, "md", notes, 556, uiFont(12.5, th.C(theme.FgMuted)))
			})
		case busy != "":
		default:
			widget.Wrap(c, "current", fmt.Sprintf("Oriel %s is the newest on the %s channel.", u.Version, x.channel()), 570,
				uiFont(12.5, th.C(theme.Fg)))
		}
		if busy != "" {
			note(c, "busy", busy+" …")
		}
		if failed != "" {
			widget.Wrap(c, "err", failed, 570, monoFont(11, th.C(theme.Error)))
		}
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			if isReady {
				note(c, "ready", "Oriel "+ready+" is installed")
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "close", "Later", widget.ButtonOpts{Key: "esc", Help: updateHelp + "#card"}) {
				open = false
			}
			switch {
			case isReady:
				if widget.Button(c, "restart", "Restart", widget.ButtonOpts{Primary: true, Help: updateHelp + "#ready"}) {
					open = false
					x.run("update.restart")
				}
			case found:
				if widget.Button(c, "install", "Update", widget.ButtonOpts{Primary: true, Disabled: busy != "", Help: updateHelp + "#install"}) {
					x.run("update.install")
				}
			case u != nil:
				if widget.Button(c, "check", "Check again", widget.ButtonOpts{Disabled: busy != "", Help: updateHelp + "#check"}) {
					x.run("update.check")
				}
			}
		})
	})
	if !open {
		x.setCard(false)
	}
}

func (x *updateExt) run(id string) {
	if err := x.h.Run(context.Background(), id, nil); err != nil {
		x.setFailed(err.Error())
		x.h.Redraw()
	}
}
