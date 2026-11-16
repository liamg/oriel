package shell

import (
	"io"
	"log/slog"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/layout"
)

// What the package's outside tests read and set of its plugins.

// SetKeys has the window's keys go through r, as its loop's do.
func SetKeys(h *Headless, r *registry.Resolver) { h.sh.keys = r }

// What fork F1's moved tests read and set of the shell.

func (f *windowFocus) Now() (bool, <-chan struct{}) { return f.now() }
func (f *windowFocus) Set(on bool) bool             { return f.set(on) }

type Help = helpView

func (v *helpView) Open() bool   { return v.open }
func (v *helpView) Page() string { return v.cur }

// LastMove is the last of a run of pointer moves waiting from ev, and the event read after the run.
func LastMove(ev render.Event, events <-chan render.Event) (render.Event, render.Event) {
	return lastMove(ev, events)
}

// TabNameOn is the token the session in front's tab name is drawn in.
func TabNameOn() theme.TokenID { return tTabNameOn }

// Reopening is the directory the window was asked to open again on, taking the ask, and whether it was.
func Reopening(h *Headless) (string, bool) {
	select {
	case to := <-h.sh.reopen:
		return to.dir, true
	default:
		return "", false
	}
}

// ResolveTheme is the theme name in variant v from srcs, as the window resolves ui.theme.
func ResolveTheme(srcs []theme.Source, name string, v theme.Variant) (*theme.Resolved, error) {
	return resolveTheme(srcs, name, v)
}

// Report is "Copy report"'s text, with nothing at its head.
func Report(h *Headless) string { return h.sh.report("") }

// StallTrace is a stall trace, for a test to hold its writes.
type StallTrace struct{ s *stallTrace }

// StartStallTrace starts the stall trace written to path, or answers false where there's no flight recorder.
func StartStallTrace(path string) (StallTrace, bool) {
	s := startStallTrace(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return StallTrace{s}, s != nil
}

func (t StallTrace) Stop()         { t.s.stop() }
func (t StallTrace) Stalled()      { t.s.stalled() }
func (t StallTrace) HoldWrite()    { t.s.wmu.Lock() }
func (t StallTrace) ReleaseWrite() { t.s.wmu.Unlock() }
func (t StallTrace) Forget()       { t.s.written = time.Time{} }

// The log panel's scopes.
const (
	LogScopeOriel   = scopeOriel
	LogScopeSession = scopeSession
)

func logPanelOf(h *Headless) *logPanel {
	p, _ := h.Plugin("notices")
	return p.(*notices).panel
}

// LogOpenRow is the log panel's expanded row's key; "" with none.
func LogOpenRow(h *Headless) string { return logPanelOf(h).open }

// ToastTTL is how long a notification's toast stays; 0 until it's answered.
func ToastTTL(n core.Notification) time.Duration { return toastTTL(n) }

// AddRecent adds dir to the recent directories, opened at at.
func AddRecent(h *Headless, dir string, at time.Time) error { return h.sh.recents.add(dir, at) }

// Recents are the recent directories' paths, as the start view lists them.
func Recents(h *Headless) []string {
	var out []string
	for _, d := range h.sh.recents.list() {
		out = append(out, d.Path)
	}
	return out
}

// DirectoriesSource is Open Directory's picker source, walking home off the loop until disposed.
func DirectoriesSource(h *Headless) (uireg.PickerSource, registry.Disposable) {
	return directoriesSource(h.host, h.sh)
}

// PaneLead is the colour a tab's lead mark has: its project's, with more than one project.
func PaneLead(h *Headless, v layout.ViewRef) (render.Color, bool) { return h.sh.layout.panes.Lead(v) }

// PaneTip is a tab's tooltip.
func PaneTip(h *Headless, v layout.ViewRef) string { return h.sh.layout.panes.Tip(v) }
