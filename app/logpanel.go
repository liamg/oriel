package app

import (
	"context"
	"fmt"
	"image/color"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/core/logs"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/script"
	"github.com/liamg/oriel/syntax"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Log panel scopes (errors.md: "Oriel, Session · {name} (the focused one) and All").
const (
	scopeOriel = iota
	scopeSession
	scopeAll
)

var levelOrder = []slog.Level{slog.LevelError, slog.LevelWarn, slog.LevelInfo, slog.LevelDebug}

// logPanel is :Log: the notification history and log records, newest last, with scopes, filters and search.
type logPanel struct {
	sh       *shell
	h        ext.Host
	scope    int
	show     map[slog.Level]bool
	filter   string
	open     string // the expanded row's key
	goFrames bool
	follow   bool // scroll the open row into view once it's laid out
	files    map[string]*sourceFile
}

func newLogPanel(sh *shell, h ext.Host) *logPanel {
	return &logPanel{sh: sh, h: h, scope: scopeAll, files: map[string]*sourceFile{},
		show: map[slog.Level]bool{slog.LevelError: true, slog.LevelWarn: true, slog.LevelInfo: true}}
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
// aren't a notice already in it, in the scope, oldest first.
func (p *logPanel) rows(st core.State) []logRow {
	var out []logRow
	inHistory := map[uint64]bool{}
	for i := range st.Notifications {
		n := &st.Notifications[i]
		inHistory[n.Seq] = true
		out = append(out, logRow{key: fmt.Sprint("n", n.Seq), time: n.Time, level: levelOf(n.Severity), session: n.Session, source: n.Source,
			text: n.Text, count: n.Count, n: n})
	}
	for i, r := range p.sh.cfg.Logs.Ring.Records() {
		if r.Notice != 0 && inHistory[r.Notice] {
			continue
		}
		r := r
		out = append(out, logRow{key: fmt.Sprintf("r%d.%d", r.Time.UnixNano(), i), time: r.Time, level: r.Level, session: r.Session,
			source: r.Source, text: r.Msg, count: 1, rec: &r})
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
	return out
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

func (p *logPanel) visible(st core.State) []logRow {
	return slices.DeleteFunc(p.rows(st), func(r logRow) bool { return !p.show[bucket(r.level)] || !p.match(r) })
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
	tLogFailLine = theme.DeclareOn("log.source.failing", "alpha(error, 8%)", "bg.base")
)

func mono(size float32, w typeset.Weight, c color.NRGBA) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size, Weight: w}, Color: c}
}

func (p *logPanel) build(c *ui.Ctx) {
	st := p.h.State()
	all := p.rows(st)
	counts := map[slog.Level]int{}
	for _, r := range all {
		counts[bucket(r.level)]++
	}
	rows := p.visible(st)
	c.Column("log", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Row("bar", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			p.scopes(c, st)
			for _, lv := range levelOrder {
				p.levelChip(c, lv, counts[lv])
			}
			widget.Input(c, "filter", &p.filter, widget.InputOpts{Label: "Filter the log", Placeholder: "filter · source:init.lua", Width: ui.Px(220)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Button(c, "copy", "Copy", widget.ButtonOpts{Help: "errors#log"}) {
				p.sh.desktop.SetClipboard(p.copyText())
			}
			if widget.Button(c, "report", "Copy report", widget.ButtonOpts{Help: "errors#log"}) {
				p.sh.desktop.SetClipboard(p.sh.report(""))
			}
		})
		rowsID := c.Next("rows")
		if p.follow {
			view, ok1 := c.Rect(rowsID)
			row, ok2 := c.Rect(ui.Child(rowsID, p.open))
			if ok1 && ok2 {
				c.ScrollTo(rowsID, render.Point{Y: c.Scroll(rowsID).Y + row.Min.Y - view.Min.Y})
				p.follow = false
			}
		}
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			for _, r := range rows[max(0, len(rows)-500):] {
				p.row(c, r)
			}
		})
	})
}

