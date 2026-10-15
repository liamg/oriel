package app

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// How long each kind of toast stays (errors.md); 0 stays until it's dismissed or answered.
func toastTTL(n core.Notification) time.Duration {
	switch {
	case n.Severity == core.SevInfo:
		return 3 * time.Second
	case n.Severity == core.SevWarning, n.Severity == core.SevError && n.Key:
		return 8 * time.Second
	}
	return 0
}

const maxToasts = 4

var (
	tStatusBg  = theme.Declare("statusline.bg", "bg.surface")
	tStatusFg  = theme.Declare("statusline.fg", "fg.muted")
	tNoticeInf = theme.Declare("notice.info", "status.working")
)

func toneOf(c *ui.Ctx, s core.Severity) color.NRGBA {
	switch s {
	case core.SevError:
		return c.Theme().C(theme.Error)
	case core.SevWarning:
		return c.Theme().C(theme.Warn)
	case core.SevQuestion:
		return c.Theme().C(theme.Accent)
	}
	return c.Theme().C(tNoticeInf)
}

func titleOf(n core.Notification) string {
	if n.Title != "" {
		return n.Title
	}
	return map[core.Severity]string{core.SevInfo: "Info", core.SevWarning: "Warning", core.SevError: "Error", core.SevQuestion: "Question"}[n.Severity]
}

// notices is every notice's surfaces: toasts, the status line's counts, the log panel, :Events, and the crash
// report offered on launch. Each is a view, so a plugin can replace any of them.
type notices struct {
	disposer
	sh      *shell
	h       plugin.Host
	hovered map[uint64]time.Time // when each toast was last under the pointer
	panel   *logPanel
	crashes []Crash
	modal   bool
	events  string // :Events filter
}

func (n *notices) Manifest() plugin.Manifest { return plugin.Manifest{ID: "notices"} }

func (n *notices) Activate(_ context.Context, h plugin.Host) error {
	n.h, n.hovered = h, map[uint64]time.Time{}
	n.panel = newLogPanel(n.sh, h)
	cfg := n.sh.cfg
	n.add(h.OnFault(func(f registry.Fault) {
		cfg.Store.Apply(core.Notified{Notice: faultNotice(f, cfg.Logs, cfg.ConfigDir)})
	}))
	n.add(h.OnFailure(func(f registry.Failure) {
		if f.InARow < registry.FailLimit { // the fault that turns it off says so, on the same row
			cfg.Store.Apply(core.Notified{Notice: failureNotice(f, cfg.Logs, cfg.ConfigDir)})
		}
	}))
	if n.sh.cfg.CrashDir != "" {
		n.crashes = PendingCrashes(n.sh.cfg.CrashDir)
		n.modal = len(n.crashes) > 0
	}
	str := func(a registry.Args, k string) string { s, _ := a[k].(string); return s }
	openLog := func(ctx registry.Ctx, a registry.Args) error {
		if seq, ok := argSeq(a); ok {
			n.panel.reveal(seq)
		}
		if s := str(a, "severity"); s != "" {
			n.panel.only(core.Severity(s))
		}
		return h.Run(ctx, "view.open", registry.Args{"id": "log", "place": "dock", "edge": "bottom", "title": "Log", "size": 360})
	}
	for _, c := range []registry.Command{
		{ID: "Log", Title: "Open the log, or set its level (:Log level debug)", Run: func(ctx registry.Ctx, a registry.Args) error {
			lv := str(a, "level")
			if f := strings.Fields(str(a, "args")); len(f) == 2 && f[0] == "level" {
				lv = f[1]
			}
			if lv == "" {
				return openLog(ctx, a)
			}
			l, ok := ParseLevel(lv)
			if !ok {
				return fmt.Errorf("no log level %q: debug, info, warn or error", lv)
			}
			n.sh.cfg.Logs.Level.Set(l)
			return nil
		}},
		{ID: "messages", Title: "Open the log", Run: openLog},
		{ID: "log.open", Title: "Open the log at an entry", Run: openLog},
		{ID: "log.copy", Title: "Copy the open log entry, or what the log shows", Run: func(registry.Ctx, registry.Args) error {
			n.sh.desktop.SetClipboard(n.panel.copyText())
			return nil
		}},
		{ID: "log.copy_report", Title: "Copy a report for a bug", Run: func(registry.Ctx, registry.Args) error {
			n.sh.desktop.SetClipboard(n.sh.report(""))
			return nil
		}},
		{ID: "Events", Title: "Show the live core event stream", Run: func(ctx registry.Ctx, _ registry.Args) error {
			return h.Run(ctx, "view.open", registry.Args{"id": "events", "place": "dock", "edge": "bottom", "title": "Events"})
		}},
		{ID: "notice.dismiss", Title: "Dismiss a notice's toast, by {seq} or every one with {id}", Run: func(_ registry.Ctx, a registry.Args) error {
			seq, ok := argSeq(a)
			if id := a.String("id"); !ok && id != "" {
				h.Dismiss(id)
				return nil
			}
			if !ok {
				return fmt.Errorf("notice.dismiss needs a seq or an id")
			}
			return n.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: seq})
		}},
		{ID: "contribution.turn_on", Title: "Turn a contribution that was turned off back on", Run: func(_ registry.Ctx, a registry.Args) error {
			kind, id := str(a, "kind"), str(a, "id")
			h.TurnOn(kind, id)
			h.Notify(core.Notice{ID: "turned-on." + kind + "." + id, Severity: core.SevInfo, Source: "core", Text: fmt.Sprintf("%s %s is back on", kind, id)})
			return nil
		}},
	} {
		n.add(h.Command(c))
	}
	n.add(h.View(NotificationsView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "toasts", Fn: n.toasts}
	})))
	n.add(h.View("statusline.left.50-counts", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "status", Fn: n.status}
	})))
	n.add(h.Contribute(registry.Indicator{Name: "logs", Slot: registry.SlotHUD, Order: 50, Span: 2, Title: "Logs", Help: hudHelp + "#logs",
		Note: func() string { return "tail" }, Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "logs", Fn: n.logsPanel}, nil }}))
	n.add(h.View("log", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "log", Fn: n.panel.build}
	})))
	n.add(h.View("events", registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "events", Fn: n.eventsView}
	})))
	n.add(h.View(ModalView, registry.ViewFunc(func(registry.ViewCtx) ui.Node {
		return &ui.Func{Key: "crash", Fn: n.crashModal}
	})))
	return nil
}

