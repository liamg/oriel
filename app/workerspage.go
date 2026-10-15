package app

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/wire"
)

// The Workers page (ui.md 45): the workers on the left, the selected one's load, agents, sessions and connection on
// the right, over the main area beside the sidebar.

const (
	workersHelp            = "agent/workers"
	sessionsShownPerWorker = 6
)

func (x *workersExt) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	st := x.h.State()
	rows := x.rows(st)
	widget.Page(c, "workers", &x.open, widget.PageOpts{Over: mainArea(), KeyMode: WorkersKeyMode, Label: "Workers page", Help: workersHelp + "#page"},
		func(c *ui.Ctx) {
			th := c.Theme()
			x.header(c, rows)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.list(c, rows)
				c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
					Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
					sel := x.selected()
					for _, r := range rows {
						if r.w.ID == sel {
							x.detail(c, st, r)
							return
						}
					}
				})
			})
		})
}

func (x *workersExt) header(c *ui.Ctx, rows []workerRow) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "WORKERS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		online := 0
		for _, r := range rows {
			if r.w.Connected {
				online++
			}
		}
		meta := []string{fmt.Sprintf("%d online", online)}
		if off := len(rows) - online; off > 0 {
			meta = append(meta, fmt.Sprintf("%d offline", off))
		}
		bound, fp, failed := x.listening()
		switch {
		case bound != "":
			meta = append(meta, "listening on "+bound, wordsOf(fp))
		case failed != "":
			meta = append(meta, "can't listen: "+failed)
		default:
			meta = append(meta, "not listening for workers elsewhere")
		}
		c.Text("m", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.err != "" {
			c.Text("err", x.err, monoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "add", "+ Add a worker", widget.ButtonOpts{Key: "a", Help: workersHelp + "#add"}) {
			x.run("workers.add", nil)
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: workersHelp + "#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func wordsOf(fp string) string {
	if fp == "" {
		return ""
	}
	return "certificate " + wire.Words(fp)
}

func (x *workersExt) run(id string, a registry.Args) {
	x.err = ""
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
	}
}

// state is how a worker is as its row says it: its tone and words.
func (x *workersExt) state(r workerRow, now time.Time) (string, theme.TokenID) {
	switch {
	case r.w.Connected:
		return "online " + ago(now, r.w.Since), theme.StatusQuiet
	case r.key.Revoked:
		return "revoked", theme.Error
	case r.w.Since.IsZero():
		return "never connected", theme.FgFaint
	}
	return "offline since " + x.when(now, r.w.Since), theme.FgFaint
}

// when is a time as the page says it: "14:02" today, else "Oct 12 14:02".
func (x *workersExt) when(now, t time.Time) string {
	t, now = t.In(time.Local), now.In(time.Local)
	if t.YearDay() == now.YearDay() && t.Year() == now.Year() {
		return t.Format("15:04")
	}
	return t.Format("Jan 2 15:04")
}

func platform(w core.WorkerState) string {
	if w.OS == "" {
		return ""
	}
	return strings.Trim(w.OS+"/"+w.Arch, "/")
}

func (x *workersExt) list(c *ui.Ctx, rows []workerRow) {
	th := c.Theme()
	c.Column("list", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{T: 6}}, func(c *ui.Ctx) {
			sel := 0
			cur := x.selected()
			for i, r := range rows {
				if r.w.ID == cur {
					sel = i
				}
			}
			res := widget.Rows(c, "workers", len(rows), &sel, widget.RowsOpts{Label: "Workers list", KeyMode: WorkersKeyMode, Help: workersHelp + "#list",
				RowLabel: func(i int) string { return rows[i].w.Title() }}, func(c *ui.Ctx, i int, _ bool) {
				x.rowView(c, rows[i], rows[i].w.ID == cur)
			})
			if (res.Changed || res.Activated >= 0) && sel < len(rows) && rows[sel].w.ID != x.sel {
				x.sel, x.confirm = rows[sel].w.ID, ""
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 3}, func(c *ui.Ctx) {
			note(c, "a", "keys: "+tildePath(x.path))
			note(c, "b", "hashed, readable by you alone")
			if bound, _, _ := x.listening(); bound == "" {
				note(c, "c", "workers.listen is off until you add one")
			} else {
				note(c, "c", "workers dial "+dialAddr(bound))
			}
		})
	})
}

