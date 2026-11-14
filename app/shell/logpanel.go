package shell

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"log/slog"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Log panel scopes: Oriel, Session · {name} (the focused one) and All.
const (
	scopeOriel = iota
	scopeSession
	scopeAll
)

var levelOrder = []slog.Level{slog.LevelError, slog.LevelWarn, slog.LevelInfo, slog.LevelDebug}

// logPanel is :Log: the notification history and log records, newest last, with scopes, filters and search.
type logPanel struct {
	sh       *shell
	h        plugin.Host
	scope    int
	show     map[slog.Level]bool
	filter   string
	open     string // the expanded row's key
	goFrames bool
	follow   bool    // scroll the open row into view once it's laid out
	width    float32 // the rows' width as last laid out
	srcs     kit.FileTexts
	cache    logCache
	shown    *kit.WhileShown // the ring's writes ask for frames while the panel is drawn
}

// logKey is what the panel's rows are made from: the state, the ring's writes, and the scope.
type logKey struct {
	seq, writes uint64
	scope       int
}

// logShown is what the rows shown are picked by: the rows, the levels shown, and the search.
type logShown struct {
	key    logKey
	levels uint8
	filter string
}

// logCache is the panel's rows as last made: rows reads them, and they're made again only when their key moves.
type logCache struct {
	made    bool
	key     logKey
	all     []logRow
	counts  map[slog.Level]int // all's, by bucket
	shownBy logShown
	shown   []logRow
}

func newLogPanel(sh *shell, h plugin.Host) *logPanel {
	return &logPanel{sh: sh, h: h, scope: scopeAll, srcs: kit.FileTexts{},
		show:  map[slog.Level]bool{slog.LevelError: true, slog.LevelWarn: true, slog.LevelInfo: true},
		shown: &kit.WhileShown{Want: sh.cfg.Logs.Showing, Focus: h}}
}

type logRow struct {
	key     string
	time    time.Time
	level   slog.Level
	session core.SessionID
	source  string
	text    string
	count   int
	n       *core.Notification
	rec     *logs.Record
}

func levelOf(s core.Severity) slog.Level {
	switch s {
	case core.SevError:
		return slog.LevelError
	case core.SevWarning:
		return slog.LevelWarn
	}
	return slog.LevelInfo
}

// reveal expands the history row seq and widens the filters so it shows.
func (p *logPanel) reveal(seq uint64) {
	p.open = fmt.Sprint("n", seq)
	p.filter, p.follow = "", true
	for _, row := range p.h.State().Notifications {
		if row.Seq == seq {
			p.show[levelOf(row.Severity)] = true
			if row.Session != "" && p.scope == scopeOriel {
				p.scope = scopeAll
			}
		}
	}
}

// only shows one severity.
func (p *logPanel) only(s core.Severity) {
	for _, lv := range levelOrder {
		p.show[lv] = lv == levelOf(s)
	}
}

// rows are what the panel lists before its severity filter and search: the history, and records that
// aren't a notice already in it, in the scope, oldest first. They're shared: callers mustn't change them.
func (p *logPanel) rows(st core.State) []logRow {
	k := logKey{seq: st.Seq, writes: p.sh.cfg.Logs.writes.Load(), scope: p.scope}
	if p.cache.made && p.cache.key == k {
		return p.cache.all
	}
	var out []logRow
	inHistory := map[uint64]bool{}
	for i := range st.Notifications {
		n := &st.Notifications[i]
		inHistory[n.Seq] = true
		out = append(out, logRow{key: "n" + strconv.FormatUint(n.Seq, 10), time: n.Time, level: levelOf(n.Severity), session: n.Session,
			source: n.Source, text: n.Text, count: n.Count, n: n})
	}
	for i, r := range p.sh.cfg.Logs.Ring.Records() {
		if r.Notice != 0 && inHistory[r.Notice] {
			continue
		}
		r := r
		out = append(out, logRow{key: "r" + strconv.FormatInt(r.Time.UnixNano(), 10) + "." + strconv.Itoa(i), time: r.Time, level: r.Level,
			session: r.Session, source: r.Source, text: r.Msg, count: 1, rec: &r})
	}
	out = slices.DeleteFunc(out, func(r logRow) bool {
		switch p.scope {
		case scopeOriel:
			return r.session != ""
		case scopeSession:
			return r.session == "" || r.session != st.Focus
		}
		return false
	})
	slices.SortStableFunc(out, func(a, b logRow) int { return a.time.Compare(b.time) })
	counts := map[slog.Level]int{}
	for _, r := range out {
		counts[bucket(r.level)]++
	}
	p.cache = logCache{made: true, key: k, all: slices.Clip(out), counts: counts}
	return p.cache.all
}

