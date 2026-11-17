// Package updates is Oriel updating itself: checking for a new release, downloading and staging it, and restarting
// into it, with its card on the settings page and its own page.
package updates

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit/draw"
	"github.com/liamg/oriel/internal/words"
	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

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
			c.Text("t", "update ready · restart", draw.StatusText(c, theme.Accent))
		})
		if in.Clicked {
			if err := x.h.Run(context.Background(), "update.restart", nil); err != nil {
				kit.NotifyFailure(x.h, core.Notice{ID: "update.restart", AutoClear: true, Title: "Update"}, err)
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
		c.Text("t", text, draw.StatusText(c, kit.TStatusFg))
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
				c.Text("versions", r.from+"  →  "+r.to, widget.MonoFont(12, th.C(theme.FgMuted)))
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
					widget.Wrap(c, "err", r.failed, 600, widget.MonoFont(11, th.C(theme.Error)))
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
							if err := x.update(updateOpts{page: true, restart: r.restart}); err != nil {
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
func (x *Update) drainSteps(c *ui.Ctx, d services.Drain, to string) {
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
		updateStepRow(c, "waiting", updateLine{text: fmt.Sprintf("Waiting for %s to finish %s, at most %s more: %s", words.Counted(len(busy), "session"),
			words.Plural(len(busy), "its turn", "their turns"), left, strings.Join(names, ", ")), state: lineRunning})
	}
}

func updateStepRow(c *ui.Ctx, key any, l updateLine) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Clip: true, Role: ui.RoleText, Label: l.text}, func(c *ui.Ctx) {
		switch l.state {
		case lineOK:
			c.Text("mark", "✓", widget.MonoFont(11.5, th.C(tUpdateOK)))
		case lineFailed:
			c.Text("mark", "✕", widget.MonoFont(11.5, th.C(tUpdateFailed)))
		default:
			widget.Spinner(c, "mark", widget.SpinnerOpts{Size: 11, Tone: th.C(tUpdateRunning)})
		}
		c.Text("text", l.text, widget.MonoFont(11.5, th.C(theme.Fg)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if !l.at.IsZero() {
			c.Text("at", l.at.Local().Format("15:04:05"), widget.MonoFont(10.5, th.C(theme.FgFaint)))
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
			widget.Wrap(c, "none", "This build of Oriel doesn't update itself.", 570, widget.UIFont(12.5, th.C(theme.Fg)))
		case found:
			sub := fmt.Sprintf("you have %s · %s channel", u.Version, x.channel())
			if !m.Published.IsZero() {
				sub += " · released " + m.Published.Format("2 Jan 2006")
			}
			widget.Heading(c, "h", "WHAT'S NEW", sub)
			notes := strings.TrimSpace(m.Notes)
			if notes == "" {
				notes = "This release has no notes."
			}
			c.Column("notes", ui.Style{W: ui.Flex(1), H: ui.Size{Kind: ui.Fit}.Clamp(0, 320), ScrollY: true, Pad: ui.Insets{R: 8}}, func(c *ui.Ctx) {
				kit.MarkdownText(c, x.h, "md", notes, 556, widget.UIFont(12.5, th.C(theme.FgMuted)), kit.MarkdownOpts{})
			})
		case busy != "":
		default:
			widget.Wrap(c, "current", fmt.Sprintf("Oriel %s is the newest on the %s channel.", u.Version, x.channel()), 570,
				widget.UIFont(12.5, th.C(theme.Fg)))
		}
		if busy != "" {
			widget.Busy(c, "busy", widget.LoadState{Phase: widget.Pending, Since: since}, widget.BusyOpts{Label: busy, Help: updateHelp + "#check"})
		}
		if failed != "" {
			widget.Wrap(c, "err", failed, 570, widget.MonoFont(11, th.C(theme.Error)))
		}
		c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			if isReady {
				widget.Note(c, "ready", "Oriel "+ready+" is installed")
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