func (x *workersExt) rowView(c *ui.Ctx, r workerRow, on bool) {
	th := c.Theme()
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(tStatsRowOn), th.C(theme.Accent)
	}
	words, tone := x.state(r, c.Now())
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 5, Clip: true}, func(c *ui.Ctx) {
			c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tone)}, nil)
				c.Text("t", r.w.Title(), uiFont(13, th.C(theme.Fg)))
				if r.local() {
					c.Text("this", "this machine", monoFont(10, th.C(theme.FgGhost)))
				}
			})
			parts := slices.DeleteFunc([]string{r.w.Host, platform(r.w), words}, func(s string) bool { return s == "" })
			c.Text("l2", strings.Join(parts, " · "), monoFont(10, th.C(theme.FgFaint)))
		})
	})
}

func (x *workersExt) detail(c *ui.Ctx, st core.State, r workerRow) {
	th := c.Theme()
	w := r.w
	help := workersHelp + "#worker"
	c.Row("title", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", w.Title(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		if own := cmp.Or(w.Name, string(w.ID)); own != w.Title() {
			c.Text("own", own, monoFont(11, th.C(theme.FgGhost)))
		}
		if r.local() {
			widget.Chip(c, "this", "this machine", th.C(theme.FgMuted), help)
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		args := registry.Args{"name": string(w.ID)}
		if widget.Button(c, "rename", "Rename", widget.ButtonOpts{Key: "r", Help: help}) {
			x.run("workers.rename", args)
		}
		if r.local() {
			if widget.Button(c, "restart", "Restart", widget.ButtonOpts{Help: workersHelp + "#restart", Disabled: !x.h.Available("workers.restart", nil)}) {
				x.run("workers.restart", nil)
			}
			return
		}
		if r.key.remote() && !r.key.Revoked {
			x.confirmButton(c, "revoke", "Revoke", "workers.revoke", args, w.ID)
		}
		if !w.Connected {
			x.confirmButton(c, "forget", "Forget", "workers.forget", args, w.ID)
		}
	})
	words, tone := x.state(r, c.Now())
	c.Row("meta", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		faint := th.C(theme.FgFaint)
		i := 0
		item := func(s string, col render.Color) {
			if s == "" {
				return
			}
			if i > 0 {
				c.Text(fmt.Sprint("d", i), "·", monoFont(11, th.C(theme.FgGhost)))
			}
			c.Text(fmt.Sprint("m", i), s, monoFont(11, col))
			i++
		}
		item(w.Host, th.C(theme.Fg))
		item(platform(w), faint)
		item(w.Version, faint)
		if w.Build != "" {
			item("build "+w.Build[:min(8, len(w.Build))], faint)
		}
		item(words, th.C(tone))
	})
	c.Row("cards1", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		x.loadCard(c, w)
		x.agentsCard(c, st, w)
	})
	c.Row("cards2", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
		x.sessionsCard(c, st, w)
		x.connectionCard(c, r)
	})
}

// confirmButton is an action that asks "Sure?" once.
func (x *workersExt) confirmButton(c *ui.Ctx, key, label, command string, args registry.Args, id core.WorkerID) {
	ask := key + ":" + string(id)
	if x.confirm == ask {
		label = "Sure?"
	}
	if widget.Button(c, key, label, widget.ButtonOpts{Ghost: true, Help: workersHelp + "#" + key}) {
		if x.confirm == ask {
			x.confirm = ""
			x.run(command, args)
		} else {
			x.confirm = ask
		}
	}
}

