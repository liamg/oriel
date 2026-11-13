package shell

import (
	"io"
	"log/slog"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui/layout"
)

// What the package's outside tests read and set of its plugins.

type (
	Picker  = picker
	Omnibox = omnibox
	Group   = group
)

const (
	PickerMax = pickerMax
	RankAway  = rankAway
)

// Source is the id of the source the group's results are of.
func (g group) Source() string { return g.src.ID }

// Items are the group's results, best first.
func (g group) Items() []uireg.PickerItem {
	out := make([]uireg.PickerItem, len(g.hits))
	for i, h := range g.hits {
		out[i] = h.item
	}
	return out
}

// RankSource is s's items for q, best first, at most limit of them, as a source of h's plugins ranks them.
func RankSource(h *Headless, s uireg.PickerSource, q string, limit int) (group, bool) {
	return rankSource(h.sh.host("test"), s, q, limit)
}

// SourceLoading says s is still loading its items.
func SourceLoading(s uireg.PickerSource) bool { return sourceLoading(s) }

func (o *omnibox) Results(moving bool) []group { g, _ := o.results(moving); return g }
func (o *omnibox) Open() bool                  { return o.open }
func (o *omnibox) Close()                      { o.open = false }

func (p *picker) Open() bool     { return p.open }
func (p *picker) Source() string { return p.source }
func (p *picker) Query() string  { return p.query }
func (p *picker) Selected() int  { return p.sel }

// SetKeys has the window's keys go through r, as its loop's do.
func SetKeys(h *Headless, r *registry.Resolver) { h.sh.keys = r }

// What fork F1's moved tests read and set of the shell.

func (f *windowFocus) Now() (bool, <-chan struct{}) { return f.now() }
func (f *windowFocus) Set(on bool) bool             { return f.set(on) }

type Help = helpView

func (v *helpView) Open() bool   { return v.open }
func (v *helpView) Page() string { return v.cur }
func (v *helpView) Close()       { v.open = false }
func (v *helpView) EndHelpMode() { v.mode = false }

// LastMove is the last of a run of pointer moves waiting from ev, and the event read after the run.
func LastMove(ev render.Event, events <-chan render.Event) (render.Event, render.Event) {
	return lastMove(ev, events)
}

type Overview = overview

const PanelBudget = panelBudget

func (o *overview) SetClock(now func() time.Time) { o.now = now }

// Done says every card's header and panels have been computed.
func (o *overview) Done() bool {
	for _, cd := range o.cards {
		if cd.git && cd.header == nil {
			return false
		}
		for _, p := range cd.panels {
			if !p.done {
				return false
			}
		}
	}
	return len(o.cards) > 0
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

// SetLogScope shows the log panel's records of scope.
func SetLogScope(h *Headless, scope int) { logPanelOf(h).scope = scope }

// LogOpenRow is the log panel's expanded row's key; "" with none.
func LogOpenRow(h *Headless) string { return logPanelOf(h).open }

// FoldLogRow folds the log panel's expanded row.
func FoldLogRow(h *Headless) { logPanelOf(h).open = "" }

// ToastTTL is how long a notification's toast stays; 0 until it's answered.
func ToastTTL(n core.Notification) time.Duration { return toastTTL(n) }

// Draining has the window show a quit waiting until deadline on busy, mid-turn, and the scheduled jobs.
func Draining(h *Headless, deadline time.Time, busy []core.SessionState, jobs []host.Job) {
	h.sh.draining.Store(&quitDrain{deadline: deadline, focus: true, polled: true, busy: busy, jobs: jobs})
}

// KeepRecents keeps the window's recent directories in path, as Run does in its state directory.
func KeepRecents(h *Headless, path string) { h.sh.recents = &recents{path: path} }

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

// RailEntryActive says e's panel is showing for project, as its rail entry is lit.
func RailEntryActive(h plugin.Host, e uireg.RailEntry, project string) bool {
	return active(h, e, project)
}

// Selected is the omnibox's selected row.
func (o *omnibox) Selected() int { return o.sel }
