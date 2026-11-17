package explain

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/liamg/oriel/app/kit/diagrams"
	"github.com/liamg/oriel/app/kit/draw"
	"github.com/liamg/oriel/internal/words"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

type walkView struct{ x *Walkthrough }

func (v walkView) Title(string) string { return "Explain this change" }
func (v walkView) EdgeToEdge()         {}
func (v walkView) ModeLabel(string) (string, string) {
	return "EXPLAIN", tWalkMode.Name()
}
func (v walkView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "explain.change", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

// view is 07-explain: the header, the step's file with its notes, and the step dock.
func (x *Walkthrough) view(c *ui.Ctx, vc uireg.ViewCtx) {
	th := c.Theme()
	x.check()
	root := c.Next("walk")
	if x.focus {
		c.RequestFocus(root)
		x.focus = false
	}
	w := x.w
	c.Column("walk", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(draw.TEditorBg), Clip: true, Focusable: true, DefaultFocus: true, KeyMode: ExplainChangeKeyMode,
		Role: ui.RoleRegion, Label: "Explain this change", Help: walkHelp}, func(c *ui.Ctx) {
		x.header(c, w)
		switch {
		case w == nil:
			x.message(c, "No walkthrough: Explain this change (␣E) asks for one.", false)
		case w.loading:
			x.asking(c, w)
		case w.err != nil:
			x.message(c, w.err.Error(), true)
		default:
			x.stage(c, w)
			x.dock(c, w)
		}
	})
}

// asking stands in for the stage while the walkthrough is asked for: how long it's taken, and Cancel.
func (x *Walkthrough) asking(c *ui.Ctx, w *walkthrough) {
	c.Column("msg", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 12}, func(c *ui.Ctx) {
		widget.Busy(c, "busy", widget.LoadState{Phase: widget.Pending, Since: w.since}, widget.BusyOpts{Label: "Asking for a walkthrough of the change",
			Size: 13, Clock: time.Second, Help: walkHelp})
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: walkHelp}) {
			w.cancel()
			x.w = nil
		}
	})
}

// message stands in for the stage: what's happening, or what went wrong with Try again.
func (x *Walkthrough) message(c *ui.Ctx, msg string, failed bool) {
	th := c.Theme()
	c.Column("msg", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 12}, func(c *ui.Ctx) {
		tok := theme.FgFaint
		if failed {
			tok = theme.Error
		}
		c.Text("t", msg, widget.UIFont(13, th.C(tok)))
		if failed && widget.Button(c, "again", "Try again", widget.ButtonOpts{Help: walkHelp}) {
			x.w = nil
			x.run("explain.change", nil)
		}
	})
}

// header is EXPLAIN THIS CHANGE, the step's project and file and its lines, and the throwaway note with Discard.
func (x *Walkthrough) header(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center, Fill: th.C(tWalkBarBg), Stroke: th.C(theme.Line),
		Clip: true}, func(c *ui.Ctx) {
		widget.Chip(c, "chip", "Explain this change", th.C(tWalkStep), walkHelp)
		if x.ready() {
			s := w.steps[w.step]
			switch {
			case s.path != "":
				c.Box("dot", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 2, Fill: kit.ProjectsHere(x.h).Colour(th, s.proj)}, nil)
				c.Text("file", s.proj+" / "+s.rel, widget.MonoFont(12, th.C(theme.Fg)))
				c.Text("lines", fmt.Sprintf("L%d–%d", s.Start, s.End), widget.MonoFont(11, th.C(theme.FgFaint)))
			case s.missing:
				c.Text("file", s.rel+" isn't in the change", widget.MonoFont(12, th.C(theme.Warn)))
			default:
				c.Text("file", "summary", widget.MonoFont(12, th.C(theme.FgMuted)))
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if w == nil {
			return
		}
		c.Text("throwaway", "throwaway · discarded if the code changes", widget.UIFont(11, th.C(theme.FgFaint)))
		if widget.Button(c, "discard", "Discard", widget.ButtonOpts{Ghost: true, Help: walkHelp}) {
			x.run("explain.change.discard", nil)
		}
	})
}