func (x *workersExt) loadCard(c *ui.Ctx, w core.WorkerState) {
	th := c.Theme()
	sampled := ""
	if w.Connected && !w.Sampled.IsZero() {
		sampled = "sampled " + ago(c.Now(), w.Sampled)
	}
	statsCard(c, "load", ui.Flex(1), "LOAD", sampled, nil, func(c *ui.Ctx) {
		if !w.Connected || w.Sampled.IsZero() {
			note(c, "none", "no reading while it's offline")
			return
		}
		meter := func(key, label, value string, frac float64) {
			c.Column(key, ui.Style{W: ui.Flex(1), Gap: 4}, func(c *ui.Ctx) {
				c.Row("l", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
					c.Text("k", label, capText(th.C(theme.FgFaint)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("v", value, monoFont(11, th.C(theme.Fg)))
				})
				tone := th.C(theme.StatusQuiet)
				if frac >= .9 {
					tone = th.C(theme.Warn)
				}
				widget.Meter(c, "m", frac, widget.MeterOpts{Tone: tone, Label: label + " " + value})
			})
		}
		cpu := fmt.Sprintf("%d%%", pct(w.CPU))
		if w.Cores > 0 {
			cpu += fmt.Sprintf(" of %d cores", w.Cores)
		}
		meter("cpu", "CPU", cpu, w.CPU)
		if w.MemTotal > 0 {
			meter("mem", "MEMORY", fmt.Sprintf("%s of %s", gib(w.Mem), gib(w.MemTotal)), float64(w.Mem)/float64(w.MemTotal))
		}
	})
}

func gib(b uint64) string { return fmt.Sprintf("%.1f GB", float64(b)/(1<<30)) }

func (x *workersExt) agentsCard(c *ui.Ctx, st core.State, w core.WorkerState) {
	th := c.Theme()
	providers := slices.Clone(w.Providers)
	add := func(p string) {
		if !slices.Contains(providers, p) {
			providers = append(providers, p)
		}
	}
	for _, u := range st.Usage {
		if u.Uses(w.ID) {
			add(u.Provider)
		}
	}
	for _, l := range w.SignedOut {
		add(l.Provider)
	}
	statsCard(c, "agents", ui.Flex(1), "AGENTS", "", nil, func(c *ui.Ctx) {
		if len(providers) == 0 {
			note(c, "none", "none reported")
			return
		}
		for _, p := range providers {
			c.Row(p, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("n", providerName(p), uiFont(12.5, th.C(theme.Fg)))
				if i := slices.IndexFunc(w.SignedOut, func(l core.Login) bool { return l.Provider == p }); i >= 0 {
					c.Text("out", "signed out: "+w.SignedOut[i].Detail, monoFont(10.5, th.C(theme.Error)))
					return
				}
				for _, u := range st.Usage {
					if u.Provider == p && u.Uses(w.ID) {
						c.Text("acct", cmp.Or(u.AccountName, u.Account, "signed in"), monoFont(10.5, th.C(theme.FgFaint)))
						if u.Tier != "" {
							widget.Chip(c, "tier", u.Tier, th.C(theme.Accent), workersHelp+"#worker")
						}
						return
					}
				}
				c.Text("acct", "no login seen yet", monoFont(10.5, th.C(theme.FgGhost)))
			})
		}
	})
}

