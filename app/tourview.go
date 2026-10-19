package app

import (
	"context"
	"fmt"
	"image"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/tour"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tTourMode   = theme.Declare("tour.mode", "accent")
	tTourAccent = theme.Declare("tour.accent", "accent")
	tTourNote   = theme.Declare("tour.note", "violet")
	tTourSide   = theme.Declare("tour.side.bg", "bg.surface")
	tTourCode   = theme.Declare("tour.code.bg", "bg.surface")
	tTourMarked = theme.DeclareOn("tour.code.marked", "alpha(accent, 7%)", "bg.surface")
)

type tourView struct{ x *tourPlugin }

func (v tourView) Title(string) string { return "Tour" }
func (v tourView) EdgeToEdge()         {}
func (v tourView) ModeLabel(string) (string, string) {
	return "TOUR", tTourMode.Name()
}
func (v tourView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "tour", Fn: func(c *ui.Ctx) { v.x.view(c, vc) }}
}

// view is 14-tour: the steps at the left, the step in the middle, and its diagram and questions at the right.
func (x *tourPlugin) view(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	se, st, ok := x.current()
	root := c.Next("tour")
	if ok && (vc.Focused && !c.FocusWithin(root) || st.focus) {
		c.Focus(root)
		st.focus = false
	}
	c.Row("tour", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase), Clip: true, Focusable: true, KeyMode: TourKeyMode,
		Role: ui.RoleRegion, Label: "Tour", Help: tourHelp}, func(c *ui.Ctx) {
		if !ok {
			c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", "The session in front isn't a tour.", uiFont(13, th.C(theme.FgFaint)))
			})
			return
		}
		x.side(c, se, st)
		x.page(c, se, st)
		x.rightColumn(c, se, st)
	})
}

// side is the tour's kind, question, repos and pin, how far it's been read, its steps, and Export.
func (x *tourPlugin) side(c *ui.Ctx, se core.SessionState, st *tourState) {
	th := c.Theme()
	t := st.t
	c.Column("side", ui.Style{W: ui.Px(270), H: ui.Flex(1), Fill: th.C(tTourSide), Stroke: th.C(theme.Line), Role: ui.RoleRegion, Label: "Steps",
		Help: tourHelp}, func(c *ui.Ctx) {
		c.Column("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 14, T: 16, B: 12}, Gap: 9}, func(c *ui.Ctx) {
			c.Text("kicker", "TOUR · "+strings.ToUpper(t.Kind), kickerFont(th.C(tTourAccent)))
			widget.Rich(c, "title", []widget.Span{{Text: cmpOr(t.Title, se.Name)}}, 242,
				ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 15, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)}, widget.RichOpts{})
			if len(t.Repos) > 0 {
				c.Row("repos", ui.Style{W: ui.Flex(1), Gap: 6, ScrollX: true}, func(c *ui.Ctx) {
					for i, r := range t.Repos {
						widget.Chip(c, i, r.Name, tokenColour(th, se.ProjectColour(r.Name), th.C(theme.Accent)), tourHelp)
					}
				})
				if r := t.Repos[0]; r.Commit != "" {
					widget.Chip(c, "pin", "pinned @ "+tour.ShortSHA(r.Commit), th.C(theme.FgMuted), tourHelp)
				}
			}
			n := len(t.Steps)
			read := 0
			for _, s := range t.Steps {
				if st.visited[s.File] {
					read++
				}
			}
			c.Row("progress", ui.Style{W: ui.Flex(1), H: ui.Px(2), Fill: th.C(theme.Line), Radius: 1}, func(c *ui.Ctx) {
				if n > 0 {
					c.Box("done", ui.Style{W: ui.Pct(float32(read) / float32(n)), H: ui.Px(2), Fill: th.C(tTourAccent), Radius: 1}, nil)
				}
			})
		})
		c.Column("steps", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			for i, s := range t.Steps {
				if x.stepRow(c, i, s, i == st.step, st.visited[s.File] && i != st.step) {
					x.run("tour.step", registry.Args{"step": i + 1})
				}
			}
		})
		c.Column("export", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 8, Stroke: th.C(theme.Line), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Row("btn", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
				if widget.Button(c, "export", "↓ Export as HTML", widget.ButtonOpts{Help: tourHelp + "#export", Disabled: len(t.Steps) == 0}) {
					x.run("tour.export", registry.Args{"ask": true})
				}
			})
			widget.Rich(c, "note", []widget.Span{{Text: "self-contained · no fetches · share as artifact"}}, 200, monoFont(10, th.C(theme.FgFaint)),
				widget.RichOpts{})
			if t.ShareURL != "" && widget.Link(c, "shared", "shared ↗ "+t.ShareURL, tourHelp+"#export") {
				if err := x.sh.desktop.OpenURL(t.ShareURL); err != nil {
					x.notify(err)
				}
			}
		})
	})
}