// lit is the note lighting line l of step s, and whether any does; with no notes, the step's own lines are lit.
func lit(s walkStep, l int) (note int, ok bool) {
	for i, n := range s.Notes {
		if l >= n.Start && l <= n.End {
			return i, true
		}
	}
	if len(s.Notes) == 0 && l >= s.Start && l <= s.End {
		return -1, true
	}
	return -1, false
}

func noteTone(th *theme.Resolved, i int) render.Color {
	if i < 0 {
		return th.C(tWalkStep)
	}
	return th.C(tWalkNotes[i%len(tWalkNotes)])
}

// stageLayout is a frame of the stage: the step and its file, and where the code, its gutter and the notes go.
type stageLayout struct {
	s                                   walkStep
	hl                                  *kit.HighlightedExcerpt
	lines                               []string
	m                                   draw.PaneMetrics
	area                                render.Rect
	last                                int // the last line shown; w.top is the first
	gutter, textX, codeW, notesW, cardX float32
}

func newStageLayout(s walkStep, hl *kit.HighlightedExcerpt, lines []string, m draw.PaneMetrics, area render.Rect, last int) stageLayout {
	gutter := max(float32(draw.GutterW), float32(len(fmt.Sprint(len(lines))))*m.CharW+34)
	textX := gutter + 4
	notesW := float32(440)
	codeW := max(area.Dx()-notesW-110, area.Dx()*.5)
	codeW = min(codeW, textX+float32(min(max(widestLine(lines), 40), codeColsMost))*m.CharW+24) // the notes sit beside the code
	notesW = min(notesW, area.Dx()-codeW-90)
	return stageLayout{s: s, hl: hl, lines: lines, m: m, area: area, last: last, gutter: gutter, textX: textX, codeW: codeW,
		notesW: notesW, cardX: codeW + 70}
}

// scrollStage moves the stage's first line: by the wheel, to the step on its first frame laid out (reveal), and to
// keep the cursor line in sight (follow); it answers the last line shown.
func (w *walkthrough) scrollStage(s walkStep, rows, lines, wheel int, laid bool) int {
	w.top += wheel
	if w.reveal && laid {
		// From a little above the step, unless that leaves its first note out of sight: then from a little above it.
		if len(s.Notes) > 0 && s.Notes[0].Start > w.top+rows-4 {
			w.top = max(s.Notes[0].Start-3, 1)
		}
		w.reveal = false
	}
	if w.follow {
		w.top = min(w.top, w.line)
		w.top = max(w.top, w.line-rows+1)
		w.follow = false
	}
	w.top = min(max(w.top, 1), max(lines-rows+1, 1))
	last := min(w.top+rows-1, lines)
	w.line = min(max(w.line, w.top), last)
	return last
}

// stage is the step's file from a little above what it's about: the lines its notes are on lit in their colours, the
// rest dimmed, and the notes beside them with leader lines; a step with no file says so.
func (x *Walkthrough) stage(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	s := w.steps[w.step]
	stageID := c.Next("stage")
	in := c.Interaction(stageID)
	if s.path == "" {
		if x.diagram(c, stageID, s) {
			return
		}
		c.Column("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			msg := "This step is about the change as a whole."
			if s.missing {
				msg = s.rel + " isn't in the change or its projects."
			}
			c.Text("t", msg, widget.UIFont(13, th.C(theme.FgFaint)))
		})
		return
	}
	x.metrics = draw.Metrics(c, x.h.Options(), x.metrics)
	m := x.metrics
	src := x.srcOf(w, s.path)
	if src.Loading() {
		c.Box("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, nil)
		return
	}
	hl, lines := src.Around(x.h, s.path, 1, 0, math.MaxInt32), src.Lines()
	area, laid := render.XYWH(0, 0, 1200, 600), false
	if r, ok := c.Rect(stageID); ok {
		area, laid = r, true
	}
	rows := max(int(area.Dy()/m.LineH)-1, 1)
	wheel := 0
	if in.Scroll.Y != 0 {
		wheel = int(math.Round(float64(in.Scroll.Y / m.LineH)))
	}
	lay := newStageLayout(s, hl, lines, m, area, w.scrollStage(s, rows, len(lines), wheel, laid))
	rowY := func(l int) float32 { return float32(l-w.top)*m.LineH + 10 }

	c.Box("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Clip: true, Wheel: true, Help: walkHelp}, func(c *ui.Ctx) {
		codeID := c.Next("code")
		click := widget.Clickable(c, "code", ui.Style{Dir: ui.Column, W: ui.Px(lay.codeW), Pad: ui.Insets{T: 10}, Clip: true, NoHover: true, Cursor: render.CursorText,
			Role: ui.RoleTextBox, Label: s.rel, Help: walkHelp}, func(c *ui.Ctx) {
			for l := w.top; l <= lay.last; l++ {
				x.codeRow(c, w, lay, l)
			}
		})
		if r, ok := c.Rect(codeID); ok && click.Clicked {
			l := w.top + int((click.Pointer().Y-r.Min.Y-10)/m.LineH)
			w.line = min(max(l, w.top), lay.last)
		}
		ps := placeNotes(w, s, lines, lay.last, area.Dy(), func(l, widest int) render.Point {
			return render.Point{X: min(lay.textX+float32(widest+3)*m.CharW, lay.codeW-12), Y: rowY(l) + m.LineH/2}
		})
		x.notes(c, w, lay, ps)
	})
}

