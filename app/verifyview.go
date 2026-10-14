package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tVerifyBorder  = theme.DeclareOn("verify.border", "alpha(accent, 35%)", "bg.surface")
	tVerifyKicker  = theme.Declare("verify.kicker", "accent")
	tVerifyTheirs  = theme.Declare("verify.bubble.theirs", "bg.overlay")
	tVerifyMine    = theme.DeclareOn("verify.bubble.mine", "alpha(blue, 26%)", "bg.base")
	tVerifyMineFg  = theme.Declare("verify.bubble.mine.fg", "fg")
	tVerifyAvatar  = theme.Declare("verify.avatar", "brand")
	tVerifyRight   = theme.Declare("verify.right", "ok")
	tVerifyPartly  = theme.Declare("verify.partly", "warn")
	tVerifyWrong   = theme.Declare("verify.wrong", "error")
	tVerifyCurrent = theme.Declare("verify.current", "accent")
	tVerifyPip     = theme.Declare("verify.pip", "bg.strong")
)

func verdictTone(th *theme.Resolved, v string) render.Color {
	switch v {
	case core.VerdictRight:
		return th.C(tVerifyRight)
	case core.VerdictPartly:
		return th.C(tVerifyPartly)
	case core.VerdictWrong:
		return th.C(tVerifyWrong)
	}
	return th.C(tVerifyPip)
}

func (x *verifyExt) run(id string) {
	if err := x.h.Run(context.Background(), id, nil); err != nil {
		x.h.Notify(core.Notice{ID: "verify", Severity: core.SevError, Key: true, Text: err.Error()})
	}
}

// dialog is VERIFY BEFORE RAISING (ui.md 13) over everything, while a quiz is open.
func (x *verifyExt) dialog(c *ui.Ctx) {
	q := x.shown()
	if q == nil {
		return
	}
	th := c.Theme()
	size := c.Size()
	at := render.Point{X: size.X / 2, Y: 44}
	h := max(320, size.Y-88)
	closed := widget.Scrim(c, "verify", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(tScrimOmni), Help: verifyHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if q.focus && q.result != "" {
			c.Focus(box)
			q.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(min(640, size.X-32)), H: ui.Px(h), Fill: th.C(tOmniBg), Stroke: th.C(tVerifyBorder), Border: 1, Radius: 10,
			Clip: true, Trap: true, Interactive: true, Focusable: true, KeyMode: VerifyKeyMode, Float: &ui.Float{Point: &at, Self: render.Point{X: .5}},
			Role: ui.RoleDialog, Label: "Verify before raising", Help: verifyHelp}, func(c *ui.Ctx) {
			x.header(c, q)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.chat(c, q)
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			if q.result == "" {
				x.composer(c, q)
			} else {
				x.outcome(c, q)
			}
			c.Box("rule3", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.footer(c, q)
		})
	})
	if closed {
		x.open = ""
	}
}

func (x *verifyExt) header(c *ui.Ctx, q *quiz) {
	th := c.Theme()
	c.Column("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 18, R: 18, T: 14, B: 12}, Gap: 6}, func(c *ui.Ctx) {
		c.Row("kick", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "VERIFY BEFORE RAISING", kickerFont(th.C(tVerifyKicker)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("by", q.by, monoFont(10.5, th.C(theme.FgFaint)))
			if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close the quiz", Help: verifyHelp, Size: 18}) {
				x.open = ""
			}
		})
		c.Row("title", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("t", "Can you explain this change?", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 16, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			n := max(len(q.qs), quizLength)
			for i := range n {
				tone := th.C(tVerifyPip)
				switch {
				case i < q.at:
					tone = verdictTone(th, q.qs[i].Verdict)
				case i == q.at && i < len(q.qs) && q.result == "":
					tone = th.C(tVerifyCurrent)
				}
				c.Box(i, ui.Style{W: ui.Px(34), H: ui.Px(4), Radius: 2, Fill: tone}, nil)
			}
			c.Text("n", fmt.Sprintf("%d/%d", q.at, n), monoFont(10.5, th.C(theme.FgFaint)))
		})
		widget.Wrap(c, "about", fmt.Sprintf("%s short questions about the diff you're putting your name on. The answers go in the PR as reviewer notes if you want them there.",
			countWord(max(len(q.qs), quizLength))), min(600, c.Size().X-36), uiFont(12, th.C(theme.FgMuted)))
	})
}

// countWord is a small count as a word: "Four".
func countWord(n int) string {
	words := []string{"No", "One", "Two", "Three", "Four", "Five", "Six"}
	if n < len(words) {
		return words[n]
	}
	return fmt.Sprint(n)
}

