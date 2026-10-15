package app

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/dap"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

const debugHelp = "editor/debug"

// debugPanel is the Debug panel's own state.
type debugPanel struct {
	frameSel    int
	bpSel       int
	watchSel    int
	watchText   string
	replText    string
	consoleSeen int          // the console's length when it last scrolled to its end
	watchVals   []watchValue // the watches' values in the selected frame
}

type debugView struct{ x *debugExt }

func (v debugView) Title(string) string { return "Debug" }
func (v debugView) EdgeToEdge()         {}
func (v debugView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "debug", Fn: v.x.panelView}
}

func debugMono(c *ui.Ctx, size float32, tok theme.TokenID) ui.TextStyle {
	return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: c.Theme().C(tok)}
}

func debugHeading(c *ui.Ctx, key any, label, note string) {
	th := c.Theme()
	c.Row(key, ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
		c.Text("t", label, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .12},
			Color: th.C(theme.FgFaint)})
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if note != "" {
			c.Text("n", note, debugMono(c, 10, theme.FgGhost))
		}
	})
}

// panelView is the toolbar over three columns: the call stack and breakpoints, variables and watches, and the debug
// console (08-debug).
func (x *debugExt) panelView(c *ui.Ctx) {
	th := c.Theme()
	c.Column("debug", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(tExplorerBg), Help: debugHelp, Role: ui.RoleRegion, Label: "Debug"}, func(c *ui.Ctx) {
		x.toolbar(c)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			c.Column("left", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.stackList(c)
				x.breakpointList(c)
			})
			c.Box("e1", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("mid", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.variables(c)
				x.watchList(c)
			})
			c.Box("e2", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("right", ui.Style{W: ui.Flex(1.3), H: ui.Flex(1)}, func(c *ui.Ctx) {
				x.consoleView(c)
				x.accessView(c)
			})
		})
	})
}

// stateLabel is "PAUSED · BREAKPOINT", "RUNNING"…
func (x *debugExt) stateLabel() string {
	s := x.sess
	if s == nil {
		return "NOT RUNNING"
	}
	if s.state == debugPaused {
		return strings.ToUpper("paused · " + s.stop.Reason)
	}
	if s.state == debugEnded && s.exit != nil {
		return fmt.Sprintf("ENDED · EXIT %d", *s.exit)
	}
	return strings.ToUpper(s.state)
}

func (x *debugExt) toolbar(c *ui.Ctx) {
	th := c.Theme()
	s := x.sess
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center, Stroke: th.C(theme.Line)}, func(c *ui.Ctx) {
		tok := theme.FgFaint
		if s != nil && s.state == debugPaused {
			tok = tDebugPaused
		}
		c.Text("state", "● "+x.stateLabel(), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold, Tracking: .1},
			Color: th.C(tok)})
		if !s.live() {
			label := "Start debugging"
			if cfg, ok := x.inferred(); ok {
				label = cfg.Name
			}
			if widget.Button(c, "start", "▶ "+label, widget.ButtonOpts{Help: debugHelp, Key: "F5"}) {
				x.run("debug.continue", nil)
			}
		} else {
			for _, b := range []struct{ glyph, label, cmd string }{
				{"▶", "Continue (F5)", "debug.continue"}, {"‖", "Pause (F6)", "debug.pause"}, {"↪", "Step over (F10)", "debug.step.over"},
				{"↓", "Step into (F11)", "debug.step.into"}, {"↑", "Step out (⇧F11)", "debug.step.out"},
				{"↻", "Restart (⇧⌘F5)", "debug.restart"}, {"■", "Stop (⇧F5)", "debug.stop"},
			} {
				if widget.IconButton(c, b.cmd, b.glyph, widget.IconButtonOpts{Label: b.label, Help: debugHelp, Size: 22}) {
					x.run(b.cmd, nil)
				}
			}
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if s != nil {
			c.Text("adapter", strings.ToUpper(s.spec.Name+" dap · "+cmpOr(s.cfg.Name, s.cfg.Program)), debugMono(c, 10, theme.Accent))
			if s.thread != 0 && s.state == debugPaused {
				c.Text("thread", fmt.Sprintf("thread %d", s.thread), debugMono(c, 10.5, theme.FgMuted))
			}
		}
	})
}