func bucket(lv slog.Level) slog.Level {
	switch {
	case lv >= slog.LevelError:
		return slog.LevelError
	case lv >= slog.LevelWarn:
		return slog.LevelWarn
	case lv >= slog.LevelInfo:
		return slog.LevelInfo
	}
	return slog.LevelDebug
}

// match is the search: words match the text, "source:x" and "session:x" their fields.
func (p *logPanel) match(r logRow) bool {
	for _, w := range strings.Fields(strings.ToLower(p.filter)) {
		k, v, ok := strings.Cut(w, ":")
		switch {
		case ok && k == "source":
			if !strings.Contains(strings.ToLower(r.source), v) {
				return false
			}
		case ok && k == "session":
			if !strings.Contains(strings.ToLower(string(r.session)), v) {
				return false
			}
		case !strings.Contains(strings.ToLower(r.text+" "+r.source), w):
			return false
		}
	}
	return true
}

// visible are the rows the levels shown and the search let through. They're shared: callers mustn't change them.
func (p *logPanel) visible(st core.State) []logRow {
	all := p.rows(st)
	by := logShown{key: p.cache.key, filter: p.filter}
	for i, lv := range levelOrder {
		if p.show[lv] {
			by.levels |= 1 << i
		}
	}
	if p.cache.shown != nil && p.cache.shownBy == by {
		return p.cache.shown
	}
	shown := []logRow{}
	for _, r := range all {
		if p.show[bucket(r.level)] && p.match(r) {
			shown = append(shown, r)
		}
	}
	p.cache.shownBy, p.cache.shown = by, slices.Clip(shown)
	return shown
}

func (r logRow) plain() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s", r.time.Format("15:04:05"), levelName(r.level), where(r), r.text)
	if r.count > 1 {
		fmt.Fprintf(&b, " ×%d", r.count)
	}
	if r.n != nil {
		if r.n.Location != "" {
			fmt.Fprintf(&b, "\nat %s", r.n.Location)
		}
		if r.n.Detail != "" {
			fmt.Fprintf(&b, "\n%s", strings.TrimRight(r.n.Detail, "\n"))
		}
	}
	if r.rec != nil {
		for _, a := range r.rec.Attrs {
			fmt.Fprintf(&b, "\n  %s=%s", a.Key, a.Value)
		}
	}
	return b.String()
}

// copyText is the open entry as plain text, or every row the panel shows.
func (p *logPanel) copyText() string {
	rows := p.visible(p.h.State())
	var out []string
	for _, r := range rows {
		if r.key == p.open {
			return r.plain()
		}
		out = append(out, r.plain())
	}
	return strings.Join(out, "\n")
}

func levelName(l slog.Level) string {
	switch bucket(l) {
	case slog.LevelError:
		return "ERROR"
	case slog.LevelWarn:
		return "WARN"
	case slog.LevelInfo:
		return "INFO"
	}
	return "DEBUG"
}

func where(r logRow) string {
	sess := "oriel"
	if r.session != "" {
		sess = string(r.session)
	}
	if r.source == "" {
		return sess
	}
	return sess + " › " + r.source
}

var (
	tLogRowError = theme.DeclareOn("log.row.error", "alpha(error, 6%)", "bg.surface")
	tLogTime     = theme.Declare("log.time", "fg.ghost")
	tLogSource   = theme.Declare("log.source", "accent")
)