// diagram draws a step with no file's diagram in the room a file would take, centred and scaled down to fit it, with
// what it is over it. It answers false when the step has none it can draw.
func (x *Walkthrough) diagram(c *ui.Ctx, stageID ui.ID, s walkStep) bool {
	if s.Diagram == "" || s.missing {
		return false
	}
	l, err := diagrams.Of(c, s.Diagram)
	if err != nil || l == nil {
		return false
	}
	th := c.Theme()
	area := render.XYWH(0, 0, 1200, 600)
	if r, ok := c.Rect(stageID); ok {
		area = r
	}
	// As wide as fits, and no taller than the stage leaves under the kicker.
	width := min(area.Dx()-80, l.Size.X)
	if h := area.Dy() - 90; h > 0 && l.Size.Y*width/l.Size.X > h {
		width = l.Size.X * h / l.Size.Y
	}
	c.Column("stage", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Gap: 14, Clip: true}, func(c *ui.Ctx) {
		c.Text("kicker", diagrams.Kicker(l), widget.KickerFont(th.C(theme.FgFaint)))
		diagrams.Draw(c, "diagram", l, max(width, 80), kit.MarkdownOpts{Help: walkHelp})
	})
	return true
}

// codeColsMost is the widest the stage's code column grows for its file, in columns: a stray long line runs under the
// notes rather than pushing them across the window.
const codeColsMost = 110

// widestLine is the widest of lines, in columns, tabs expanded.
func widestLine(lines []string) int {
	widest := 0
	for _, l := range lines {
		widest = max(widest, utf8.RuneCountInString(kit.ExpandTabs(l)))
	}
	return widest
}

// placedNote is a note's card on the stage: its top, height, and where its leader starts.
type placedNote struct {
	i    int
	y, h float32
	from render.Point
}

// placeNotes levels each visible note with its first line and below the one before, then moves them up from the
// bottom so none runs out; anchor is where a note's leader starts, given its first visible line and widest line.
func placeNotes(w *walkthrough, s walkStep, lines []string, last int, height float32, anchor func(l, widest int) render.Point) []placedNote {
	var ps []placedNote
	for i, n := range s.Notes {
		if n.End < w.top || n.Start > last {
			continue
		}
		h := w.cardH[i]
		if h == 0 {
			h = 110
		}
		widest := 0
		for l := max(n.Start, w.top); l <= min(n.End, last); l++ {
			widest = max(widest, utf8.RuneCountInString(kit.ExpandTabs(lines[l-1])))
		}
		from := anchor(max(n.Start, w.top), widest)
		y := from.Y - 30
		if len(ps) > 0 {
			p := ps[len(ps)-1]
			y = max(y, p.y+p.h+14)
		}
		ps = append(ps, placedNote{i: i, y: max(y, 8), h: h, from: from})
	}
	bottom := height - 8
	for k := len(ps) - 1; k >= 0; k-- {
		ps[k].y = max(min(ps[k].y, bottom-ps[k].h), 8)
		bottom = ps[k].y - 14
	}
	return ps
}