func (x *debugExt) run(cmd string, args registry.Args) {
	if err := x.h.Run(context.Background(), cmd, args); err != nil {
		x.print("error", err.Error())
	}
}

func (x *debugExt) stackList(c *ui.Ctx) {
	s := x.sess
	var frames []dap.Frame
	thread := ""
	if s != nil && s.state == debugPaused {
		frames = s.frames
		thread = fmt.Sprintf("thread %d", s.thread)
		x.panel.frameSel = s.frame
	}
	c.Column("stack", ui.Style{W: ui.Flex(1), H: ui.Flex(1.4)}, func(c *ui.Ctx) {
		debugHeading(c, "h", "CALL STACK", thread)
		if len(frames) == 0 {
			c.Text("none", "Not paused.", debugMono(c, 11, theme.FgGhost))
			return
		}
		res := widget.Rows(c, "frames", len(frames), &x.panel.frameSel, widget.RowsOpts{Help: debugHelp, Label: "Call stack", Height: ui.Flex(1),
			RowLabel: func(i int) string { return frames[i].Name }},
			func(c *ui.Ctx, i int, sel bool) {
				f := frames[i]
				tok := theme.Fg
				if f.Hint == "subtle" {
					tok = theme.FgFaint
				}
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(22), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("n", f.Name, debugMono(c, 11.5, tok))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					if f.Path != "" {
						c.Text("at", fmt.Sprintf("%s:%d", filepath.Base(f.Path), f.Line+1), debugMono(c, 10.5, theme.FgFaint))
					}
				})
			})
		if res.Activated >= 0 && res.Activated != s.frame {
			x.selectFrame(res.Activated)
		}
	})
}

// bpRow is a breakpoint as the list shows it.
type bpRow struct {
	file string
	i    int
	bp   debugBreakpoint
}

func (x *debugExt) bpRows() []bpRow {
	var out []bpRow
	files := make([]string, 0, len(x.bps))
	for f := range x.bps {
		files = append(files, f)
	}
	slices.Sort(files)
	for _, f := range files {
		for i, b := range x.bps[f] {
			out = append(out, bpRow{f, i, b})
		}
	}
	return out
}

func (x *debugExt) breakpointList(c *ui.Ctx) {
	rows := x.bpRows()
	c.Column("bps", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		debugHeading(c, "h", "BREAKPOINTS", x.counts())
		if len(rows) == 0 {
			c.Text("none", "No breakpoints: click a line number, or ␣db.", debugMono(c, 11, theme.FgGhost))
			return
		}
		res := widget.Rows(c, "list", len(rows), &x.panel.bpSel, widget.RowsOpts{Help: debugHelp, Label: "Breakpoints", Height: ui.Flex(1),
			ClickSelects: true,
			RowLabel:     func(i int) string { return fmt.Sprintf("%s:%d", filepath.Base(rows[i].file), rows[i].bp.Line+1) }},
			func(c *ui.Ctx, i int, sel bool) {
				r := rows[i]
				tok, note := tBreakpoint, r.bp.note()
				hits := "–"
				if st, ok := x.stateOf(r.file, r.i); ok {
					if !st.Verified {
						tok = tBreakpointFaint
					}
					if n := x.sess.hits[st.ID]; n > 0 {
						hits = fmt.Sprintf("hit %d", n)
					}
				}
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(22), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("g", r.bp.glyph(), debugMono(c, 11, tok))
					c.Text("at", fmt.Sprintf("%s:%d", filepath.Base(r.file), r.bp.Line+1), debugMono(c, 11.5, theme.Fg))
					if note != "" {
						c.Text("note", "· "+note, debugMono(c, 11, theme.FgMuted))
					}
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					c.Text("hits", hits, debugMono(c, 10.5, theme.FgFaint))
					if widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Remove the breakpoint", Help: debugHelp, Size: 18}) {
						x.remove(r.file, r.bp.Line)
					}
				})
			})
		if res.Activated >= 0 && res.Activated < len(rows) {
			r := rows[res.Activated]
			x.run("editor.open", registry.Args{"path": r.file, "line": r.bp.Line + 1})
		}
	})
}