func argSeq(a registry.Args) (uint64, bool) {
	switch v := a["seq"].(type) {
	case uint64:
		return v, true
	case int:
		return uint64(v), v > 0
	case float64:
		return uint64(v), v > 0
	}
	return 0, false
}

// toasts shows the notices still due, at the top right: no errors from a session that isn't in front.
func (n *notices) toasts(c *ui.Ctx) {
	st := n.h.State()
	now := c.Now()
	var due []core.Notification
	for _, row := range st.Notifications {
		if row.Dismissed || row.Severity == core.SevError && row.Session != "" && row.Session != st.Focus {
			continue
		}
		if ttl := toastTTL(row); ttl > 0 {
			end := row.Time.Add(ttl)
			if h, ok := n.hovered[row.Seq]; ok && h.Add(ttl).After(end) {
				end = h.Add(ttl)
			}
			if !now.Before(end) {
				continue
			}
			c.WakeAt(end)
		}
		due = append(due, row)
	}
	if len(due) > maxToasts {
		due = due[len(due)-maxToasts:]
	}
	for seq := range n.hovered {
		if !slices.ContainsFunc(due, func(r core.Notification) bool { return r.Seq == seq }) {
			delete(n.hovered, seq)
		}
	}
	if len(due) == 0 {
		return
	}
	corner := render.Point{X: c.Size().X - 16, Y: 12}
	c.Column("toasts", ui.Style{Gap: 8, Z: ui.LayerToast, Float: &ui.Float{Point: &corner, Self: render.Point{X: 1}}, Role: ui.RoleGroup, Label: "Notifications"}, func(c *ui.Ctx) {
		for _, row := range due {
			n.toast(c, row, now)
		}
	})
}

func (n *notices) toast(c *ui.Ctx, row core.Notification, now time.Time) {
	o := widget.NoticeCardOpts{Title: titleOf(row), Source: row.Source, Body: row.Text, Tone: toneOf(c, row.Severity), Help: "errors#toasts"}
	if row.Session != "" {
		o.Source += " · " + string(row.Session)
	}
	if ttl := toastTTL(row); ttl > 0 {
		o.Age = fmt.Sprintf("%ds", int(now.Sub(row.Time).Seconds()))
	} else {
		o.Close = true
	}
	if row.Count > 1 {
		o.Hint = fmt.Sprintf("×%d", row.Count)
	}
	for _, a := range row.Actions {
		o.Actions = append(o.Actions, a.Title)
	}
	details := -1
	if row.Severity == core.SevError {
		details = len(o.Actions)
		o.Actions = append(o.Actions, "Details")
		if len(script.ParseTrace(row.Detail)) > 0 {
			o.Actions[details] = "Show trace"
		}
	}
	res := widget.NoticeCard(c, row.Seq, o)
	if res.Hovered {
		n.hovered[row.Seq] = now
	}
	ctx := context.Background()
	switch {
	case res.Action >= 0 && res.Action == details:
		n.h.Run(ctx, "log.open", registry.Args{"seq": row.Seq})
		n.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: row.Seq})
	case res.Action >= 0:
		a := row.Actions[res.Action]
		if err := n.h.Run(ctx, a.Command, registry.Args(a.Args)); err != nil {
			n.h.Notify(core.Notice{ID: "action.failed", Severity: core.SevError, Source: "core", Text: fmt.Sprintf("%s: %v", a.Title, err)})
		}
		n.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: row.Seq})
	case res.Closed:
		n.sh.cfg.Store.Apply(core.NoticeDismissed{Seq: row.Seq})
	}
}

