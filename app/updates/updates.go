// Package updates is Oriel updating itself: checking for a new release, downloading and staging it, and restarting
// into it, with its card on the settings page and its own page.
package updates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit/work"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/update"
)

// Updates is Oriel updating itself.
type Updates struct {
	Updater *update.Updater
	// Relaunch starts the installed Oriel again; it runs once the window has closed after an update's restart.
	Relaunch func() error
}

// The update card, and the Updating page. "update ready · restart" is the indicator "update", in the status line's
// right slot.
const (
	UpdateCardView = kit.OverlayPrefix + "update"
	updateHelp     = "shell/update"
)

// How often the updater asks for a newer release, and how long after start it first does when it didn't at start.
var (
	updateFirstCheck = 30 * time.Second
	updateEvery      = time.Hour
	// updateStartCheck bounds the check an automatic start makes before anything else.
	updateStartCheck = 20 * time.Second
)

// Update modes (settings → updates).
const (
	updateAutomatic = "automatic"
	updateAsk       = "ask"
	updateManual    = "manual"
)

var (
	tUpdateOK      = theme.Declare("update.step.ok", "ok")
	tUpdateRunning = theme.Declare("update.step.running", "status.working")
	tUpdateFailed  = theme.Declare("update.step.failed", "error")
)

type Update struct {
	kit.Disposer
	Updates  *Updates // nil in a build that doesn't update itself
	StateDir string   // where the version last run is kept
	h        plugin.Host

	mu     sync.Mutex
	busy   string    // what it's doing now: "Checking for updates", "Downloading 2.0.0", "Updating to 2.0.0"
	since  time.Time // when it started
	card   bool
	failed string         // the last check's or install's error, for the card
	work   work.Work      // the timer's checks
	run    *updateRun     // the update the Updating page shows, if any
	start  sync.WaitGroup // an automatic start's check, while it runs
	page   bool           // the Updating page is up
}

// updateRun is one update as the Updating page tells it: each step as a line, the download's bytes, and how it
// ended.
type updateRun struct {
	from, to    string
	lines       []updateLine
	done, total int64
	restart     bool   // it ends in a restart onto the new version
	installed   bool   // swapped in
	failed      string // why it stopped
}

type updateLine struct {
	stage, text string
	at          time.Time
	state       lineState
}

type lineState int

const (
	lineRunning lineState = iota
	lineOK
	lineFailed
)

func (x *Update) Manifest() plugin.Manifest { return plugin.Manifest{ID: "update"} }

func (x *Update) updater() *update.Updater {
	if x.Updates == nil {
		return nil
	}
	return x.Updates.Updater
}

func (x *Update) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.Add(h.Option(registry.OptionSpec{Name: "updates.mode", Type: registry.Enum, Values: []string{updateAutomatic, updateAsk, updateManual},
		Default: updateAutomatic, Scopes: registry.ScopeUser,
		Help: "automatic updates as Oriel starts, and installs a release that comes out while it runs for the next restart; ask downloads it and offers it; manual only checks when you run :UpdateCheck"}))
	x.Add(h.Option(registry.OptionSpec{Name: "updates.channel", Type: registry.Enum, Values: []string{update.Stable, update.Nightly},
		Default: update.Stable, Scopes: registry.ScopeUser, Help: "stable releases, or nightly builds as well"}))
	for _, c := range x.commands() {
		x.Add(h.Command(c))
	}
	x.Add(h.View(UpdateCardView, uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "update", Fn: func(c *ui.Ctx) { x.pageView(c); x.cardView(c) }}
	})))
	x.Add(h.Contribute(uireg.Indicator{Name: "update", Slot: uireg.SlotStatusRight, Order: 70, Global: true, Help: updateHelp + "#ready",
		Build: func(registry.Ctx) (ui.Node, error) {
			if _, ok := x.ready(); !ok && !x.working() {
				return nil, nil
			}
			return &ui.Func{Key: "update", Fn: x.statusSegment}, nil
		}}))
	x.updated()
	if u := x.updater(); u != nil {
		u.Progress = x.progress
		x.Add(&x.work)
		first := updateFirstCheck
		if x.mode() == updateAutomatic {
			first = updateEvery
			x.start.Add(1)
			plugin.Go(x.h, "the update check at start", func() {
				defer x.start.Done()
				x.startup()
			})
		}
		x.work.Loop(func(ctx context.Context) { x.watch(ctx, first, updateEvery) })
	}
	return nil
}