func (x *workersExt) sessionsCard(c *ui.Ctx, st core.State, w core.WorkerState) {
	th := c.Theme()
	var mine []core.SessionState
	running := 0
	for _, se := range st.Sessions {
		if cmp.Or(se.Worker, core.LocalWorker) != w.ID || se.Archived {
			continue
		}
		mine = append(mine, se)
		if se.Agent.Running {
			running++
		}
	}
	statsCard(c, "sessions", ui.Flex(1), "SESSIONS", fmt.Sprintf("%s · %d agents running", counted(len(mine), "session"), running), nil, func(c *ui.Ctx) {
		if len(mine) == 0 {
			msg := "none"
			if !w.Connected {
				msg = "its sessions show while it's online"
			}
			note(c, "none", msg)
			return
		}
		for i, se := range mine {
			if i == sessionsShownPerWorker {
				note(c, "more", fmt.Sprintf("%d more", len(mine)-i))
				break
			}
			status, tone := consoleStatus(se)
			c.Row(string(se.ID), ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(tone)}, nil)
				c.Text("n", cmp.Or(se.Name, string(se.ID)), monoFont(11.5, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				c.Text("s", status, monoFont(10, th.C(theme.FgFaint)))
			})
		}
	})
}

func (x *workersExt) connectionCard(c *ui.Ctx, r workerRow) {
	th := c.Theme()
	statsCard(c, "conn", ui.Flex(1), "CONNECTION", "", nil, func(c *ui.Ctx) {
		line := func(key, s string) { c.Text(key, s, monoFont(10.5, th.C(theme.FgFaint))) }
		if r.local() {
			line("how", "on ~/.oriel/worker.sock, owner only")
			line("life", "Oriel starts it, and stops it as Oriel quits")
			return
		}
		switch {
		case r.key.Revoked:
			line("how", "its key is revoked: it's refused")
		case r.key.Hash != "":
			line("how", "dials Oriel with its key · pinned TLS")
		default:
			line("how", "has no key here")
		}
		if !r.key.Added.IsZero() {
			line("added", "added "+x.when(c.Now(), r.key.Added))
		}
		if r.key.Addr != "" {
			line("addr", "last from "+r.key.Addr)
		}
		for i, f := range x.refusals(r.w.ID) {
			if i == 3 {
				break
			}
			c.Text(fmt.Sprint("ref", i), "refused "+x.when(c.Now(), f.at)+": "+f.reason, monoFont(10, th.C(theme.Error)))
		}
	})
}

// The dialog: Add a worker, then what to run there; or Rename.

func (x *workersExt) dialog(c *ui.Ctx) {
	d := x.dlg
	if d == nil {
		return
	}
	open := true
	title := "Add a worker"
	if d.rename != "" {
		title = "Rename " + x.title(d.rename)
	}
	widget.Modal(c, "workersdlg", &open, title, widget.ModalOpts{Width: 600, Help: workersHelp + "#add"}, func(c *ui.Ctx) {
		switch {
		case d.added != nil:
			x.pairingBody(c, d.added)
		case d.rename != "":
			x.renameBody(c, d)
		default:
			x.addBody(c, d)
		}
	})
	if !open {
		x.dlg = nil
	}
}

// field is a text field of the dialog's: Tab moves on, Escape closes, ⏎ and ⌘↵ submit.
func (x *workersExt) field(c *ui.Ctx, key string, text *string, label, placeholder string, focus bool, o widget.InputOpts) {
	if focus {
		c.Focus(c.Next(key))
	}
	o.Label, o.Placeholder, o.Help, o.TabMoves, o.KeyMode = label, placeholder, workersHelp+"#add", true, WorkerDialogMode
	res := widget.Input(c, key, text, o)
	switch {
	case res.Cancelled:
		x.dlg = nil
	case res.Submitted:
		x.submit()
	case res.Changed && x.dlg != nil:
		x.dlg.invalid = ""
	}
}