// variableItems are the tree's rows for variables, their children as fetched; an unfetched structured one has a
// placeholder child so it can be opened, and refs records what each id expands.
func (x *debugExt) variableItems(prefix string, vs []dap.Variable, refs map[string]int, depth int) []widget.TreeItem {
	th := x.h.CurrentTheme()
	var out []widget.TreeItem
	for i, v := range vs {
		id := fmt.Sprintf("%s/%d:%s", prefix, i, v.Name)
		it := widget.TreeItem{ID: id, Label: v.Name, Note: strings.TrimSpace(v.Type + " " + v.Value), Help: debugHelp}
		if v.Ref > 0 && depth < 12 {
			refs[id] = v.Ref
			if kids, ok := x.sess.vars[v.Ref]; ok && len(kids) > 0 {
				it.Children = x.variableItems(id, kids, refs, depth+1)
			} else {
				it.Children = []widget.TreeItem{{ID: id + "/…", Label: "…", Tone: th.C(theme.FgGhost), Help: debugHelp}}
			}
		}
		out = append(out, it)
	}
	return out
}

func (x *debugExt) variables(c *ui.Ctx) {
	s := x.sess
	c.Column("vars", ui.Style{W: ui.Flex(1), H: ui.Flex(1.4)}, func(c *ui.Ctx) {
		var items []widget.TreeItem
		refs := map[string]int{}
		note := ""
		if s != nil && s.state == debugPaused {
			for i, sc := range s.scopes {
				id := fmt.Sprintf("scope%d", i)
				it := widget.TreeItem{ID: id, Label: sc.Name, Section: true, Open: i == 0, Help: debugHelp}
				if vs, ok := s.vars[sc.Ref]; ok {
					it.Children = x.variableItems(id, vs, refs, 0)
				} else {
					refs[id] = sc.Ref
					it.Children = []widget.TreeItem{{ID: id + "/…", Label: "…", Help: debugHelp}}
				}
				items = append(items, it)
			}
			if f, ok := s.top(); ok {
				note = f.Name
			}
		}
		debugHeading(c, "h", "VARIABLES", note)
		if len(items) == 0 {
			c.Text("none", "Not paused.", debugMono(c, 11, theme.FgGhost))
			return
		}
		res := widget.Tree(c, "tree", items, widget.TreeOpts{Help: debugHelp, Label: "Variables", Height: ui.Flex(1)})
		for _, id := range []string{res.Toggled, res.Activated} {
			if ref, ok := refs[id]; ok {
				x.expand(ref)
			}
		}
	})
}

func (x *debugExt) watchList(c *ui.Ctx) {
	c.Column("watch", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		debugHeading(c, "h", "WATCH", "")
		vals := x.panel.watchVals
		if len(x.watches) > 0 {
			widget.Rows(c, "list", len(x.watches), &x.panel.watchSel, widget.RowsOpts{Help: debugHelp, Label: "Watch", ClickSelects: true,
				RowLabel: func(i int) string { return x.watches[i] }},
				func(c *ui.Ctx, i int, sel bool) {
					val, tok := "", theme.Fg
					if i < len(vals) {
						val = vals[i].Value
						if vals[i].Err {
							tok = theme.FgFaint
						}
					}
					c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(22), Pad: ui.PadXY(10, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("e", x.watches[i], debugMono(c, 11.5, theme.Accent))
						c.Text("v", val, debugMono(c, 11.5, tok))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Stop watching", Help: debugHelp, Size: 18}) {
							x.run("debug.watch.remove", registry.Args{"index": i})
						}
					})
				})
		}
		c.Row("add", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 4)}, func(c *ui.Ctx) {
			if widget.Input(c, "in", &x.panel.watchText, widget.InputOpts{Label: "Add a watch", Placeholder: "+ watch an expression",
				Help: debugHelp}).Submitted {
				if x.addWatch(x.panel.watchText) == nil {
					x.panel.watchText = ""
				}
			}
		})
	})
}