func (x *verifyExt) chat(c *ui.Ctx, q *quiz) {
	th := c.Theme()
	width := min(640, c.Size().X-32) - 36
	scrolled(c, "chat", &q.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase)},
		ui.Style{Pad: ui.PadXY(18, 16), Gap: 12}, func(c *ui.Ctx, _ func(string) ui.ID) {
			for i := 0; i < len(q.qs) && i <= q.at; i++ {
				qq := q.qs[i]
				c.Column(i, ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
					c.Row("num", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Box("l", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
						c.Text("t", fmt.Sprintf("QUESTION %d", i+1), kickerFont(th.C(theme.FgGhost)))
						c.Box("r", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					})
					x.theirs(c, "q", q, qq.Question, width)
					if qq.Answer != "" {
						c.Row("a", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
							c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
							c.Column("b", ui.Style{Pad: ui.PadXY(12, 9), Radius: 12, Fill: th.C(tVerifyMine), Stroke: th.C(tVerifyBorder), Border: 1}, func(c *ui.Ctx) {
								widget.Rich(c, "t", mdSpans(qq.Answer), width*.74, uiFont(13, th.C(tVerifyMineFg)), widget.RichOpts{})
							})
						})
					}
					if qq.Verdict != "" {
						x.verdict(c, qq, width)
					}
				})
			}
			switch {
			case q.asking:
				x.typing(c, q)
			case q.err != "":
				c.Column("err", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
					widget.Wrap(c, "t", q.err, width, monoFont(11, th.C(theme.Error)))
					if widget.Button(c, "retry", "Try again", widget.ButtonOpts{Help: verifyHelp}) {
						if len(q.qs) == 0 {
							x.run("verify.again")
						} else {
							q.qs[q.at].Answer, q.answer = "", q.qs[q.at].Answer
							q.err = ""
						}
					}
				})
			}
		})
}

// theirs is a bubble from the fork, with its avatar.
func (x *verifyExt) theirs(c *ui.Ctx, key any, q *quiz, text string, width float32) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.End}, func(c *ui.Ctx) {
		c.Box("av", ui.Style{W: ui.Px(22), H: ui.Px(22), Radius: 5, Fill: th.C(tVerifyAvatar), Main: ui.Center, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("c", strings.ToUpper(cmpOr(q.by, "a")[:1]), mono(11, typeset.Bold, th.C(theme.AccentInk)))
		})
		c.Column("b", ui.Style{Pad: ui.PadXY(12, 9), Radius: 12, Fill: th.C(tVerifyTheirs)}, func(c *ui.Ctx) {
			widget.Rich(c, "t", mdSpans(text), width*.74, uiFont(13, th.C(theme.Fg)), widget.RichOpts{})
		})
	})
}

// verdict is the grade on an answer: ✓, ◐ or ✕, what it says, and the file to read.
func (x *verifyExt) verdict(c *ui.Ctx, qq core.QuizQuestion, width float32) {
	th := c.Theme()
	tone := verdictTone(th, qq.Verdict)
	c.Row("v", ui.Style{Gap: 10, Pad: ui.Insets{L: 30}}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: tone}, nil)
		if qq.Verdict == core.VerdictPartly {
			c.Row("g", ui.Style{W: ui.Px(11), H: ui.Px(11), Radius: 5.5, Stroke: tone, Border: 1.5, Clip: true}, func(c *ui.Ctx) {
				c.Box("half", ui.Style{W: ui.Px(5.5), H: ui.Flex(1), Fill: tone}, nil)
			})
		} else {
			g := uiFont(13, tone)
			g.Font.Weight = typeset.Bold
			c.Text("g", verdictGlyph(qq.Verdict), g)
		}
		c.Column("t", ui.Style{Gap: 4, Pad: ui.PadXY(0, 3)}, func(c *ui.Ctx) {
			note := cmpOr(qq.Note, map[string]string{core.VerdictRight: "Right.", core.VerdictPartly: "Partly.", core.VerdictWrong: "Not quite."}[qq.Verdict])
			widget.Rich(c, "note", mdSpans(note), width*.7, uiFont(12, th.C(theme.FgMuted)), widget.RichOpts{})
			if qq.File != "" && qq.Verdict != core.VerdictRight {
				c.Row("file", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("w", "Worth a look:", uiFont(12, th.C(theme.FgFaint)))
					label := filepath.Base(qq.File)
					if qq.Line > 0 {
						label += fmt.Sprintf(":%d", qq.Line)
					}
					if widget.Link(c, "open", label, verifyHelp+"#file") {
						if err := x.openAt(context.Background(), x.shown(), qq.File, qq.Line); err != nil {
							x.h.Notify(core.Notice{ID: "verify", Severity: core.SevError, Key: true, Text: err.Error()})
						}
					}
				})
			}
		})
	})
}

// typing is the fork thinking: three dots in a bubble.
func (x *verifyExt) typing(c *ui.Ctx, q *quiz) {
	th := c.Theme()
	c.Column("typing", ui.Style{Gap: 6}, func(c *ui.Ctx) {
		if len(q.qs) == 0 {
			c.Text("t", "Writing the questions…", uiFont(12, th.C(theme.FgFaint)))
		}
		c.Row("dots", ui.Style{Pad: ui.Insets{L: 30}}, func(c *ui.Ctx) {
			c.Row("b", ui.Style{Pad: ui.PadXY(12, 10), Radius: 12, Gap: 3, Fill: th.C(tVerifyTheirs), Role: ui.RoleStatus, Label: "Thinking…"}, func(c *ui.Ctx) {
				for i, a := range []float32{1, .6, .3} {
					fg := th.C(theme.FgFaint)
					fg.A = uint8(float32(fg.A) * a)
					c.Box(i, ui.Style{W: ui.Px(5), H: ui.Px(5), Radius: 2.5, Fill: fg}, nil)
				}
			})
		})
	})
}