// stepRow is a step in the list: ✓ once read, else its number; the one shown lit. It says whether it was clicked.
func (x *tourPlugin) stepRow(c *ui.Ctx, i int, s tour.Step, on, read bool) bool {
	th := c.Theme()
	st := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(14, 0), Gap: 10, Cross: ui.Center, Label: fmt.Sprintf("Step %d: %s", i+1, s.Title),
		Selected: on, Help: tourHelp}
	if on {
		st.Fill = theme.WithAlpha(th.C(tTourAccent), .07)
	}
	return widget.Clickable(c, fmt.Sprint("step", i), st, func(c *ui.Ctx) {
		if on {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(36), Fill: th.C(tTourAccent), Float: &ui.Float{Offset: render.Point{X: -14}}}, nil)
		}
		box := ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 3, Main: ui.Center, Cross: ui.Center, Stroke: th.C(theme.LineStrong), Border: 1, Keep: true}
		mark, fg := fmt.Sprint(i+1), th.C(theme.FgFaint)
		switch {
		case on:
			box.Fill, box.Stroke, fg = th.C(tTourAccent), th.C(tTourAccent), th.C(theme.AccentInk)
		case read:
			box.Stroke, mark, fg = th.C(theme.OK), "✓", th.C(theme.OK)
		}
		c.Box("n", box, func(c *ui.Ctx) { c.Text("t", mark, monoFont(10, fg)) })
		tok := theme.FgMuted
		if on {
			tok = theme.Fg
		}
		c.Box("title", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", s.Title, uiFont(13, th.C(tok))) })
	}).Clicked
}

// page is the step: its number and title, then what it says in order. Before any step lands, what the agent is doing
// and the conversation.
func (x *tourPlugin) page(c *ui.Ctx, se core.SessionState, st *tourState) {
	th := c.Theme()
	id := c.Next("page")
	if st.top {
		c.ScrollTo(id, render.Point{})
		st.top = false
	}
	if st.scroll != 0 {
		p := c.Scroll(id)
		c.ScrollTo(id, render.Point{X: p.X, Y: max(p.Y+st.scroll, 0)})
		st.scroll = 0
	}
	width := float32(780)
	if r, ok := c.Rect(id); ok {
		width = max(min(r.Dx()-88, 820), 300)
	}
	c.Column("page", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 44, R: 44, T: 34, B: 48}, Gap: 16, Help: tourHelp}, func(c *ui.Ctx) {
		s, ok := st.current()
		if !ok {
			x.waiting(c, se, st, width)
			return
		}
		c.Text("kicker", fmt.Sprintf("STEP %d OF %d", st.step+1, len(st.t.Steps)), kickerFont(th.C(tTourAccent)))
		widget.Rich(c, "title", []widget.Span{{Text: s.Title}}, width,
			ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 26, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)}, widget.RichOpts{})
		quote := 0
		for k, it := range s.Items {
			switch it.Kind {
			case tour.Prose:
				x.prose(c, k, it.Text, width)
			case tour.Note:
				c.Row(k, ui.Style{W: ui.Px(width), Pad: ui.Insets{L: 12}, Stroke: th.C(theme.LineStrong)}, func(c *ui.Ctx) {
					c.Box("bar", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: th.C(theme.LineStrong), Float: &ui.Float{Offset: render.Point{X: -12}}}, nil)
					markdownText(c, "md", it.Text, width-12, uiFont(13.5, th.C(theme.FgMuted)))
				})
			case tour.Quote:
				quote++
				x.quote(c, k, st, s, it, quote, width)
			case tour.Example:
				x.codeBox(c, k, "example", it.Lang, "", it.Text, width)
			case tour.Run:
				head := "$ " + it.Run.Cmd
				if it.Run.Cwd != "" {
					head = it.Run.Cwd + " " + head
				}
				exit := ""
				if it.Run.Exit != 0 {
					exit = fmt.Sprintf("exit %d", it.Run.Exit)
				}
				x.codeBox(c, k, head, exit, "", it.Text, width)
			case tour.Shot:
				x.shot(c, k, st, it, width)
			}
		}
	})
}