func (p *logPanel) build(c *ui.Ctx) {
	p.shown.Keep()
	st := p.h.State()
	rows := p.visible(st)
	counts := p.cache.counts
	c.Column("log", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			p.scopes(c, st)
			for _, lv := range levelOrder {
				p.levelChip(c, lv, counts[lv])
			}
			widget.Input(c, "filter", &p.filter, widget.InputOpts{Label: "Filter the log", Placeholder: "filter · source:init.lua", Width: ui.Px(220)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "copy", "Copy", widget.ButtonOpts{Help: "errors#log"}) {
				kit.CopyText(p.h, p.copyText())
			}
			if widget.Button(c, "report", "Copy report", widget.ButtonOpts{Help: "errors#log"}) {
				kit.CopyText(p.h, p.sh.report(""))
			}
		})
		rows := rows[max(0, len(rows)-500):]
		rowsID, reveal := c.Next("rows"), -1
		if r, ok := c.Rect(rowsID); ok && r.Dx() > 0 {
			p.width = r.Dx()
		}
		if p.follow {
			view, ok1 := c.Rect(rowsID)
			row, ok2 := c.Rect(ui.Child(rowsID, p.open))
			if ok1 && ok2 {
				c.ScrollTo(rowsID, render.Point{Y: c.Scroll(rowsID).Y + row.Min.Y - view.Min.Y})
				p.follow = false
			} else {
				reveal = slices.IndexFunc(rows, func(r logRow) bool { return r.key == p.open }) // built, to scroll to next frame
			}
		}
		c.Items("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, len(rows), ui.ItemsOpts{Estimate: 23, Reveal: reveal,
			Key: func(i int) any { return rows[i].key }}, func(c *ui.Ctx, i int) { p.row(c, rows[i]) })
	})
}

func (p *logPanel) scopes(c *ui.Ctx, st core.State) {
	labels := []string{"Oriel", "Session", "All"}
	if st.Focus != "" {
		labels[1] = "Session · " + string(st.Focus)
	}
	widget.Segmented(c, "scope", kit.Upper(labels), &p.scope, widget.SegmentedOpts{Help: "errors#log", Label: "Log scope"})
}

func (p *logPanel) levelChip(c *ui.Ctx, lv slog.Level, n int) {
	tone := map[slog.Level]theme.TokenID{slog.LevelError: theme.Error, slog.LevelWarn: theme.FgMuted, slog.LevelInfo: theme.FgMuted, slog.LevelDebug: theme.FgGhost}[lv]
	label := levelName(lv)
	if n > 0 && lv >= slog.LevelWarn {
		label = fmt.Sprintf("%s %d", label, n)
	}
	col := c.Theme().C(tone)
	if !p.show[lv] {
		col = c.Theme().C(theme.FgGhost)
	}
	if widget.Clickable(c, lv, ui.Style{Help: "errors#log", Label: "Show " + levelName(lv), Selected: p.show[lv]}, func(c *ui.Ctx) {
		widget.Chip(c, "chip", label, col, "errors#log")
	}).Clicked {
		p.show[lv] = !p.show[lv]
	}
}

func (p *logPanel) row(c *ui.Ctx, r logRow) {
	th := c.Theme()
	open := p.open == r.key
	isErr := bucket(r.level) == slog.LevelError
	fill := color.NRGBA{}
	if isErr {
		fill = th.C(tLogRowError)
	}
	levelCol := map[slog.Level]theme.TokenID{slog.LevelError: theme.Error, slog.LevelWarn: theme.Warn, slog.LevelInfo: theme.FgFaint, slog.LevelDebug: theme.FgGhost}[bucket(r.level)]
	c.Column(r.key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		line := c.Next("line")
		in := widget.Clickable(c, "line", ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(23), Pad: ui.Insets{R: 12}, Gap: 12, Cross: ui.Center, Fill: fill,
			Help: "errors#log", Expanded: &open, Label: r.text}, func(c *ui.Ctx) {
			edge := color.NRGBA{}
			if isErr {
				edge = th.C(theme.Error)
			}
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
			c.Text("time", r.time.Local().Format("15:04:05"), kit.Mono(11, typeset.Normal, th.C(tLogTime)))
			c.Box("level", ui.Style{W: ui.Px(44)}, func(c *ui.Ctx) {
				c.Text(nil, levelName(r.level), kit.Mono(10, typeset.SemiBold, th.C(levelCol)))
			})
			c.Row("where", ui.Style{W: ui.Px(230), Gap: 6, Clip: true}, func(c *ui.Ctx) {
				sess := "oriel"
				if r.session != "" {
					sess = string(r.session)
				}
				c.Text("session", sess, kit.Mono(11, typeset.Normal, th.C(theme.FgFaint)))
				if r.source != "" {
					c.Text("sep", "›", kit.Mono(11, typeset.Normal, th.C(theme.FgGhost)))
					c.Text("source", r.source, kit.Mono(11, typeset.Normal, th.C(tLogSource)))
				}
			})
			c.Row("text", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				c.Text(nil, kit.FirstLine(r.text), kit.Mono(11.5, typeset.Normal, th.C(theme.Fg)))
			})
			if r.count > 1 {
				c.Row("count", ui.Style{Pad: ui.PadXY(6, 1), Radius: 8, Fill: th.C(theme.BgOverlay)}, func(c *ui.Ctx) {
					c.Text(nil, fmt.Sprintf("×%d", r.count), kit.Mono(9.5, typeset.Normal, th.C(theme.FgFaint)))
				})
			}
		})
		if in.Clicked {
			if open = !open; open {
				p.open = r.key
			} else {
				p.open = ""
			}
		}
		if widget.ContextMenu(c, "menu", line, []widget.MenuItem{{Label: "Copy", Help: widget.ContextMenuHelp}}).Chosen == 0 {
			kit.CopyText(p.h, r.plain())
		}
		if open {
			p.detail(c, r)
		}
	})
}