func (x *Update) commands() []registry.Command {
	can := func(registry.Args) bool { return x.updater() != nil }
	return []registry.Command{
		{ID: "UpdateCheck", Title: "Check for a newer Oriel now, and show what it brings", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.checkNow(true); return nil }},
		{ID: "update.check", Title: "Check for a newer Oriel now, and show what it brings", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.checkNow(true); return nil }},
		{ID: "update.notes", Title: "Show the update card: the newer release and its notes", Available: can,
			Run: func(registry.Ctx, registry.Args) error { x.setCard(true); return nil }},
		{ID: "update.install", Title: "Update Oriel now: download, verify and install the newer release, then restart onto it once the agents have handed over",
			Available: func(registry.Args) bool { _, ok := x.latest(); return ok },
			Run:       func(registry.Ctx, registry.Args) error { return x.update(true, true) }},
		{ID: "update.restart", Title: "Restart onto the installed update: running agents hand over first",
			Available: func(registry.Args) bool { _, ok := x.ready(); return ok },
			Run:       func(registry.Ctx, registry.Args) error { return x.restart() }},
		{ID: "update.progress", Title: "Show the Updating page: how the update is getting on",
			Available: func(registry.Args) bool { return x.current() != nil },
			Run:       func(registry.Ctx, registry.Args) error { x.setPage(true); return nil }},
	}
}

func (x *Update) mode() string {
	s, _ := registry.Option[string](x.h.Options(), "updates.mode", registry.At{})
	return s
}

func (x *Update) channel() string {
	s, _ := registry.Option[string](x.h.Options(), "updates.channel", registry.At{})
	return s
}

func (x *Update) latest() (update.Manifest, bool) {
	if u := x.updater(); u != nil {
		return u.Latest()
	}
	return update.Manifest{}, false
}

func (x *Update) ready() (string, bool) {
	if u := x.updater(); u != nil {
		return u.Ready()
	}
	return "", false
}

// working is whether a download or an install is running.
func (x *Update) working() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return strings.HasPrefix(x.busy, "Updating") || strings.HasPrefix(x.busy, "Downloading")
}

func (x *Update) current() *updateRun {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.run
}

func (x *Update) setCard(open bool) {
	x.mu.Lock()
	x.card = open
	x.mu.Unlock()
	x.h.Redraw()
}

func (x *Update) setPage(open bool) {
	x.mu.Lock()
	x.page = open
	x.mu.Unlock()
	x.h.Redraw()
}

// updated says, once, that this start is a newer version than the last one: the restart an update ended in.
func (x *Update) updated() {
	u := x.updater()
	if u == nil || x.StateDir == "" {
		return
	}
	path := filepath.Join(x.StateDir, "version")
	last, _ := os.ReadFile(path)
	if prev := strings.TrimSpace(string(last)); prev != "" && update.Newer(u.Version, prev) {
		x.h.Notify(core.Notice{ID: "update.done", Severity: core.SevInfo, Title: "Update",
			Text: fmt.Sprintf("Oriel updated to %s, from %s.", u.Version, prev)})
	}
	_ = os.WriteFile(path, []byte(u.Version+"\n"), 0o644)
}

// startup is an automatic start: it checks before anything else, and a newer release is installed and restarted
// onto at once, on the Updating page.
func (x *Update) startup() {
	if !x.begin("Checking for updates") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), updateStartCheck)
	_, err := x.updater().Check(ctx, x.channel())
	cancel()
	x.end(err)
	if err != nil {
		if !errors.Is(err, update.ErrUpToDate) {
			x.h.Log().Warn("update check at start", "err", err)
		}
		return
	}
	x.h.Do(func() {
		if err := x.update(true, true); err != nil {
			x.h.Log().Warn("update at start", "err", err)
		}
	})
}

// watch checks now and then, unless updates are manual.
func (x *Update) watch(ctx context.Context, first, every time.Duration) {
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if x.mode() != updateManual {
				x.check(false)
			}
			t.Reset(every)
		}
	}
}

// checkNow checks off the loop. One the user asked for opens the card, already busy; while a check is running it
// shows that one's answer.
func (x *Update) checkNow(asked bool) {
	began := x.begin("Checking for updates")
	if asked {
		x.setCard(true)
	}
	if began {
		plugin.Go(x.h, "an update check", func() { x.checked(asked) })
	}
}

// check asks for a newer release, unless a check or install is running.
func (x *Update) check(asked bool) {
	if x.begin("Checking for updates") {
		x.checked(asked)
	}
}