// notes draws the placed notes' cards and their leader lines.
func (x *Walkthrough) notes(c *ui.Ctx, w *walkthrough, lay stageLayout, ps []placedNote) {
	th := c.Theme()
	s, area, cardX, notesW := lay.s, lay.area, lay.cardX, lay.notesW
	if len(ps) > 0 {
		c.Box("leaders", ui.Style{Float: &ui.Float{}, Z: ui.LayerContent}, func(c *ui.Ctx) {
			c.Paint("lines", ui.Px(area.Dx()), ui.Px(area.Dy()), func(f render.Frame, r render.Rect) {
				f.PushClip(r, 0)
				defer f.Pop()
				for _, p := range ps {
					from := render.Point{X: r.Min.X + p.from.X, Y: r.Min.Y + p.from.Y}
					to := render.Point{X: r.Min.X + cardX, Y: r.Min.Y + p.y + 20}
					widget.Leader(f, from, to, noteTone(th, p.i))
				}
			})
		})
	}
	for _, p := range ps {
		n := s.Notes[p.i]
		c.Box(fmt.Sprint("note", p.i), ui.Style{Float: &ui.Float{Offset: render.Point{X: cardX, Y: p.y}}, Z: ui.LayerContent + 1}, func(c *ui.Ctx) {
			id := widget.Annotation(c, "card", widget.AnnotationOpts{N: noteMark(p.i), Title: n.Title, Tone: noteTone(th, p.i),
				Width: ui.Px(notesW), Ground: th.C(draw.TEditorBg), Help: walkHelp}, func(c *ui.Ctx) {
				if n.Text != "" {
					kit.MarkdownText(c, x.h, "md", n.Text, notesW-28, widget.AnnotationBody(c), kit.MarkdownOpts{})
				}
			})
			if r, ok := c.Rect(id); ok && r.Dy() != w.cardH[p.i] {
				w.cardH[p.i] = r.Dy()
				x.h.Redraw()
			}
		})
	}
}

// codeRow is line l of the step's file: its number, coloured by the note it's lit for, and its text, dimmed when no
// note is on it, with a note's mark highlighted.
func (x *Walkthrough) codeRow(c *ui.Ctx, w *walkthrough, lay stageLayout, l int) {
	th := c.Theme()
	s, hl, lines, gutter, m := lay.s, lay.hl, lay.lines, lay.gutter, lay.m
	note, on := lit(s, l)
	tone := noteTone(th, note)
	st := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(m.LineH), Cross: ui.Center}
	if on {
		st.Fill = theme.WithAlpha(tone, .08)
	} else {
		st.Fade = .65
	}
	c.Row(l, st, func(c *ui.Ctx) {
		num := th.C(draw.TGutterFg)
		switch {
		case l == w.line:
			num = th.C(theme.Fg)
		case on:
			num = tone
		}
		c.Row("gutter", ui.Style{W: ui.Px(gutter), Pad: ui.Insets{R: 16}, Main: ui.End}, func(c *ui.Ctx) {
			c.Text("n", fmt.Sprint(l), ui.TextStyle{Font: m.Font, Color: num})
		})
		if on {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(m.LineH), Fill: tone, Float: &ui.Float{Offset: render.Point{X: gutter - 2}}}, nil)
		}
		c.Box("gap", ui.Style{W: ui.Px(4)}, nil)
		if on && note >= 0 {
			if mk := s.Notes[note].Mark; mk != "" {
				line := kit.ExpandTabs(lines[l-1])
				if i := strings.Index(line, mk); i >= 0 {
					col := utf8.RuneCountInString(line[:i])
					c.Box("mark", ui.Style{W: ui.Px(float32(utf8.RuneCountInString(mk)) * m.CharW), H: ui.Px(m.LineH - 2), Radius: 2,
						Fill: theme.WithAlpha(tone, .2), Z: ui.LayerUnder, Float: &ui.Float{Offset: render.Point{X: gutter + 4 + float32(col)*m.CharW, Y: 1}}}, nil)
				}
			}
		}
		if hl != nil && l-hl.First >= 0 && l-hl.First < len(hl.Spans) {
			for k, sp := range hl.Spans[l-hl.First] {
				ts := ui.TextStyle{Font: m.Font, Color: th.C(draw.TCodeFg)}
				if sp.Capture != "" {
					sy := th.Syntax("@" + sp.Capture)
					ts.Color, ts.Font.Italic = sy.FG, sy.Italic
				}
				c.Text(k, strings.TrimRight(kit.ExpandTabs(sp.Text), "\r"), ts)
			}
			return
		}
		c.Text("t", kit.ExpandTabs(lines[l-1]), ui.TextStyle{Font: m.Font, Color: th.C(draw.TCodeFg)})
	})
}