// status is the status line's ✕/⚠ counts: the focused session's and Oriel's. A click opens the log there.
func (n *notices) status(c *ui.Ctx) {
	st := n.h.State()
	errs, warns := st.Counts(st.Focus)
	c.Row("status", ui.Style{H: ui.Flex(1), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		count := func(key, glyph string, k int, sev core.Severity, tone color.NRGBA) {
			if k == 0 {
				tone = c.Theme().C(tStatusFg)
			}
			if widget.Clickable(c, key, ui.Style{Dir: ui.Row, Gap: 5, Cross: ui.Center, Help: "errors#log", Label: fmt.Sprintf("%d %ss", k, sev)}, func(c *ui.Ctx) {
				c.Text("glyph", glyph, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: tone})
				c.Text("n", fmt.Sprint(k), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: c.Theme().C(tStatusFg)})
			}).Clicked {
				n.h.Run(context.Background(), "log.open", registry.Args{"severity": string(sev)})
			}
		}
		count("errors", "✕", errs, core.SevError, c.Theme().C(theme.Error))
		count("warnings", "⚠", warns, core.SevWarning, c.Theme().C(theme.Warn))
	})
}

// eventsView is :Events: the last events applied, newest last, filtered by type or scope.
func (n *notices) eventsView(c *ui.Ctx) {
	mono := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5}, Color: c.Theme().C(theme.FgMuted)}
	c.Column("events", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		widget.Input(c, "filter", &n.events, widget.InputOpts{Label: "Filter events", Placeholder: "filter · type or session", Width: ui.Px(260)})
		evs := n.sh.events.list()
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			q := strings.ToLower(n.events)
			for _, ev := range evs {
				line := fmt.Sprintf("%6d  %-22s %-14s %s", ev.Seq, ev.Type, orDash(string(ev.Scope)), ev.JSON)
				if q != "" && !strings.Contains(strings.ToLower(line), q) {
					continue
				}
				c.Text(ev.Seq, line, mono)
			}
		})
	})
}

// expandTabs is s with each tab as four spaces: text runs have no tab stops.
func expandTabs(s string) string { return strings.ReplaceAll(s, "\t", "    ") }

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// crashModal offers the newest crash report from a previous run (38-crash): Copy report, open an issue, delete.
func (n *notices) crashModal(c *ui.Ctx) {
	if !n.modal || len(n.crashes) == 0 {
		return
	}
	cr := n.crashes[0]
	title := "Oriel quit unexpectedly last time"
	widget.Modal(c, "crash", &n.modal, title, widget.ModalOpts{Width: 720, Help: "errors#crash"}, func(c *ui.Ctx) {
		muted := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5}, Color: c.Theme().C(theme.FgMuted)}
		c.Text("when", fmt.Sprintf("%s · %s", cr.Time.Local().Format("2006-01-02 15:04"), cr.Version),
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: c.Theme().C(theme.FgFaint)})
		widget.Wrap(c, "about", "The crash report below stays on this machine; nothing has been sent. If you'd like it fixed, copy it into a new issue. "+
			"Secrets and tokens are already redacted.", 680, muted)
		c.Column("report", ui.Style{W: ui.Flex(1), H: ui.Px(250), Pad: ui.Pad(12), Gap: 3, Fill: c.Theme().C(theme.BgBase), Stroke: c.Theme().C(theme.Line),
			Border: 1, Radius: 6, ScrollY: true}, func(c *ui.Ctx) {
			for i, ln := range strings.Split(cr.Report, "\n") {
				col := c.Theme().C(theme.FgMuted)
				switch {
				case strings.HasPrefix(ln, "panic:"):
					col = c.Theme().C(theme.Error)
				case strings.HasPrefix(ln, "github.com/"):
					col = c.Theme().C(theme.Accent)
				}
				c.Text(i, expandTabs(ln), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11}, Color: col})
			}
		})
		c.Row("actions", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.Button(c, "copy", "Copy report", widget.ButtonOpts{Primary: true}) {
				n.sh.desktop.SetClipboard(cr.Report)
			}
			if widget.Link(c, "issue", "Open a GitHub issue ↗", "errors#crash") {
				n.sh.desktop.SetClipboard(cr.Report)
				if err := n.sh.desktop.OpenURL(IssueURL); err != nil {
					n.h.Notify(core.Notice{ID: "crash.open-issue", Severity: core.SevError, Source: "core", Text: err.Error()})
				}
			}
			c.Text("url", strings.TrimPrefix(IssueURL, "https://"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: c.Theme().C(theme.FgFaint)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "delete", "Delete report", widget.ButtonOpts{}) {
				os.Remove(cr.Path)
				n.crashes = n.crashes[1:]
				n.modal = len(n.crashes) > 0
			}
			if widget.Button(c, "close", "Close", widget.ButtonOpts{}) {
				n.modal = false
			}
		})
	})
}