// checked runs a check begun. One the user asked for opens the card whatever it finds; one on the timer downloads a
// newer release first, then offers it (ask) or installs it for the next restart (automatic).
func (x *Update) checked(asked bool) {
	u := x.updater()
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
	x.download(m)
}

// download fetches m ahead, quietly; then ask offers it and automatic installs it for the next restart.
func (x *Update) download(m update.Manifest) {
	if !x.begin("Downloading " + m.Version) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	err := x.updater().Download(ctx)
	cancel()
	x.end(err)
	if err != nil {
		x.h.Log().Warn("update download", "err", err)
		return
	}
	switch x.mode() {
	case updateAutomatic:
		x.h.Do(func() {
			if err := x.update(false, false); err != nil {
				x.h.Log().Warn("update install", "err", err)
			}
		})
	case updateAsk:
		x.h.Notify(core.Notice{ID: "update.available", Severity: core.SevInfo, Title: "Update",
			Text:    fmt.Sprintf("Oriel %s is downloaded and ready: you have %s.", m.Version, x.updater().Version),
			Actions: []core.Action{{Title: "What's new", Command: "update.notes"}, {Title: "Update", Command: "update.install"}}})
	}
}

func (x *Update) begin(what string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.busy != "" {
		return false
	}
	x.busy, x.since = what, time.Now()
	x.refresh()
	return true
}

func (x *Update) end(err error) {
	x.mu.Lock()
	x.busy = ""
	if err != nil && !errors.Is(err, update.ErrUpToDate) {
		x.failed = err.Error()
	}
	x.mu.Unlock()
	x.refresh()
}

// refresh builds the indicator again, as what it shows has changed.
func (x *Update) refresh() {
	x.h.Do(func() { _ = x.h.Run(context.Background(), "hud.refresh", registry.Args{"name": "update"}) })
}

func (x *Update) setFailed(s string) {
	x.mu.Lock()
	x.failed = s
	x.mu.Unlock()
}

// progress is the updater telling the run how far it's got: a stage starts a line, and the download's line counts
// its bytes.
func (x *Update) progress(s update.Step) {
	x.mu.Lock()
	r := x.run
	if r == nil {
		x.mu.Unlock()
		return
	}
	if s.Stage == update.StageDownload {
		r.done, r.total = s.Done, s.Total
	}
	text := stepText(s)
	if n := len(r.lines); n > 0 && r.lines[n-1].stage == s.Stage && r.lines[n-1].state == lineRunning {
		r.lines[n-1].text = text
	} else {
		r.say(s.Stage, text)
	}
	x.mu.Unlock()
	x.h.Redraw()
}

// say ends the line running and starts another.
func (r *updateRun) say(stage, text string) {
	if n := len(r.lines); n > 0 && r.lines[n-1].state == lineRunning {
		r.lines[n-1].state = lineOK
	}
	r.lines = append(r.lines, updateLine{stage: stage, text: text, at: time.Now()})
}

func stepText(s update.Step) string {
	switch s.Stage {
	case update.StageDownload:
		if s.Total > 0 {
			return fmt.Sprintf("Downloading %s · %s of %s", s.Detail, kit.Bytes(s.Done), kit.Bytes(s.Total))
		}
		return "Downloading " + s.Detail
	case update.StageVerify:
		return "Verified " + s.Detail
	case update.StageExtract:
		return "Extracting " + s.Detail
	case update.StageCheck:
		return "Checking the new version starts: " + s.Detail
	case update.StageSwap:
		return "Swapping in the new " + filepath.Base(s.Detail) + "; the old one is kept for a rollback"
	}
	return s.Detail
}