func (x *debugExt) consoleView(c *ui.Ctx) {
	note := ""
	if s := x.sess; s != nil {
		note = s.spec.Name
		if s.state == debugPaused {
			note += fmt.Sprintf(" · frame %d", s.frame)
		}
	}
	c.Column("console", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
		debugHeading(c, "h", "DEBUG CONSOLE", note)
		box := c.Next("lines")
		var last ui.ID
		c.Column("lines", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(10, 6), Gap: 2, ScrollY: true, Clip: true}, func(c *ui.Ctx) {
			for i, l := range x.console {
				last = c.Next(i)
				tok, text := theme.FgMuted, l.Text
				switch l.Kind {
				case "input":
					tok, text = theme.Accent, "› "+text
				case "agent":
					tok, text = theme.Accent, "agent › "+text
				case "result":
					tok = theme.Fg
				case "error", "stderr":
					tok = theme.Error
				case "console", "important":
					tok = theme.FgFaint
				}
				c.Text(i, text, debugMono(c, 11.5, tok))
			}
		})
		if n := len(x.console); n != x.panel.consoleSeen && n > 0 {
			x.panel.consoleSeen = n
			c.ScrollIntoView(box, last)
		}
		c.Row("repl", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 4)}, func(c *ui.Ctx) {
			disabled := x.client() == nil
			if widget.Input(c, "in", &x.panel.replText, widget.InputOpts{Label: "Evaluate", Placeholder: "› evaluate in the selected frame",
				Help: debugHelp, Disabled: disabled}).Submitted {
				if x.eval(x.panel.replText) == nil {
					x.panel.replText = ""
				}
			}
		})
	})
}

// accessView is what agents may do in the debugger, a chip each: a click moves it on from allow to ask to off.
func (x *debugExt) accessView(c *ui.Ctx) {
	th := c.Theme()
	debugHeading(c, "access-h", "AGENT'S DEBUGGER ACCESS", "")
	c.Row("access", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(10, 6), Gap: 6, Cross: ui.Center, ScrollX: true}, func(c *ui.Ctx) {
		for _, a := range debugAccesses {
			level := x.level(a.Action)
			tone, label := th.C(tDebugAllow), a.Label
			switch level {
			case "ask":
				tone, label = th.C(tDebugAsk), label+" · ask"
			case "off":
				tone, label = th.C(tDebugOff), label+" · off"
			}
			on := level == "allow"
			if widget.ToggleChip(c, a.Action, strings.ToUpper(label), on, widget.ToggleChipOpts{Tone: tone, Help: debugHelp + "#agent",
				Label: "Agents may " + a.Help + ": " + level, Tooltip: "Allow, ask or off: click for the next"}) != on {
				x.run("debug.agent.access", registry.Args{"action": a.Action})
			}
		}
	})
}

// statusSegment is where the debugger is, and the breakpoints: "● paused round.go:42 · 3 breakpoints · 1 conditional".
func (x *debugExt) statusSegment(c *ui.Ctx) {
	where := ""
	s := x.sess
	switch {
	case s == nil || s.state == debugEnded:
	case s.state == debugPaused:
		where = "paused"
		if f, ok := s.top(); ok && f.Path != "" {
			where += fmt.Sprintf(" %s:%d", filepath.Base(f.Path), f.Line+1)
		}
	default:
		where = s.state
	}
	counts := x.counts()
	if where == "" && counts == "" {
		return
	}
	c.Row("debug", ui.Style{H: ui.Flex(1), Gap: 10, Cross: ui.Center, Help: debugHelp, Role: ui.RoleStatus, Label: "Debugger"}, func(c *ui.Ctx) {
		if where != "" {
			c.Text("w", "● "+where, statusText(c, tDebugPaused))
		}
		if counts != "" {
			c.Text("n", counts, statusText(c, tStatusFg))
		}
	})
}