// waiting is the page before any step: what the agent's doing, and the question's thread.
func (x *tourPlugin) waiting(c *ui.Ctx, se core.SessionState, st *tourState, width float32) {
	th := c.Theme()
	what := "is waiting for a question"
	if len(se.Threads) > 0 {
		what = "is walking the code"
	}
	c.Row("drafting", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("d", "◆", uiFont(13, th.C(tTourAccent)))
		c.Text("t", providerName(se.Agent.Provider)+" "+what, uiFont(15, th.C(theme.Fg)))
	})
	c.Text("sub", "Steps appear here as they land: you can read step 1 while step 6 is being written.", uiFont(13, th.C(theme.FgFaint)))
	c.Column("thread", ui.Style{W: ui.Px(width), Gap: 10}, func(c *ui.Ctx) {
		if t, ok := se.Thread(se.PlanThread()); ok {
			x.threadCard(c, se, st, t)
		}
		x.askField(c, se, st, "Ask about this tour…")
	})
}

var imageLine = regexp.MustCompile(`^\s*!\[([^\]]*)\]\(([^)\s]+)\)\s*$`)

// prose is a step's markdown, with a line that's only an image drawn as the image.
func (x *tourPlugin) prose(c *ui.Ctx, key any, text string, width float32) {
	th := c.Theme()
	_, st, _ := x.current()
	c.Column(key, ui.Style{W: ui.Px(width), Gap: 12}, func(c *ui.Ctx) {
		var run []string
		k := 0
		flush := func() {
			if strings.TrimSpace(strings.Join(run, "")) != "" {
				markdownText(c, k, strings.Join(run, "\n"), width, uiFont(14.5, th.C(theme.Fg)))
				k++
			}
			run = nil
		}
		for _, l := range strings.Split(text, "\n") {
			m := imageLine.FindStringSubmatch(l)
			if m == nil {
				run = append(run, l)
				continue
			}
			flush()
			if img, ok := x.image(st.t.Root, m[2]); ok {
				widget.Image(c, k, img, min(width, float32(img.Bounds().Dx())), widget.ImageOpts{Label: m[1], Help: tourHelp, Zoom: true, Group: "tour:" + st.t.Root})
			} else {
				c.Text(k, m[2]+" isn't in the tour", monoFont(11, th.C(theme.Warn)))
			}
			k++
		}
		flush()
	})
}

// image is a picture in the tour, decoded once while its file stays the same.
func (x *tourPlugin) image(root, src string) (image.Image, bool) {
	p, err := tour.AssetPath(root, src)
	if err != nil {
		return nil, false
	}
	img, _, err := pictures.file(p)
	return img, err == nil
}