// update, on the loop, installs the newer release in the background, told on the Updating page when page is set; with
// restart, Oriel then restarts onto it once the agents have handed over.
func (x *Update) update(restart, page bool) error {
	u := x.updater()
	if u == nil || u.Installer == nil {
		return errors.New("this build of Oriel doesn't update itself")
	}
	m, ok := u.Latest()
	if !ok {
		return update.ErrNoRelease
	}
	if v, ok := u.Ready(); ok && v == m.Version {
		if restart {
			return x.restart()
		}
		return nil
	}
	if !x.begin("Updating to " + m.Version) {
		return errors.New("an update is already running")
	}
	r := &updateRun{from: u.Version, to: m.Version, restart: restart}
	r.say("found", fmt.Sprintf("Found Oriel %s on the %s channel: you have %s", m.Version, x.channel(), u.Version))
	x.mu.Lock()
	x.run = r
	if page {
		x.page = true
	}
	x.mu.Unlock()
	x.h.Dismiss("update.available")
	plugin.Go(x.h, "an update's install", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		err := u.Install(ctx)
		x.end(err)
		x.mu.Lock()
		if err != nil {
			if n := len(r.lines); n > 0 && r.lines[n-1].state == lineRunning {
				r.lines[n-1].state = lineFailed
			}
			r.failed = err.Error()
		} else {
			r.installed = true
			r.say("installed", "Oriel "+m.Version+" is installed")
			if !restart {
				r.lines[len(r.lines)-1].state = lineOK
			}
		}
		x.mu.Unlock()
		x.h.Redraw()
		switch {
		case err != nil:
			x.h.Notify(core.Notice{ID: "update.failed", Severity: core.SevError, Title: "Update",
				Text:    fmt.Sprintf("Oriel %s didn't install: %v", m.Version, err),
				Actions: []core.Action{{Title: "Details", Command: "update.progress"}}})
		case restart:
			x.h.Do(func() {
				if err := x.restart(); err != nil {
					x.h.Log().Warn("update restart", "err", err)
				}
			})
		default:
			x.h.Notify(core.Notice{ID: "update.ready", Severity: core.SevInfo, Title: "Update",
				Text:    fmt.Sprintf("Oriel %s is installed: it runs from the next restart.", m.Version),
				Actions: []core.Action{{Title: "Restart", Command: "update.restart"}}})
		}
	})
	return nil
}

// restart quits, handing the agents over, and starts the installed Oriel once the window has closed. With the
// Updating page up, the page tells the hand-over in place of the quit dialog.
func (x *Update) restart() error {
	v, ok := x.ready()
	if !ok {
		return errors.New("no update is waiting for a restart")
	}
	x.mu.Lock()
	if r := x.run; r != nil && r.to == v {
		r.say("drain", "Handing the sessions over: their agents resume in Oriel "+v)
	}
	page := x.page && x.run != nil
	x.mu.Unlock()
	q, ok := kit.QuitOf(x.h)
	if !ok {
		return errors.New("this window can't restart")
	}
	q.Restart(x.Updates.Relaunch)
	if d, ok := q.Draining(); ok && page {
		d.Hide(true)
	}
	return nil
}

// draining is the hand-over the restart's quit waits on, if one does.
func (x *Update) draining() kit.Drain {
	if q, ok := kit.QuitOf(x.h); ok {
		if d, ok := q.Draining(); ok {
			return d
		}
	}
	return nil
}

// statusSegment is "update ready · restart" on the status line once an update is installed, and what the updater
// is doing while it downloads or installs.
func (x *Update) statusSegment(c *ui.Ctx) {
	x.mu.Lock()
	busy, r := x.busy, x.run
	x.mu.Unlock()
	if v, ok := x.ready(); ok {
		label := "Update ready · restart"
		in := widget.Clickable(c, "update", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: updateHelp + "#ready", Role: ui.RoleButton,
			Label: label, Tooltip: "Oriel " + v + " is installed: restart to run it"}, func(c *ui.Ctx) {
			c.Text("t", "update ready · restart", kit.StatusText(c, theme.Accent))
		})
		if in.Clicked {
			if err := x.h.Run(context.Background(), "update.restart", nil); err != nil {
				x.h.Notify(core.Notice{ID: "update.restart", Severity: core.SevError, Title: "Update", Text: err.Error()})
			}
		}
		return
	}
	if !strings.HasPrefix(busy, "Updating") && !strings.HasPrefix(busy, "Downloading") {
		return
	}
	text := strings.ToLower(busy) + " …"
	if r != nil && r.total > 0 && r.done < r.total {
		text = fmt.Sprintf("%s · %d%%", strings.ToLower(busy), r.done*100/r.total)
	}
	in := widget.Clickable(c, "update", ui.Style{H: ui.Flex(1), Cross: ui.Center, Help: updateHelp + "#install", Role: ui.RoleButton,
		Label: busy, Tooltip: "Show how the update is getting on"}, func(c *ui.Ctx) {
		c.Text("t", text, kit.StatusText(c, kit.TStatusFg))
	})
	if in.Clicked && r != nil {
		x.setPage(true)
	}
}