// dock is the step dock: a chip for each step across files and repos, then the step's number, title and prose, Ask
// about this step, Comment on its line, who wrote it, and ← →.
func (x *Walkthrough) dock(c *ui.Ctx, w *walkthrough) {
	th := c.Theme()
	s := w.steps[w.step]
	n := len(w.steps)
	c.Column("dock", ui.Style{W: ui.Flex(1), Fill: th.C(tWalkDockBg), Role: ui.RoleRegion, Label: "Steps", Help: walkHelp}, func(c *ui.Ctx) {
		c.Row("edge", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tWalkDockEdge)}, func(c *ui.Ctx) {
			c.Box("done", ui.Style{W: ui.Pct(float32(w.step+1) / float32(n)), H: ui.Px(1), Fill: th.C(tWalkStep)}, nil)
		})
		c.Row("strip", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 18, R: 18, T: 12, B: 10}, Gap: 16, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			c.Row("steps", ui.Style{W: ui.Flex(1), Cross: ui.Center, ScrollX: true}, func(c *ui.Ctx) {
				for i, st := range w.steps {
					if i > 0 {
						wire := th.C(theme.LineStrong)
						if i <= w.step {
							wire = theme.WithAlpha(th.C(tWalkStep), .5)
						}
						c.Box(fmt.Sprint("wire", i), ui.Style{W: ui.Px(26), H: ui.Px(1), Fill: wire}, nil)
					}
					if x.stepChip(c, i, st, i == w.step, i < w.step) {
						x.run("explain.change.step", registry.Args{"step": i + 1})
					}
				}
			})
			c.Text("n", fmt.Sprintf("%d %s · %d %s", w.repos, words.Plural(w.repos, "repo", "repos"), w.files, words.Plural(w.files, "file", "files")),
				widget.MonoFont(10.5, th.C(theme.FgFaint)))
		})
		c.Row("body", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 14), Gap: 26}, func(c *ui.Ctx) {
			c.Column("num", ui.Style{W: ui.Px(110), Gap: 6}, func(c *ui.Ctx) {
				c.Text("k", "STEP", widget.KickerFont(th.C(theme.FgFaint)))
				c.Row("n", ui.Style{Cross: ui.End}, func(c *ui.Ctx) {
					c.Text("i", fmt.Sprintf("%02d", w.step+1), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 40, Weight: typeset.SemiBold},
						Color: th.C(tWalkStep)})
					c.Text("of", fmt.Sprintf("/%02d", n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16}, Color: th.C(theme.FgFaint)})
				})
			})
			width := float32(760)
			if r, ok := c.Rect(c.Next("text")); ok {
				width = min(r.Dx(), 760)
			}
			c.Column("text", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
				c.Text("title", s.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 19, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				if s.Prose != "" {
					kit.MarkdownText(c, x.h, "prose", s.Prose, width, widget.UIFont(13.5, th.C(theme.FgMuted)), kit.MarkdownOpts{})
				}
				c.Row("acts", ui.Style{Gap: 8, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
					if widget.Button(c, "ask", "Ask about this step", widget.ButtonOpts{Ghost: true, Help: walkHelp,
						Disabled: !x.h.Available("explain.change.ask", nil)}) {
						x.run("explain.change.ask", nil)
					}
					if s.path != "" && widget.Button(c, "comment", fmt.Sprintf("Comment on L%d", w.line), widget.ButtonOpts{Ghost: true, Help: walkHelp,
						Disabled: !x.h.Available("explain.change.comment", nil)}) {
						x.run("explain.change.comment", nil)
					}
					c.Text("by", w.by, widget.UIFont(11, th.C(theme.FgFaint)))
				})
			})
			c.Row("nav", ui.Style{Gap: 8}, func(c *ui.Ctx) {
				if widget.Button(c, "prev", "←", widget.ButtonOpts{Ghost: true, Help: walkHelp, Disabled: w.step == 0}) {
					x.run("explain.change.step", registry.Args{"by": -1})
				}
				if widget.Button(c, "next", "→", widget.ButtonOpts{Help: walkHelp, Disabled: w.step == n-1}) {
					x.run("explain.change.step", registry.Args{"by": 1})
				}
			})
		})
	})
}