func (x *verifyExt) composer(c *ui.Ctx, q *quiz) {
	c.Row("composer", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 12), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		in := c.Next("answer")
		if q.focus {
			c.Focus(in)
			q.focus = false
		}
		r := widget.Input(c, "answer", &q.answer, widget.InputOpts{Label: "Your answer", Placeholder: "Your answer, in a sentence or two",
			Lines: 4, Help: verifyHelp, KeyMode: VerifyKeyMode, Disabled: len(q.qs) == 0})
		if r.Submitted {
			x.run("verify.send")
		}
		if r.Cancelled {
			x.open = ""
		}
		if widget.Button(c, "send", "Send", widget.ButtonOpts{Primary: true, Help: verifyHelp,
			Disabled: q.asking || strings.TrimSpace(q.answer) == "" || q.at >= len(q.qs)}) {
			x.run("verify.send")
		}
	})
}

// outcome is how it went, once every question is graded: go on with the raise, or take it again.
func (x *verifyExt) outcome(c *ui.Ctx, q *quiz) {
	th := c.Theme()
	v := core.Verification{Questions: q.qs}
	c.Row("outcome", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 12), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Column("sum", ui.Style{W: ui.Flex(1), Gap: 6, Clip: true}, func(c *ui.Ctx) {
			parts := []string{fmt.Sprintf("%d right", v.Count(core.VerdictRight))}
			if n := v.Count(core.VerdictPartly); n > 0 {
				parts = append(parts, fmt.Sprintf("%d partly", n))
			}
			if n := v.Count(core.VerdictWrong); n > 0 {
				parts = append(parts, fmt.Sprintf("%d wrong", n))
			}
			head, tone := "Verified · ", th.C(tVerifyRight)
			switch q.result {
			case core.VerifyFailed:
				head, tone = "Not yet · ", th.C(tVerifyWrong)
			case core.VerifySkipped:
				head, tone = "Skipped · ", th.C(tVerifyPartly)
			}
			c.Text("t", head+strings.Join(parts, " · "), mono(12, typeset.SemiBold, tone))
			if q.result == core.VerifyPassed {
				st := widget.CheckOff
				if q.notes {
					st = widget.CheckOn
				}
				if widget.Check(c, "notes", st, widget.CheckOpts{Text: "Add the answers to the PR as reviewer notes", Plain: true, Help: verifyHelp + "#notes"}) {
					q.notes = !q.notes
				}
			}
		})
		if q.result == core.VerifyFailed {
			if widget.Button(c, "again", "Ask again", widget.ButtonOpts{Help: verifyHelp}) {
				x.run("verify.again")
			}
			return
		}
		label := "Done"
		if q.resume != nil {
			label = "Raise PR"
			if q.resume.cmd == "ship.raise-all" {
				label = "Raise PRs"
			}
		}
		if widget.Button(c, "go", label, widget.ButtonOpts{Primary: true, Help: verifyHelp}) {
			x.run("verify.continue")
		}
	})
}

func (x *verifyExt) footer(c *ui.Ctx, q *quiz) {
	th := c.Theme()
	f, _ := q.pointer()
	c.Row("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(18, 7), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("send", "⌘↵ send", monoFont(11, th.C(theme.FgFaint)))
		if widget.Button(c, "file", "open the file it's asking about", widget.ButtonOpts{Ghost: true, Key: "⌥O", Disabled: f == "", Help: verifyHelp + "#file"}) {
			x.run("verify.open-file")
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if q.result != core.VerifyPassed && q.result != core.VerifySkipped {
			if widget.Button(c, "skip", "Skip verification (logged)", widget.ButtonOpts{Ghost: true, Help: verifyHelp + "#skip"}) {
				x.run("verify.skip")
			}
		}
	})
}

// status is "verify-before-PR: on for this repo" at the right of the status line, while the quiz is open.
func (x *verifyExt) status(c *ui.Ctx) {
	q := x.shown()
	if q == nil {
		return
	}
	sess, ok := x.h.Session(q.session)
	if !ok {
		return
	}
	se := sess.Snapshot()
	where := "on for " + string(se.Kind) + " sessions"
	if ps := raisable(se); len(ps) > 0 {
		switch v, l := x.setting(se, ps[0].Name); {
		case l == registry.LayerProject:
			where = v + " for this repo"
		case l != registry.LayerDefault:
			where = v
		}
	}
	c.Text("verify", "verify-before-PR: "+where, monoFont(11, c.Theme().C(theme.FgFaint)))
}