// detail is an expanded entry: its trace and the source around the failing line, or its stderr, and its actions.
func (p *logPanel) detail(c *ui.Ctx, r logRow) {
	th := c.Theme()
	var detail, loc string
	var actions []core.Action
	if r.n != nil {
		detail, loc, actions = r.n.Detail, r.n.Location, r.n.Actions
	} else if r.rec != nil {
		for _, a := range r.rec.Attrs {
			switch a.Key {
			case "detail":
				detail = a.Value
			case "at":
				loc = a.Value
			}
		}
	}
	frames := script.ParseTrace(detail)
	if loc == "" {
		if f, ok := failingFrame(frames); ok {
			loc = f.Loc()
		}
	}
	var src *kit.HighlightedExcerpt
	if loc != "" {
		src = p.source(loc)
	}
	// The text wraps to the left column: the row less its padding, beside the source when that shows.
	width := cmp.Or(p.width, 900) - 86 - 12 - 16
	if src != nil {
		width = (width - 16) / 2.3
	}
	c.Row("detail", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 86, T: 10, R: 12, B: 12}, Gap: 16}, func(c *ui.Ctx) {
		c.Column("left", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			if text, font := kit.ExpandTabs(r.text), kit.Mono(11.5, typeset.Normal, th.C(theme.Fg)); p.cut(c, r, text, font.Font) {
				widget.Wrap(c, "text", text, width, font)
			}
			switch {
			case len(frames) > 0:
				c.Text("head", "TRACEBACK", kit.Mono(9.5, typeset.SemiBold, th.C(theme.FgFaint)))
				p.frames(c, frames, loc)
			case detail != "":
				c.Text("head", "OUTPUT", kit.Mono(9.5, typeset.SemiBold, th.C(theme.FgFaint)))
				for i, ln := range strings.Split(strings.TrimRight(detail, "\n"), "\n") {
					col := th.C(theme.FgMuted)
					if hasFileRef(ln) {
						col = th.C(tLogSource)
					}
					widget.Wrap(c, i, kit.ExpandTabs(ln), width, kit.Mono(11, typeset.Normal, col))
				}
			case r.rec != nil:
				for i, a := range r.rec.Attrs {
					widget.Wrap(c, i, kit.ExpandTabs(a.Key+" = "+a.Value), width, kit.Mono(11, typeset.Normal, th.C(theme.FgMuted)))
				}
			}
			if len(actions) > 0 {
				c.Row("actions", ui.Style{Gap: 8, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
					for i, a := range actions {
						if widget.Button(c, i, a.Title, widget.ButtonOpts{Primary: i == 0, Help: "errors#log"}) {
							if err := p.h.Run(context.Background(), a.Command, registry.Args(a.Args)); err != nil {
								p.h.Notify(core.Notice{ID: "action.failed", Severity: core.SevError, AutoClear: true, Source: "core", Text: fmt.Sprintf("%s: %v", a.Title, err)})
							}
						}
					}
				})
			}
		})
		if src != nil {
			c.Column("source", ui.Style{W: ui.Flex(1.3)}, func(c *ui.Ctx) { src.Build(c, r.n) })
		}
	})
}