// stepChip is a step's diamond and its project and file, lit while it's shown; it says whether it was clicked.
func (x *Walkthrough) stepChip(c *ui.Ctx, i int, s walkStep, on, done bool) bool {
	th := c.Theme()
	fill, fg := th.C(theme.LineStrong), th.C(theme.FgFaint)
	switch {
	case on:
		fill, fg = th.C(tWalkStep), th.C(theme.BgBase)
	case done:
		fill, fg = theme.WithAlpha(th.C(tWalkStep), .18), th.C(tWalkStep)
	}
	proj, file := s.proj, filepath.Base(s.rel)
	if s.path != "" && strings.Contains(s.rel, "/") && on {
		file = s.rel
	}
	if s.path == "" && !s.missing {
		proj, file = "—", "summary"
	}
	border := th.C(theme.LineStrong)
	if on {
		border = theme.WithAlpha(th.C(tWalkStep), .45)
	}
	return widget.Clickable(c, fmt.Sprint("step", i), ui.Style{Dir: ui.Row, Gap: 6, Cross: ui.Center, Radius: 3, Label: fmt.Sprintf("Step %d: %s", i+1, s.Title),
		Selected: on, Tooltip: s.Title, Help: walkHelp}, func(c *ui.Ctx) {
		widget.Diamond(c, "n", fmt.Sprint(i+1), 22, fill, fg)
		c.Row("file", ui.Style{Pad: ui.PadXY(7, 2), Gap: 5, Stroke: border, Border: 1, Radius: 3, Fill: th.C(theme.BgSurface), Cross: ui.Center}, func(c *ui.Ctx) {
			if proj != "" {
				c.Text("p", proj, widget.MonoFont(10.5, th.C(tWalkProject)))
			}
			tok := theme.FgFaint
			if on {
				tok = theme.Fg
			}
			c.Text("f", file, widget.MonoFont(10.5, th.C(tok)))
		})
	}).Clicked
}

// statusSegment is the status line's step, file and keys while the walkthrough has focus: "step 3/7 · invoice.go ·
// ←/→ step · c comment · q quit".
func (x *Walkthrough) statusSegment(c *ui.Ctx) {
	if !x.ready() {
		return
	}
	th := c.Theme()
	w, s := x.w, x.current()
	c.Row("explain", ui.Style{H: ui.Flex(1), Gap: 12, Cross: ui.Center, Role: ui.RoleStatus, Label: "Walkthrough"}, func(c *ui.Ctx) {
		c.Text("step", fmt.Sprintf("step %d/%d", w.step+1, len(w.steps)), draw.StatusText(c, draw.TStatusStrong))
		if s.rel != "" {
			c.Text("file", filepath.Base(s.rel), draw.StatusText(c, kit.TStatusFg))
		}
		c.Box("sep", ui.Style{W: ui.Px(1), H: ui.Px(12), Fill: th.C(theme.Line)}, nil)
		c.Text("keys", "←/→ step · c comment · q quit", draw.StatusText(c, kit.TStatusFg))
	})
}

// noteMark names a step's note in its diamond with a letter, A onwards, so notes don't read as the steps' numbers.
func noteMark(i int) string {
	if i < 26 {
		return string(rune('A' + i))
	}
	return fmt.Sprint(i + 1)
}