func (x *workersExt) addBody(c *ui.Ctx, d *workerDialog) {
	th := c.Theme()
	focus := d.focus
	d.focus = false
	c.Column("name", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		heading(c, "h", "Name", "how Oriel knows it; the worker connects as it")
		x.field(c, "name", &d.name, "Name", "devbox", focus, widget.InputOpts{Invalid: d.invalid != ""})
	})
	if v, _, _ := x.h.Options().Get("workers.listen", registry.At{}); v == "" {
		c.Column("listen", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			heading(c, "h", "Listen on", "saved as workers.listen")
			x.field(c, "listen", &d.listen, "Listen on", defaultListen, false, widget.InputOpts{})
			note(c, "n", "Oriel isn't listening for workers yet: it listens here from now on")
		})
	}
	if d.invalid != "" {
		widget.Wrap(c, "err", d.invalid, 540, monoFont(11, th.C(theme.Error)))
	}
	x.foot(c, func(c *ui.Ctx) {
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: workersHelp + "#add"}) {
			x.dlg = nil
		}
		if widget.Button(c, "ok", "Add", widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: workersHelp + "#add"}) {
			x.submit()
		}
	})
}

func (x *workersExt) renameBody(c *ui.Ctx, d *workerDialog) {
	th := c.Theme()
	focus := d.focus
	d.focus = false
	c.Column("name", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		heading(c, "h", "Name", "Oriel's name for it; the worker keeps its own, "+string(d.rename))
		x.field(c, "name", &d.name, "Name", string(d.rename), focus, widget.InputOpts{SelectOnFocus: true, Invalid: d.invalid != ""})
	})
	if d.invalid != "" {
		widget.Wrap(c, "err", d.invalid, 540, monoFont(11, th.C(theme.Error)))
	}
	x.foot(c, func(c *ui.Ctx) {
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: workersHelp + "#worker"}) {
			x.dlg = nil
		}
		if widget.Button(c, "ok", "Save", widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: workersHelp + "#worker"}) {
			x.submit()
		}
	})
}

func (x *workersExt) pairingBody(c *ui.Ctx, p *pairing) {
	th := c.Theme()
	help := workersHelp + "#add"
	c.Column("run", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		heading(c, "h", "Run this on "+string(p.name), "the key is shown this once")
		c.Column("cmd", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 8), Fill: th.C(theme.BgSurface), Stroke: th.C(theme.Line), Border: 1, Radius: 6,
			Role: ui.RoleText, Label: p.command}, func(c *ui.Ctx) {
			widget.Wrap(c, "t", p.command, 530, monoFont(11, th.C(theme.Fg)))
		})
		c.Row("copy", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			label := "Copy"
			if p.copied {
				label = "Copied"
			}
			if widget.Button(c, "copy", label, widget.ButtonOpts{Help: help}) {
				x.sh.desktop.SetClipboard(p.command)
				p.copied = true
			}
			note(c, "n", "on the other machine, with oriel installed")
		})
	})
	c.Column("check", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		heading(c, "h", "Check", "")
		widget.Wrap(c, "t", "The worker pins Oriel's certificate by its fingerprint, and prints it as words as it starts. They should read:",
			540, uiFont(12, th.C(theme.FgMuted)))
		c.Text("words", p.words, monoFont(12.5, th.C(theme.Accent)))
	})
	var w core.WorkerState
	for _, o := range x.h.State().Workers {
		if o.ID == p.name {
			w = o
		}
	}
	c.Row("status", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		if !w.Connected {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.StatusWorking)}, nil)
			c.Text("t", "Waiting for "+string(p.name)+" to connect…", uiFont(12.5, th.C(theme.FgMuted)))
			return
		}
		c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(theme.StatusQuiet)}, nil)
		parts := slices.DeleteFunc([]string{w.Title() + " connected", platform(w), w.Version}, func(s string) bool { return s == "" })
		c.Text("t", strings.Join(parts, " · "), uiFont(12.5, th.C(theme.Fg)))
	})
	x.foot(c, func(c *ui.Ctx) {
		if widget.Button(c, "ok", "Done", widget.ButtonOpts{Primary: true, Key: "esc", Help: help}) {
			x.dlg = nil
		}
	})
}

func (x *workersExt) foot(c *ui.Ctx, buttons func(c *ui.Ctx)) {
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center, Main: ui.End, Pad: ui.Insets{T: 6}}, buttons)
}