// quote is real code from its repo: a header naming where, its lines numbered as the file's with the marked ones lit,
// and its notes under their lines. A long quote shows only around its marks until "… n more lines" is clicked.
func (x *tourPlugin) quote(c *ui.Ctx, key any, st *tourState, s tour.Step, it tour.Item, n int, width float32) {
	th := c.Theme()
	q := it.Quote
	se, _ := x.front()
	c.Column(key, ui.Style{W: ui.Px(width), Fill: th.C(tTourCode), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true, Role: ui.RoleGroup,
		Label: q.Repo + " " + q.Path, Help: tourHelp}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			c.Text("repo", q.Repo, monoFont(11.5, tokenColour(th, se.ProjectColour(q.Repo), th.C(theme.Accent))))
			c.Box("path", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", q.Path, monoFont(11.5, th.C(theme.Fg))) })
			if q.Err != "" {
				return
			}
			if q.Rev != "" {
				widget.Chip(c, "rev", "@ "+q.Rev, th.C(tTourNote), tourHelp)
			}
			c.Text("lines", "L"+tour.LineLabel(q.Start, q.End), monoFont(10.5, th.C(theme.FgFaint)))
			if it.Lang != "" {
				widget.Chip(c, "lang", it.Lang, th.C(theme.FgMuted), tourHelp)
			}
			if q.Rev == "" && widget.Link(c, "open", "open ↗", tourHelp+"#quotes") {
				x.run("tour.open-quote", registry.Args{"n": n})
			}
		})
		if q.Err != "" {
			c.Row("err", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10)}, func(c *ui.Ctx) { c.Text("t", q.Err, uiFont(12.5, th.C(theme.Error))) })
			return
		}
		m := x.sh.editor.metrics(c)
		hl := x.highlight(st, filepath.Join(q.Dir, q.Path), q.Src, q.Start, q.End)
		gutter := float32(len(fmt.Sprint(q.End)))*m.charW + 28
		whole := st.expanded[q]
		c.Column("code", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 6, B: 6}, ScrollX: true}, func(c *ui.Ctx) {
			k := 0
			for l := q.Start; l <= q.End; l++ {
				if !whole && !q.Visible(l) {
					continue
				}
				tourCodeLine(c, l, fmt.Sprint(l), q.Lines[l-q.Start], hl, l-q.Start, q.Marked[l], gutter, m)
				for _, note := range q.NotesAt(l) {
					k++
					x.note(c, k, note, gutter, width)
				}
			}
			if h := q.Hidden(); h > 0 && !whole {
				c.Row("more", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: gutter, T: 4}}, func(c *ui.Ctx) {
					if widget.Link(c, "more", fmt.Sprintf("… %d more %s", h, plural(h, "line", "lines")), tourHelp+"#quotes") {
						st.expanded[q] = true
						for i, qq := range quotesOf(s) {
							if qq == q {
								st.open[fmt.Sprint(s.File, "#", i)] = true
							}
						}
					}
				})
			}
			if q.Clipped {
				c.Row("clip", ui.Style{Pad: ui.Insets{L: gutter, T: 4}}, func(c *ui.Ctx) {
					c.Text("t", "the range ran past the end of the file", uiFont(11.5, th.C(theme.FgFaint)))
				})
			}
			for _, note := range q.Loose {
				k++
				x.note(c, k, note, gutter, width)
			}
		})
	})
}

// note is a note pinned in a quote: its number in the annotation house style, under the line it's about.
func (x *tourPlugin) note(c *ui.Ctx, k int, n tour.PinnedNote, gutter, width float32) {
	th := c.Theme()
	w := width - gutter - 28
	c.Row(fmt.Sprint("note", k), ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: gutter, R: 14, T: 6, B: 8}}, func(c *ui.Ctx) {
		widget.Annotation(c, "card", widget.AnnotationOpts{N: fmt.Sprint(k), Title: "line " + tour.LineLabel(n.Line, n.End), Tone: th.C(tTourNote),
			Width: ui.Px(w), Ground: th.C(tTourCode), Help: tourHelp + "#quotes"}, func(c *ui.Ctx) {
			markdownText(c, "md", n.Text, w-28, widget.AnnotationBody(c))
		})
	})
}

// tourCodeLine is one line of code: its number, then its text in the theme's syntax colours; a marked line is lit,
// with the accent at its edge.
func tourCodeLine(c *ui.Ctx, key any, num, text string, hl *sourceFile, i int, marked bool, gutter float32, m paneMetrics) {
	th := c.Theme()
	st := ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(m.lineH), Cross: ui.Center}
	numFg := th.C(tGutterFg)
	if marked {
		st.Fill, numFg = th.C(tTourMarked), th.C(tTourAccent)
	}
	c.Row(key, st, func(c *ui.Ctx) {
		if marked {
			c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Px(m.lineH), Fill: th.C(tTourAccent), Float: &ui.Float{Offset: render.Point{X: gutter - 6}}}, nil)
		}
		c.Row("n", ui.Style{W: ui.Px(gutter), Pad: ui.Insets{R: 14}, Main: ui.End}, func(c *ui.Ctx) {
			if num != "" {
				c.Text("t", num, ui.TextStyle{Font: m.font, Color: numFg})
			}
		})
		if hl != nil && i < len(hl.spans) {
			for k, sp := range hl.spans[i] {
				ts := ui.TextStyle{Font: m.font, Color: th.C(tCodeFg)}
				if sp.capture != "" {
					sy := th.Syntax("@" + sp.capture)
					ts.Color, ts.Font.Italic = sy.FG, sy.Italic
				}
				c.Text(k, strings.TrimRight(expandTabs(sp.text), "\r"), ts)
			}
			return
		}
		c.Text("t", expandTabs(text), ui.TextStyle{Font: m.font, Color: th.C(tCodeFg)})
	})
}