// cut says whether r's text doesn't fit on its folded row: the row less its edge, time, level, where, count, gaps
// and padding.
func (p *logPanel) cut(c *ui.Ctx, r logRow, text string, f typeset.FontSpec) bool {
	if strings.Contains(text, "\n") {
		return true
	}
	sh := c.Shaper()
	room := cmp.Or(p.width, 900) - 2 - 44 - 230 - 4*12 - 12 - sh.Shape("00:00:00", kit.Mono(11, typeset.Normal, color.NRGBA{}).Font).Advance
	if r.count > 1 {
		room -= 12 + 12 + sh.Shape(fmt.Sprintf("×%d", r.count), kit.Mono(9.5, typeset.Normal, color.NRGBA{}).Font).Advance
	}
	return sh.Shape(text, f).Advance > room
}

func hasFileRef(s string) bool {
	for _, f := range strings.Fields(s) {
		if file, line := script.SplitLoc(strings.TrimRight(f, ":,")); line > 0 && strings.Contains(file, ".") {
			return true
		}
	}
	return false
}

// frames lists a trace, one frame per line, each jumping to its source. In a Lua trace, Go frames fold
// behind "show".
func (p *logPanel) frames(c *ui.Ctx, frames []Frame, failing string) {
	th := c.Theme()
	lua := slices.ContainsFunc(frames, func(f Frame) bool { return !f.Go })
	hidden := 0
	c.Column("frames", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		for i, f := range frames {
			if lua && f.Go && !p.goFrames {
				hidden++
				continue
			}
			fill := color.NRGBA{}
			if f.Loc() == failing {
				fill = th.C(theme.BgRaised)
			}
			in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(19), Pad: ui.PadXY(6, 0), Gap: 12, Cross: ui.Center, Fill: fill,
				Radius: 3, Help: "errors#log", Label: f.Loc()}, func(c *ui.Ctx) {
				c.Box("loc", ui.Style{W: ui.Px(180), Clip: true}, func(c *ui.Ctx) {
					c.Text(nil, shortLoc(f), kit.Mono(11, typeset.Normal, th.C(tLogSource)))
				})
				c.Row("fn", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text(nil, f.Fn, kit.Mono(11, typeset.Normal, th.C(theme.FgMuted)))
				})
			})
			if in.Clicked && f.Line > 0 {
				if err := p.h.Run(context.Background(), "editor.open", registry.Args{"path": p.resolve(f.File), "line": f.Line}); err != nil {
					p.h.Notify(core.Notice{ID: "log.open-frame", Severity: core.SevWarning, Source: "core", AutoClear: true, Text: err.Error()})
				}
			}
		}
		if hidden > 0 {
			if widget.Clickable(c, "gofr", ui.Style{Dir: ui.Row, Pad: ui.PadXY(6, 2), Gap: 12, Help: "errors#log", Label: "Show Go frames"}, func(c *ui.Ctx) {
				c.Text("n", fmt.Sprintf("+%d Go frames", hidden), kit.Mono(11, typeset.Normal, th.C(theme.FgGhost)))
				c.Text("show", "show", kit.Mono(11, typeset.Normal, th.C(theme.FgFaint)))
			}).Clicked {
				p.goFrames = true
			}
		}
	})
}

func shortLoc(f Frame) string {
	if f.Go {
		return filepath.Base(f.File) + fmt.Sprintf(":%d", f.Line)
	}
	return f.Loc()
}

// resolve finds a trace's file: absolute as it is, a config file under the config dir.
func (p *logPanel) resolve(file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(p.sh.cfg.ConfigDir, file)
}

const sourceContext = 2

func (p *logPanel) source(loc string) *kit.HighlightedExcerpt {
	file, line := script.SplitLoc(loc)
	if line == 0 {
		return nil
	}
	path := p.resolve(file)
	src := p.srcs.Of(p.h, path)
	if src.Missing() || src.Src == nil {
		return nil
	}
	f := src.Around(p.h, path, line, sourceContext, sourceContext)
	f.Mark = true
	return f
}