func (p *logPanel) scopes(c *ui.Ctx, st core.State) {
	labels := []string{"Oriel", "Session", "All"}
	if st.Focus != "" {
		labels[1] = "Session · " + string(st.Focus)
	}
	th := c.Theme()
	c.Row("scope", ui.Style{Stroke: th.C(theme.LineStrong), Border: 1, Radius: 5, Clip: true, Role: ui.RoleTabList, Label: "Log scope"}, func(c *ui.Ctx) {
		for i, l := range labels {
			id := c.Next(i)
			if c.Interaction(id).Clicked {
				p.scope = i
			}
			fill, fg := color.NRGBA{}, th.C(theme.FgMuted)
			if p.scope == i {
				fill, fg = th.C(theme.AccentBg), th.C(theme.Accent)
			}
			c.Box(i, ui.Style{Pad: ui.PadXY(10, 4), Fill: fill, Interactive: true, Help: "errors#log", Cursor: render.CursorPointer, Role: ui.RoleTab, Selected: p.scope == i,
				Label: l}, func(c *ui.Ctx) {
				c.Text(nil, l, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12}, Color: fg})
			})
		}
	})
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
	id := c.Next(lv)
	if c.Interaction(id).Clicked {
		p.show[lv] = !p.show[lv]
	}
	c.Box(lv, ui.Style{Interactive: true, Help: "errors#log", Cursor: render.CursorPointer, Role: ui.RoleButton, Label: "Show " + levelName(lv), Selected: p.show[lv]}, func(c *ui.Ctx) {
		widget.Chip(c, "chip", label, col, "errors#log")
	})
}