// highlight is lines start through end of a file's src in its language's colours, worked out once per text.
func (x *tourPlugin) highlight(st *tourState, path string, src []byte, start, end int) *sourceFile {
	if len(src) == 0 || path == "" {
		return nil
	}
	k := fmt.Sprintf("%s\x00%p\x00%d\x00%d", path, &src[0], start, end)
	if f, ok := st.hl[k]; ok {
		return f
	}
	if len(st.hl) > 256 {
		clear(st.hl)
	}
	f := highlightAround(x.h, path, src, start, 0, end-start)
	st.hl[k] = f
	return f
}

// codeBox is code from nowhere (an example) or a recorded run's output, under a header.
func (x *tourPlugin) codeBox(c *ui.Ctx, key any, head, tag, lang, text string, width float32) {
	th := c.Theme()
	_, st, _ := x.current()
	c.Column(key, ui.Style{W: ui.Px(width), Fill: th.C(tTourCode), Stroke: th.C(theme.Line), Border: 1, Radius: 6, Clip: true, Role: ui.RoleGroup, Label: head,
		Help: tourHelp}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			c.Box("h", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", head, monoFont(11.5, th.C(theme.FgMuted))) })
			if strings.HasPrefix(tag, "exit") {
				c.Text("tag", tag, monoFont(10.5, th.C(theme.Error)))
			} else if tag != "" {
				widget.Chip(c, "tag", tag, th.C(theme.FgMuted), tourHelp)
			}
		})
		m := x.sh.editor.metrics(c)
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		var hl *sourceFile
		if head == "example" && tag != "" {
			hl = x.highlight(st, exampleName(x.h, tag), []byte(text), 1, len(lines))
		}
		c.Column("code", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 6, B: 6}, ScrollX: true}, func(c *ui.Ctx) {
			for i, l := range lines[:min(len(lines), 400)] {
				tourCodeLine(c, i, "", l, hl, i, false, 14, m)
			}
			if len(lines) > 400 {
				c.Text("more", fmt.Sprintf("… %d more lines", len(lines)-400), uiFont(11.5, th.C(theme.FgFaint)))
			}
		})
	})
}