// pageView is the Updating page: each step of the update as it happens, the download's progress, and the hand-over
// and restart it ends in.
func (x *Update) pageView(c *ui.Ctx) {
	x.mu.Lock()
	open, run := x.page, x.run
	var r updateRun
	if run != nil {
		r = *run
		r.lines = append([]updateLine(nil), run.lines...)
	}
	x.mu.Unlock()
	if !open || run == nil {
		return
	}
	th := c.Theme()
	drain := x.draining()
	if drain != nil {
		drain.Hide(true)
	}
	widget.Page(c, "updating", &open, widget.PageOpts{Label: "Updating Oriel", Help: updateHelp + "#page"}, func(c *ui.Ctx) {
		c.Column("centre", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.PadXY(24, 24)}, func(c *ui.Ctx) {
			c.Column("box", ui.Style{W: ui.Px(min(640, c.Size().X-48)), Gap: 14}, func(c *ui.Ctx) {
				c.Text("title", "UPDATING ORIEL", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16, Weight: typeset.Bold, Tracking: .24},
					Color: th.C(theme.Fg)})
				c.Text("versions", r.from+"  →  "+r.to, kit.MonoFont(12, th.C(theme.FgMuted)))
				frac := -1.0
				switch {
				case r.failed != "":
					frac = 0
				case r.total > 0 && r.done < r.total:
					frac = float64(r.done) / float64(r.total)
				case r.installed && !r.restart:
					frac = 1
				}
				widget.ProgressBar(c, "bar", widget.ProgressOpts{Frac: frac, Height: 6, Label: "Update progress", Help: updateHelp + "#page"})
				c.Column("log", ui.Style{W: ui.Flex(1), Gap: 6, Pad: ui.Insets{T: 4}, Role: ui.RoleList, Label: "Update steps"}, func(c *ui.Ctx) {
					for i, l := range r.lines {
						updateStepRow(c, i, l)
					}
					if r.installed && r.restart {
						x.drainSteps(c, drain, r.to)
					}
				})
				if r.failed != "" {
					widget.Wrap(c, "err", r.failed, 600, kit.MonoFont(11, th.C(theme.Error)))
				}
				c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Main: ui.End, Pad: ui.Insets{T: 8}}, func(c *ui.Ctx) {
					switch {
					case r.failed != "":
						if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: updateHelp + "#page"}) {
							open = false
						}
						if widget.Button(c, "retry", "Try again", widget.ButtonOpts{Primary: true, Help: updateHelp + "#page"}) {
							x.mu.Lock()
							x.run = nil
							x.mu.Unlock()
							if err := x.update(r.restart, true); err != nil {
								x.setFailed(err.Error())
							}
						}
					case drain != nil:
						if widget.Button(c, "cancel", "Not now", widget.ButtonOpts{Disabled: drain.Handing(), Help: updateHelp + "#page"}) {
							drain.Cancel()
						}
						if widget.Button(c, "now", "Restart now", widget.ButtonOpts{Primary: true, Disabled: drain.Handing(), Help: updateHelp + "#page"}) {
							drain.Now()
						}
					case r.installed && !r.restart:
						if widget.Button(c, "later", "Later", widget.ButtonOpts{Key: "esc", Help: updateHelp + "#page"}) {
							open = false
						}
						if widget.Button(c, "restart", "Restart", widget.ButtonOpts{Primary: true, Help: updateHelp + "#ready"}) {
							x.command("update.restart")
						}
					case !r.installed:
						if widget.Button(c, "hide", "Hide", widget.ButtonOpts{Key: "esc", Help: updateHelp + "#page"}) {
							open = false
						}
					}
				})
			})
		})
	})
	if !open {
		x.setPage(false)
		if drain != nil {
			drain.Hide(false)
		}
	}
}

// drainSteps are the hand-over's lines: the turns it waits for, then the agents stopping, then the restart.
func (x *Update) drainSteps(c *ui.Ctx, d kit.Drain, to string) {
	if d == nil {
		updateStepRow(c, "restart", updateLine{text: "Restarting into Oriel " + to, state: lineRunning})
		return
	}
	busy, polled := d.Busy()
	switch {
	case d.Handing():
		updateStepRow(c, "stopping", updateLine{text: "Stopping the agents: they resume in Oriel " + to, state: lineRunning})
	case polled && len(busy) > 0:
		left := max(d.Deadline().Sub(c.Now()), 0).Round(time.Second)
		c.WakeAt(c.Now().Add(time.Second))
		var names []string
		for _, se := range busy {
			names = append(names, kit.Label(se))
		}
		updateStepRow(c, "waiting", updateLine{text: fmt.Sprintf("Waiting for %s to finish %s, at most %s more: %s", kit.Counted(len(busy), "session"),
			kit.Plural(len(busy), "its turn", "their turns"), left, strings.Join(names, ", ")), state: lineRunning})
	}
}