func (p *logPanel) row(c *ui.Ctx, r logRow) {
	th := c.Theme()
	open := p.open == r.key
	id := c.Next(r.key)
	if c.Interaction(id).Clicked {
		if open {
			p.open = ""
		} else {
			p.open = r.key
		}
	}
	isErr := bucket(r.level) == slog.LevelError
	fill := color.NRGBA{}
	if isErr {
		fill = th.C(tLogRowError)
	}
	levelCol := map[slog.Level]theme.TokenID{slog.LevelError: theme.Error, slog.LevelWarn: theme.Warn, slog.LevelInfo: theme.FgFaint, slog.LevelDebug: theme.FgGhost}[bucket(r.level)]
	c.Column(r.key, ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Row("line", ui.Style{W: ui.Flex(1), H: ui.Px(23), Pad: ui.Insets{R: 12}, Gap: 12, Cross: ui.Center, Fill: fill, Interactive: true, Help: "errors#log",
			Cursor: render.CursorPointer, Role: ui.RoleButton, Expanded: &open, Label: r.text}, func(c *ui.Ctx) {
			edge := color.NRGBA{}
			if isErr {
				edge = th.C(theme.Error)
			}
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
			c.Text("time", r.time.Local().Format("15:04:05"), mono(11, typeset.Normal, th.C(tLogTime)))
			c.Box("level", ui.Style{W: ui.Px(44)}, func(c *ui.Ctx) {
				c.Text(nil, levelName(r.level), mono(10, typeset.SemiBold, th.C(levelCol)))
			})
			c.Row("where", ui.Style{W: ui.Px(230), Gap: 6, Clip: true}, func(c *ui.Ctx) {
				sess := "oriel"
				if r.session != "" {
					sess = string(r.session)
				}
				c.Text("session", sess, mono(11, typeset.Normal, th.C(theme.FgFaint)))
				if r.source != "" {
					c.Text("sep", "›", mono(11, typeset.Normal, th.C(theme.FgGhost)))
					c.Text("source", r.source, mono(11, typeset.Normal, th.C(tLogSource)))
				}
			})
			c.Row("text", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
				c.Text(nil, firstLine(r.text), mono(11.5, typeset.Normal, th.C(theme.Fg)))
			})
			if r.count > 1 {
				c.Row("count", ui.Style{Pad: ui.PadXY(6, 1), Radius: 8, Fill: th.C(theme.BgOverlay)}, func(c *ui.Ctx) {
					c.Text(nil, fmt.Sprintf("×%d", r.count), mono(9.5, typeset.Normal, th.C(theme.FgFaint)))
				})
			}
		})
		if open {
			p.detail(c, r)
		}
	})
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(s, "\n")
	return s
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
	c.Row("detail", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 86, T: 10, R: 12, B: 12}, Gap: 16}, func(c *ui.Ctx) {
		c.Column("left", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			switch {
			case len(frames) > 0:
				c.Text("head", "TRACEBACK", mono(9.5, typeset.SemiBold, th.C(theme.FgFaint)))
				p.frames(c, frames, loc)
			case detail != "":
				c.Text("head", "OUTPUT", mono(9.5, typeset.SemiBold, th.C(theme.FgFaint)))
				for i, ln := range strings.Split(strings.TrimRight(detail, "\n"), "\n") {
					col := th.C(theme.FgMuted)
					if hasFileRef(ln) {
						col = th.C(tLogSource)
					}
					c.Text(i, expandTabs(ln), mono(11, typeset.Normal, col))
				}
			case r.rec != nil:
				for i, a := range r.rec.Attrs {
					c.Text(i, expandTabs(a.Key+" = "+a.Value), mono(11, typeset.Normal, th.C(theme.FgMuted)))
				}
			}
			if len(actions) > 0 {
				c.Row("actions", ui.Style{Gap: 8, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
					for i, a := range actions {
						if widget.Button(c, i, a.Title, widget.ButtonOpts{Primary: i == 0, Help: "errors#log"}) {
							if err := p.h.Run(context.Background(), a.Command, registry.Args(a.Args)); err != nil {
								p.h.Notify(core.Notice{ID: "action.failed", Severity: core.SevError, Source: "core", Text: fmt.Sprintf("%s: %v", a.Title, err)})
							}
						}
					}
				})
			}
		})
		if loc != "" {
			if f := p.source(loc); f != nil {
				c.Column("source", ui.Style{W: ui.Flex(1.3)}, func(c *ui.Ctx) { f.build(c, r.n) })
			}
		}
	})
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
			id := c.Next(i)
			if c.Interaction(id).Clicked && f.Line > 0 {
				if err := p.h.Run(context.Background(), "editor.open", registry.Args{"path": p.resolve(f.File), "line": f.Line}); err != nil {
					p.h.Notify(core.Notice{ID: "log.open-frame", Severity: core.SevWarning, Source: "core", Key: true, Text: err.Error()})
				}
			}
			fill := color.NRGBA{}
			if f.Loc() == failing {
				fill = th.C(theme.BgRaised)
			}
			c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(19), Pad: ui.PadXY(6, 0), Gap: 12, Cross: ui.Center, Fill: fill, Radius: 3, Interactive: true, Help: "errors#log",
				Cursor: render.CursorPointer, Role: ui.RoleButton, Label: f.Loc()}, func(c *ui.Ctx) {
				c.Box("loc", ui.Style{W: ui.Px(180), Clip: true}, func(c *ui.Ctx) {
					c.Text(nil, shortLoc(f), mono(11, typeset.Normal, th.C(tLogSource)))
				})
				c.Row("fn", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
					c.Text(nil, f.Fn, mono(11, typeset.Normal, th.C(theme.FgMuted)))
				})
			})
		}
		if hidden > 0 {
			id := c.Next("gofr")
			if c.Interaction(id).Clicked {
				p.goFrames = true
			}
			c.Row("gofr", ui.Style{Pad: ui.PadXY(6, 2), Gap: 12, Interactive: true, Help: "errors#log", Cursor: render.CursorPointer, Role: ui.RoleButton, Label: "Show Go frames"}, func(c *ui.Ctx) {
				c.Text("n", fmt.Sprintf("+%d Go frames", hidden), mono(11, typeset.Normal, th.C(theme.FgGhost)))
				c.Text("show", "show", mono(11, typeset.Normal, th.C(theme.FgFaint)))
			})
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

// sourceFile is a few lines around a failing one, highlighted with tree-sitter.
type sourceFile struct {
	path    string
	line    int
	lines   []string
	first   int // the line number of lines[0]
	spans   [][]span
	modTime time.Time
	mark    bool            // the failing line, marked
	title   string          // the path its header shows; the file's own, home as ~, if ""
	theme   *theme.Resolved // drawn in this, not the window's: a theme's preview
}

type span struct {
	text    string
	capture string
}

const sourceContext = 2

func (p *logPanel) source(loc string) *sourceFile {
	file, line := script.SplitLoc(loc)
	if line == 0 {
		return nil
	}
	path := p.resolve(file)
	st, err := os.Stat(path)
	if err != nil {
		return nil
	}
	if f, ok := p.files[loc]; ok && f.modTime.Equal(st.ModTime()) {
		return f
	}
	src, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	f := highlightAround(p.h, path, src, line, sourceContext, sourceContext)
	f.mark = true
	f.modTime = st.ModTime()
	p.files[loc] = f
	return f
}

// highlightAround is the lines from before lines above line to after lines below it, highlighted.
func highlightAround(r registry.Registry, path string, src []byte, line, before, after int) *sourceFile {
	all := strings.Split(string(src), "\n")
	first := max(1, line-before)
	last := min(len(all), line+after)
	f := &sourceFile{path: path, line: line, first: first}
	if first > last {
		return f
	}
	f.lines = all[first-1 : last]
	var hl []syntax.Span
	if lang, ok := languageFor(r, path, src); ok {
		if ps, err := syntax.NewParser(lang); err == nil {
			ps.Parse(src)
			start := offsetOfLine(all, first)
			hl, _ = ps.Highlights(start, start+len(strings.Join(f.lines, "\n")))
			ps.Close()
		}
	}
	off := offsetOfLine(all, first)
	for _, ln := range f.lines {
		f.spans = append(f.spans, splitSpans(ln, off, hl))
		off += len(ln) + 1
	}
	return f
}

func offsetOfLine(lines []string, n int) int {
	off := 0
	for _, l := range lines[:n-1] {
		off += len(l) + 1
	}
	return off
}

// splitSpans cuts one line at offset off into runs by highlight capture.
func splitSpans(ln string, off int, hl []syntax.Span) []span {
	var out []span
	pos := 0
	for _, h := range hl {
		s, e := max(h.Range.Start-off, pos), min(h.Range.End-off, len(ln))
		if e <= s {
			continue
		}
		if s > pos {
			out = append(out, span{text: ln[pos:s]})
		}
		out = append(out, span{text: ln[s:e], capture: h.Capture})
		pos = e
	}
	if pos < len(ln) {
		out = append(out, span{text: ln[pos:]})
	}
	return out
}

func (f *sourceFile) build(c *ui.Ctx, n *core.Notification) {
	th := c.Theme()
	if f.theme != nil {
		th = f.theme
	}
	home, _ := os.UserHomeDir()
	shown := f.path
	if home != "" && strings.HasPrefix(shown, home+string(filepath.Separator)) {
		shown = "~" + shown[len(home):]
	}
	if f.title != "" {
		shown = f.title
	}
	c.Column("src", ui.Style{W: ui.Flex(1), Fill: th.C(theme.BgBase), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
		c.Row("path", ui.Style{W: ui.Flex(1), H: ui.Px(24), Pad: ui.PadXY(10, 0), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text(nil, shown, mono(10.5, typeset.Normal, th.C(theme.FgFaint)))
		})
		c.Column("lines", ui.Style{W: ui.Flex(1), Pad: ui.Insets{B: 6}}, func(c *ui.Ctx) {
			for i, ln := range f.spans {
				no := f.first + i
				failing := f.mark && no == f.line
				fill := color.NRGBA{}
				if failing {
					fill = th.C(tLogFailLine)
				}
				c.Row(no, ui.Style{W: ui.Flex(1), H: ui.Px(18), Gap: 10, Cross: ui.Center, Fill: fill}, func(c *ui.Ctx) {
					edge := color.NRGBA{}
					if failing {
						edge = th.C(theme.Error)
					}
					c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
					c.Box("no", ui.Style{W: ui.Px(22)}, func(c *ui.Ctx) {
						c.Text(nil, fmt.Sprint(no), mono(11, typeset.Normal, th.C(theme.FgGhost)))
					})
					c.Row("code", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
						for j, s := range ln {
							col := th.C(theme.Fg)
							if s.capture != "" {
								col = th.Syntax("@" + s.capture).FG
							}
							c.Text(j, expandTabs(s.text), mono(11.5, typeset.Normal, col))
						}
					})
				})
			}
		})
	})
}