// shot is a screenshot with its annotations over it: numbered pins, boxes, and arrows from where the eye starts.
func (x *tourPlugin) shot(c *ui.Ctx, key any, st *tourState, it tour.Item, width float32) {
	th := c.Theme()
	s := it.Shot
	img, ok := x.image(st.t.Root, s.Src)
	c.Column(key, ui.Style{W: ui.Px(width), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if !ok {
			c.Row("missing", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(12, 10), Stroke: th.C(theme.LineStrong), Border: 1, Radius: 4}, func(c *ui.Ctx) {
				c.Text("t", cmpOr(s.Src, "a screenshot")+" isn't in this tour's assets", uiFont(12, th.C(theme.Error)))
			})
			return
		}
		w := width
		h := w * float32(img.Bounds().Dy()) / float32(img.Bounds().Dx())
		accent := th.C(tTourAccent)
		c.Box("wrap", ui.Style{W: ui.Px(w), H: ui.Px(h), Radius: 4, Clip: true, Stroke: th.C(theme.Line), Border: 1}, func(c *ui.Ctx) {
			widget.Image(c, "img", img, w, widget.ImageOpts{Label: cmpOr(s.Alt, s.Src), Help: tourHelp, Zoom: true, Group: "tour:" + st.t.Root})
			c.Box("marks", ui.Style{Float: &ui.Float{}, Z: ui.LayerContent}, func(c *ui.Ctx) {
				c.Paint("lines", ui.Px(w), ui.Px(h), func(f render.Frame, r render.Rect) {
					for _, m := range s.Mark {
						switch m.Kind {
						case tour.Box:
							b := render.XYWH(r.Min.X+float32(m.X)/100*w, r.Min.Y+float32(m.Y)/100*h, float32(m.X2)/100*w, float32(m.Y2)/100*h)
							f.Rect(b, render.Style{Stroke: accent, Width: 2, Radius: 4})
						case tour.Arrow:
							x1, y1, bx, by, hd, ok := tour.ArrowHead(m, float64(w), float64(h))
							if !ok {
								continue
							}
							pt := func(x, y float64) render.Point { return render.Point{X: r.Min.X + float32(x), Y: r.Min.Y + float32(y)} }
							var line render.Path
							line.MoveTo(pt(x1, y1)).LineTo(pt(bx, by))
							f.Path(&line, render.Style{Stroke: accent, Width: 2})
							var head render.Path
							head.MoveTo(pt(hd[0][0], hd[0][1])).LineTo(pt(hd[1][0], hd[1][1])).LineTo(pt(hd[2][0], hd[2][1])).Close()
							f.Path(&head, render.Style{Fill: accent})
						}
					}
				})
			})
			for i, m := range s.Mark {
				at := render.Point{X: float32(m.X) / 100 * w, Y: float32(m.Y) / 100 * h}
				if m.Kind == tour.Box {
					at.Y += float32(m.Y2)/100*h + 6
					if !m.LabelBelow {
						at.Y = float32(m.Y)/100*h - 26
					}
				} else {
					at.X -= 9
					at.Y -= 9
				}
				c.Box(fmt.Sprint("mark", i), ui.Style{Float: &ui.Float{Offset: at}, Z: ui.LayerContent + 1}, func(c *ui.Ctx) {
					c.Row("m", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
						if m.N > 0 {
							c.Box("n", ui.Style{W: ui.Px(18), H: ui.Px(18), Radius: 9, Fill: accent, Main: ui.Center, Cross: ui.Center, Keep: true}, func(c *ui.Ctx) {
								c.Text("t", fmt.Sprint(m.N), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold},
									Color: th.C(theme.AccentInk)})
							})
						}
						if m.Label != "" {
							c.Row("l", ui.Style{Pad: ui.PadXY(8, 2), Radius: 4, Fill: th.C(theme.BgBase), Stroke: accent, Border: 1.5, Keep: true}, func(c *ui.Ctx) {
								c.Text("t", m.Label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
							})
						}
					})
				})
			}
		})
		if it.Caption != "" {
			c.Text("cap", it.Caption, uiFont(12, th.C(theme.FgMuted)))
		}
	})
}

// rightColumn is the step's diagrams, asking about it, and ← →.
func (x *tourPlugin) rightColumn(c *ui.Ctx, se core.SessionState, st *tourState) {
	th := c.Theme()
	s, has := st.current()
	c.Column("right", ui.Style{W: ui.Px(380), H: ui.Flex(1), Stroke: th.C(theme.Line), Role: ui.RoleRegion, Label: "About this step", Help: tourHelp}, func(c *ui.Ctx) {
		c.Column("scroll", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			for k, it := range s.Items {
				if it.Kind != tour.Diagram {
					continue
				}
				c.Column(fmt.Sprint("diagram", k), ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 8, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
					c.Row("head", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("k", "DIAGRAM", kickerFont(th.C(theme.FgFaint)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("lang", it.Lang, monoFont(10, th.C(theme.FgFaint)))
					})
					c.Column("src", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8), Radius: 4, Fill: th.C(tTourCode), Stroke: th.C(theme.Line), Border: 1, ScrollX: true},
						func(c *ui.Ctx) {
							for i, l := range strings.Split(it.Text, "\n") {
								c.Text(i, expandTabs(l), monoFont(11, th.C(theme.FgMuted)))
							}
						})
					if it.Caption != "" {
						c.Text("cap", it.Caption, uiFont(12, th.C(theme.FgMuted)))
					}
				})
			}
			if !has {
				return
			}
			c.Column("ask", ui.Style{W: ui.Flex(1), Pad: ui.Pad(14), Gap: 10}, func(c *ui.Ctx) {
				c.Text("k", "ASK ABOUT THIS STEP", kickerFont(th.C(theme.FgFaint)))
				for _, t := range stepThreads(se, s.File) {
					x.threadCard(c, se, st, t)
				}
				x.askField(c, se, st, "Ask, or ask to rework this step…")
			})
		})
		if !has {
			return
		}
		c.Row("nav", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
			if widget.Button(c, "prev", "← Prev", widget.ButtonOpts{Ghost: true, Disabled: st.step == 0, Help: tourHelp}) {
				x.run("tour.step", registry.Args{"by": -1})
			}
			c.Row("n", ui.Style{W: ui.Flex(1), Main: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", fmt.Sprintf("step %d / %d", st.step+1, len(st.t.Steps)), monoFont(10.5, th.C(theme.FgFaint)))
			})
			if widget.Button(c, "next", "Next →", widget.ButtonOpts{Primary: true, Disabled: st.step == len(st.t.Steps)-1, Help: tourHelp}) {
				x.run("tour.step", registry.Args{"by": 1})
			}
		})
	})
}