func updateStepRow(c *ui.Ctx, key any, l updateLine) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true, Role: ui.RoleText, Label: l.text}, func(c *ui.Ctx) {
		switch l.state {
		case lineOK:
			c.Text("mark", "✓", kit.MonoFont(11.5, th.C(tUpdateOK)))
		case lineFailed:
			c.Text("mark", "✕", kit.MonoFont(11.5, th.C(tUpdateFailed)))
		default:
			widget.Spinner(c, "mark", widget.SpinnerOpts{Size: 11, Tone: th.C(tUpdateRunning)})
		}
		c.Text("text", l.text, kit.MonoFont(11.5, th.C(theme.Fg)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if !l.at.IsZero() {
			c.Text("at", l.at.Local().Format("15:04:05"), kit.MonoFont(10.5, th.C(theme.FgFaint)))
		}
	})
}

// cardView is the update card: the newer release, its notes,
// and Update, or Restart once it's installed.
func (x *Update) cardView(c *ui.Ctx) {
	x.mu.Lock()
	open, busy, since, failed := x.card, x.busy, x.since, x.failed
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
	widget.Modal(c, "updatecard", &open, widget.ModalOpts{Title: title, Width: 620, Help: updateHelp + "#card"}, func(c *ui.Ctx) {
		ready, isReady := x.ready()
		switch {
		case u == nil:
			widget.Wrap(c, "none", "This build of Oriel doesn't update itself.", 570, kit.UIFont(12.5, th.C(theme.Fg)))
		case found:
			sub := fmt.Sprintf("you have %s · %s channel", u.Version, x.channel())
			if !m.Published.IsZero() {
				sub += " · released " + m.Published.Format("2 Jan 2006")
			}
			kit.Heading(c, "h", "WHAT'S NEW", sub)
			notes := strings.TrimSpace(m.Notes)
			if notes == "" {
				notes = "This release has no notes."
			}
			c.Column("notes", ui.Style{W: ui.Flex(1), H: ui.Size{Kind: ui.Fit}.Clamp(0, 320), ScrollY: true, Pad: ui.Insets{R: 8}}, func(c *ui.Ctx) {
				kit.MarkdownText(c, x.h, "md", notes, 556, kit.UIFont(12.5, th.C(theme.FgMuted)), kit.MarkdownOpts{})
			})
		case busy != "":
		default:
			widget.Wrap(c, "current", fmt.Sprintf("Oriel %s is the newest on the %s channel.", u.Version, x.channel()), 570,
				kit.UIFont(12.5, th.C(theme.Fg)))
		}
		if busy != "" {
			widget.Busy(c, "busy", widget.LoadState{Phase: widget.Pending, Since: since}, widget.BusyOpts{Label: busy, Help: updateHelp + "#check"})
		}
		if failed != "" {
			widget.Wrap(c, "err", failed, 570, kit.MonoFont(11, th.C(theme.Error)))
		}
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			if isReady {
				kit.Note(c, "ready", "Oriel "+ready+" is installed")
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "close", "Later", widget.ButtonOpts{Key: "esc", Help: updateHelp + "#card"}) {
				open = false
			}
			switch {
			case isReady:
				if widget.Button(c, "restart", "Restart", widget.ButtonOpts{Primary: true, Help: updateHelp + "#ready"}) {
					open = false
					x.command("update.restart")
				}
			case found:
				if widget.Button(c, "install", "Update", widget.ButtonOpts{Primary: true, Disabled: busy != "",
					Help: updateHelp + "#install"}) {
					open = false
					x.command("update.install")
				}
			case u != nil:
				if widget.Button(c, "check", "Check again", widget.ButtonOpts{Disabled: busy != "", Busy: strings.HasPrefix(busy, "Checking"),
					BusyLabel: "Checking", Help: updateHelp + "#check"}) {
					x.command("update.check")
				}
			}
		})
	})
	if !open {
		x.setCard(false)
	}
}

func (x *Update) command(id string) {
	if err := x.h.Run(context.Background(), id, nil); err != nil {
		x.setFailed(err.Error())
		x.h.Redraw()
	}
}

// Check asks for a newer release now, as the hourly check does.
func (x *Update) Check() { x.check(false) }