// threadCard is a thread about the tour, as it is between an editor's lines.
func (x *tourPlugin) threadCard(c *ui.Ctx, se core.SessionState, st *tourState, t core.Thread) {
	d := st.replies[t.ID]
	if d == nil {
		d = new(string)
		st.replies[t.ID] = d
	}
	c.Box(string(t.ID), ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		inlineThreadCard(c, x.h, se, t, d, func() {
			if strings.TrimSpace(*d) == "" {
				return
			}
			if err := x.h.Run(context.Background(), "request.comment", registry.Args{"thread": string(t.ID), "body": *d}); err != nil {
				x.notify(err)
				return
			}
			*d = ""
		}, threadCardOpts{})
	})
}

// askField asks about the step shown, ⏎ sending it.
func (x *tourPlugin) askField(c *ui.Ctx, se core.SessionState, st *tourState, placeholder string) {
	if st.focusAsk {
		c.Focus(c.Next("askfield"))
		st.focusAsk = false
	}
	r := widget.Input(c, "askfield", &st.ask, widget.InputOpts{Label: "Ask about this step", Placeholder: placeholder, Help: tourHelp + "#ask"})
	switch {
	case r.Cancelled:
		st.focus = true
	case r.Submitted && strings.TrimSpace(st.ask) != "":
		if err := x.ask(context.Background(), se, st, st.ask); err != nil {
			x.notify(err)
			return
		}
		st.ask = ""
	}
}

func (x *tourPlugin) notify(err error) {
	x.h.Notify(core.Notice{ID: "tour", Severity: core.SevWarning, Source: "tour", Key: true, Text: err.Error()})
}

// run runs a command from a control in the reader.
func (x *tourPlugin) run(cmd string, a registry.Args) {
	if err := x.h.Run(context.Background(), cmd, a); err != nil {
		x.notify(err)
	}
}

// tourSegment is a status line segment shown while the reader has focus.
func tourSegment(x *tourPlugin, fn func(c *ui.Ctx)) registry.ViewFunc {
	return func(registry.ViewCtx) ui.Node {
		if f := x.h.Layout().Focus; f == nil || f.ID != TourView {
			return nil
		}
		if _, ok := x.front(); !ok {
			return nil
		}
		return &ui.Func{Key: "tour", Fn: fn}
	}
}

// statusLeft is "auth flow  walkthrough · 2 repos · 14 quotes".
func (x *tourPlugin) statusLeft(c *ui.Ctx) {
	se, st, _ := x.current()
	t := st.t
	c.Row("tour", ui.Style{H: ui.Flex(1), Gap: 10, Cross: ui.Center, Role: ui.RoleStatus, Label: "Tour"}, func(c *ui.Ctx) {
		c.Text("name", se.Name, statusText(c, tStatusStrong))
		c.Text("what", fmt.Sprintf("%s · %d %s · %d %s", t.Kind, len(t.Repos), plural(len(t.Repos), "repo", "repos"), t.Quotes(),
			plural(t.Quotes(), "quote", "quotes")), statusText(c, tStatusFg))
	})
}

// statusRight is the reader's keys and the step: "h/l step · j/k scroll · gd open quote in editor  4/9".
func (x *tourPlugin) statusRight(c *ui.Ctx) {
	_, st, _ := x.current()
	c.Row("tour", ui.Style{H: ui.Flex(1), Gap: 12, Cross: ui.Center, Role: ui.RoleStatus, Label: "Tour keys"}, func(c *ui.Ctx) {
		c.Text("keys", "h/l step · j/k scroll · gd open quote in editor", statusText(c, tStatusFg))
		if n := len(st.t.Steps); n > 0 {
			c.Text("n", fmt.Sprintf("%d/%d", st.step+1, n), statusText(c, tStatusStrong))
		}
	})
}
