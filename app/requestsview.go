package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

type requestsList struct{ x *requests }

func (v requestsList) Title(string) string { return "Requests" }
func (v requestsList) EdgeToEdge()         {}
func (v requestsList) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "requests", Fn: func(c *ui.Ctx) { v.x.listView(c, vc) }}
}

type requestView struct{ x *requests }

func (v requestView) Title(string) string {
	se, ok := v.x.front()
	if !ok {
		return "Request"
	}
	st := v.x.state(se.ID)
	if t, ok := se.Thread(st.sel); ok && !st.writing {
		return truncate(t.Heading(), 40)
	}
	return "New request"
}
func (v requestView) EdgeToEdge() {}
func (v requestView) Build(vc registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "request", Fn: func(c *ui.Ctx) { v.x.requestView(c, vc) }}
}

func noSessionNote(c *ui.Ctx) {
	c.Column("none", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Main: ui.Center, Cross: ui.Center, Pad: ui.Pad(16)}, func(c *ui.Ctx) {
		c.Text("t", "No session in front: its requests show here.", uiFont(12.5, c.Theme().C(theme.FgFaint)))
	})
}

// listView is the Requests view (21's left): every request grouped by what it waits on, filters, search, and New
// request.
func (x *requests) listView(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	root := c.Next("requests")
	c.Column("requests", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgSurface), Role: ui.RoleRegion, Label: "Requests",
		Help: "agent/requests"}, func(c *ui.Ctx) {
		if !ok {
			noSessionNote(c)
			return
		}
		st := x.state(se.ID)
		sel, _ := x.selection(se, st)
		c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 12, B: 10}, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("k", "REQUESTS", kickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if k := boundKey(x.h, "view.toggle", registry.Args{"id": RequestsView, "place": "dock", "edge": "left"}); k != "" {
				c.Text("keys", k, monoFont(10, th.C(theme.FgGhost)))
			}
		})
		x.filters(c, se, st, vc.Width)
		c.Box("search", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 8, B: 10}}, func(c *ui.Ctx) {
			widget.Input(c, "q", &st.query, widget.InputOpts{Label: "Search requests", Placeholder: "⌕ search requests…", Help: "agent/requests#search"})
		})
		listID := c.Next("list")
		if vc.Focused && !c.FocusWithin(root) || st.focusList {
			c.Focus(listID)
			st.focusList = false
		}
		vs := x.visible(se, st)
		now := c.Now()
		scrolled(c, "list", &st.list, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: RequestsKeyMode, Role: ui.RoleList,
			Label: "Requests", Help: "agent/requests#list"}, ui.Style{Pad: ui.Insets{B: 8}}, func(c *ui.Ctx, block func(string) ui.ID) {
			if len(vs) == 0 {
				c.Box("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12)}, func(c *ui.Ctx) {
					widget.Wrap(c, "t", "No requests yet. Write one below, or ask about a line from the editor.", max(vc.Width-32, 120),
						uiFont(12, th.C(theme.FgFaint)))
				})
			}
			for g := groupWorking; g <= groupResolved; g++ {
				var in []core.Thread
				for _, t := range vs {
					if groupOf(t) == g {
						in = append(in, t)
					}
				}
				if len(in) == 0 {
					continue
				}
				c.Row(fmt.Sprint("g", g), ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16, R: 16, T: 12, B: 6}, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Text("k", strings.ToUpper(groupNames[g]), kickerFont(th.C(groupTokens[g])))
					if g == groupResolved {
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						c.Text("n", fmt.Sprint(len(in)), monoFont(10, th.C(theme.FgFaint)))
					}
				})
				for _, t := range in {
					block(string(t.ID))
					x.listRow(c, se, st, t, !st.writing && t.ID == sel.ID, now)
				}
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Row("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 10), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.Button(c, "new", "New request", widget.ButtonOpts{Primary: true, Help: "agent/requests#new-request"}) {
				x.run("request.new", nil)
			}
			note := "not anchored to a line"
			if k := boundKey(x.h, "request.new", nil); k != "" {
				note = k + " · " + note
			}
			c.Text("note", note, monoFont(10, th.C(theme.FgGhost)))
		})
	})
}

// filters are the list's chips: All and each group, with counts; one is on.
func (x *requests) filters(c *ui.Ctx, se core.SessionState, st *requestsState, width float32) {
	th := c.Theme()
	counts := make([]int, len(filterGroups))
	for _, t := range se.Threads {
		counts[0]++
		for i, g := range filterGroups {
			if i > 0 && int(groupOf(t)) == g {
				counts[i]++
			}
		}
	}
	labels := []string{"all", "needs you", "working", "open", "resolved"}
	tones := []theme.TokenID{theme.Accent, theme.StatusNeedsYou, theme.StatusWorking, theme.FgMuted, theme.StatusQuiet}
	if r, ok := c.Rect(c.Next("filters")); ok {
		width = r.Dx()
	}
	font := kickerFont(render.Color{}).Font
	var rows [][]int
	w := float32(0)
	for i, l := range labels {
		n := c.Shaper().Shape(strings.ToUpper(l), font).Advance + c.Shaper().Shape(fmt.Sprint(counts[i]), font).Advance + 22
		if len(rows) == 0 || w+n > max(width-32, 160) {
			rows = append(rows, nil)
			w = 0
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], i)
		w += n + 6
	}
	c.Column("filters", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 0), Gap: 6}, func(c *ui.Ctx) {
		for r, row := range rows {
			c.Row(r, ui.Style{Gap: 6}, func(c *ui.Ctx) {
				for _, i := range row {
					on := st.filter == i
					tone := th.C(tones[i])
					fill := render.Color{}
					if on {
						fill = theme.WithAlpha(tone, .12)
					}
					if widget.Clickable(c, i, ui.Style{Dir: ui.Row, H: ui.Px(20), Pad: ui.PadXY(7, 0), Gap: 6, Cross: ui.Center, Radius: 10,
						Stroke: theme.WithAlpha(tone, .45), Border: 1, Fill: fill, Selected: on, Focusable: true, Label: labels[i],
						Help: "agent/requests#filters"}, func(c *ui.Ctx) {
						c.Text("l", strings.ToUpper(labels[i]), kickerFont(tone))
						c.Text("n", fmt.Sprint(counts[i]), kickerFont(tone))
					}).Clicked {
						st.filter = i
					}
				}
			})
		}
	})
}

func (x *requests) listRow(c *ui.Ctx, se core.SessionState, st *requestsState, t core.Thread, sel bool, now time.Time) {
	th := c.Theme()
	g := groupOf(t)
	fill, edge := render.Color{}, render.Color{}
	if sel {
		fill, edge = th.C(tReqRowOn), th.C(tReqRowEdge)
	}
	title, tone := th.C(theme.Fg), th.C(groupTokens[g])
	if g == groupResolved {
		title = th.C(theme.FgMuted)
	}
	if widget.Clickable(c, string(t.ID), ui.Style{Dir: ui.Row, W: ui.Flex(1), Fill: fill, Role: ui.RoleListItem, Selected: sel, Label: t.Heading(),
		Help: "agent/requests#list"}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Column("in", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 16, T: 9, B: 9}, Gap: 4}, func(c *ui.Ctx) {
			c.Row("h", ui.Style{W: ui.Flex(1), Gap: 9, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: tone}, nil)
				c.Text("t", truncate(t.Heading(), 46), uiFont(13, title))
			})
			c.Box("p", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 16}}, func(c *ui.Ctx) {
				if p := preview(se, t); p != "" && g != groupResolved {
					c.Text("t", truncate(p, 50), uiFont(12, th.C(theme.FgFaint)))
				}
			})
			meta := []string{threadPlace(t), msgs(len(t.Comments)), ago(now, t.Updated)}
			if t.Btw {
				meta = []string{"btw", ago(now, t.Updated)}
			}
			c.Box("m", ui.Style{Pad: ui.Insets{L: 16}}, func(c *ui.Ctx) {
				c.Text("t", strings.Join(meta, " · "), monoFont(10.5, th.C(theme.FgGhost)))
			})
		})
	}).Clicked {
		st.sel, st.writing = t.ID, false
		st.convo.follow = true
		x.run("request.open", nil)
	}
}

// requestView is one request at a readable width (21's centre and right): its anchored code, the conversation with
// the agent's activity and evidence, the composer, and what it changed and spawned.
func (x *requests) requestView(c *ui.Ctx, vc registry.ViewCtx) {
	th := c.Theme()
	se, ok := x.front()
	root := c.Next("request")
	c.Column("request", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgBase), Role: ui.RoleRegion, Label: "Request", Help: "agent/requests#request"},
		func(c *ui.Ctx) {
			if !ok {
				noSessionNote(c)
				return
			}
			st := x.state(se.ID)
			t, has := x.selection(se, st)
			x.requestHeader(c, se, t, has)
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
				width := float32(840)
				if r, ok := c.Rect(c.Next("left")); ok {
					width = min(max(r.Dx()-96, 280), 900)
				}
				c.Column("left", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
					if vc.Focused && !c.FocusWithin(root) {
						c.Focus(c.Next("convo")) // its keys are the list's: j, k, o, R
					}
					scrolled(c, "convo", &st.convo, ui.Style{W: ui.Flex(1), H: ui.Flex(1), Focusable: true, KeyMode: RequestsKeyMode, Role: ui.RoleList,
						Label: "Conversation"}, ui.Style{Pad: ui.Insets{T: 20, B: 20}, Gap: 20, Cross: ui.Center}, func(c *ui.Ctx, block func(string) ui.ID) {
						if !has {
							x.newRequestNote(c, st, width)
							return
						}
						if t.Anchor != nil {
							x.anchorCard(c, se, t, width)
						}
						for _, cm := range t.Comments {
							block(cm.ID)
							x.comment(c, se, t, cm, width)
						}
						startingCard(c, "starting", se, width)
					})
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					c.Row("composerwrap", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 14), Main: ui.Center}, func(c *ui.Ctx) {
						x.composer(c, se, st, has, width)
					})
				})
				c.Box("edge", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
				x.requestSide(c, se, st, t, has)
			})
		})
}

func (x *requests) requestHeader(c *ui.Ctx, se core.SessionState, t core.Thread, has bool) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(24, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		title, word, tok := "New request", "draft", theme.FgFaint
		if has {
			title = t.Heading()
			word, tok = statusLabel(se, t)
		}
		c.Box("dot", ui.Style{W: ui.Px(8), H: ui.Px(8), Radius: 4, Fill: th.C(tok)}, nil)
		c.Text("title", truncate(title, 80), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 15, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		widget.Chip(c, "status", word, th.C(tok), "agent/requests#request")
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if !has {
			return
		}
		if widget.Button(c, "anchor", "Open in editor ↗", widget.ButtonOpts{Disabled: t.Anchor == nil, Help: "agent/requests#request"}) {
			x.run("request.anchor", nil)
		}
		diff := registry.Args{}
		if p, ok := se.Primary(); ok {
			diff["path"] = p.Dir
		}
		if widget.Button(c, "diff", "View diff", widget.ButtonOpts{Disabled: !x.h.Available("diff.open", diff) || len(t.Touched) == 0,
			Help: "agent/requests#request"}) {
			x.run("diff.open", registry.Args{"path": anchorPath(se, &core.Anchor{Project: t.Touched[0].Project, File: t.Touched[0].Path})})
		}
		if t.Open() {
			if widget.Button(c, "resolve", "Resolve", widget.ButtonOpts{Help: "agent/requests#request"}) {
				x.run("request.resolve", registry.Args{"thread": string(t.ID)})
			}
		} else if widget.Button(c, "reopen", "Reopen", widget.ButtonOpts{Help: "agent/requests#request"}) {
			x.run("request.reopen", registry.Args{"thread": string(t.ID)})
		}
	})
}

func (x *requests) newRequestNote(c *ui.Ctx, st *requestsState, width float32) {
	th := c.Theme()
	c.Column("new", ui.Style{W: ui.Px(width), Gap: 8}, func(c *ui.Ctx) {
		c.Text("k", "NEW REQUEST", kickerFont(th.C(theme.Accent)))
		where := "Not anchored to a line: it's about the whole change."
		if a := st.newAnchor; a != nil {
			where = fmt.Sprintf("About %s:%d.", a.File, a.Line)
		}
		widget.Wrap(c, "w", where+" The agent gets it at once when it's idle, or when its turn ends; urgent interrupts the turn, and btw asks a fork on the side.",
			width, uiFont(12.5, th.C(theme.FgMuted)))
	})
}

// fileLines are a file's lines as the editor has them, else as they are on disk.
func (x *requests) fileLines(path string) []string {
	var b []byte
	if x.sh.editor != nil && x.sh.editor.e != nil {
		if f, ok := x.sh.editor.openBuffer(path); ok {
			b = f.Bytes()
		}
	}
	if b == nil {
		b, _ = os.ReadFile(path)
	}
	if b == nil {
		return nil
	}
	return strings.Split(strings.TrimSuffix(string(b), "\n"), "\n")
}

// anchorCard is the request's anchored code, where it is now.
func (x *requests) anchorCard(c *ui.Ctx, se core.SessionState, t core.Thread, width float32) {
	th := c.Theme()
	a := t.Anchor
	lines := x.fileLines(anchorPath(se, a))
	at := anchoredLine(lines, a)
	end := at + max(a.EndLine-a.Line, 0)
	from := max(at-3, 0)
	var shown []string
	if len(lines) > 0 && at < len(lines) {
		shown = lines[from:min(end+1, len(lines))]
	} else if a.Text != "" {
		from, at, end = a.Line-1, a.Line-1, a.Line-1+strings.Count(strings.TrimRight(a.Text, "\n"), "\n")
		shown = strings.Split(strings.TrimRight(a.Text, "\n"), "\n")
	}
	c.Column("anchor", ui.Style{W: ui.Px(width), Fill: th.C(tBlockBg), Stroke: th.C(tBlockLine), Border: 1, Radius: 6, Clip: true, Role: ui.RoleGroup,
		Label: "Anchored code"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("sq", ui.Style{W: ui.Px(9), H: ui.Px(9), Radius: 2, Fill: th.C(theme.StatusWorking)}, nil)
			c.Text("f", a.File, monoFont(11.5, th.C(theme.FgMuted)))
			span := fmt.Sprintf(":%d", from+1)
			if end > from {
				span = fmt.Sprintf(":%d-%d", from+1, end+1)
			}
			c.Text("l", span, monoFont(11, th.C(theme.FgGhost)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", "anchored · follows edits", monoFont(10, th.C(theme.FgGhost)))
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
		c.Column("code", ui.Style{W: ui.Flex(1), Pad: ui.Insets{T: 4, B: 4}}, func(c *ui.Ctx) {
			for i, l := range shown {
				n := from + i
				on := n >= at && n <= end
				fill := render.Color{}
				if on {
					fill = th.C(tReqAnchorOn)
				}
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(20), Fill: fill, Cross: ui.Center}, func(c *ui.Ctx) {
					edge := render.Color{}
					if on {
						edge = th.C(theme.Accent)
					}
					c.Box("e", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
					c.Box("n", ui.Style{W: ui.Px(44), Pad: ui.Insets{L: 10}}, func(c *ui.Ctx) {
						c.Text("t", fmt.Sprint(n+1), monoFont(11.5, th.C(theme.FgGhost)))
					})
					c.Text("t", strings.ReplaceAll(l, "\t", "    "), monoFont(12, th.C(theme.Fg)))
				})
			}
		})
	})
}

// comment is one message: who said it and when, its body as markdown, and for the agent's, what it did on the way
// (the activity block) and the runs behind what it claims (evidence chips).
func (x *requests) comment(c *ui.Ctx, se core.SessionState, t core.Thread, cm core.Comment, width float32) {
	th := c.Theme()
	src, sourced := registry.ThreadSourceFor(x.h, t.Origin)
	c.Row(cm.ID, ui.Style{W: ui.Px(width), Gap: 14, Role: ui.RoleListItem, Label: cmpOr(cm.Name, string(cm.Author))}, func(c *ui.Ctx) {
		avatar(c, "av", cm)
		inner := width - 42
		c.Column("in", ui.Style{W: ui.Px(inner), Gap: 8}, func(c *ui.Ctx) {
			c.Row("who", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				name := cmpOr(cm.Name, "you")
				if cm.Author == core.AuthorAgent {
					name = providerName(cm.Name)
				}
				c.Text("n", name, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
				if when := commentWhen(c.Now(), cm, t.Origin); when != "" {
					c.Text("t", when, monoFont(10, th.C(theme.FgGhost)))
				}
				if cm.Redacted {
					widget.Chip(c, "redacted", "hidden from the agent", th.C(theme.FgFaint), "git/github#trusted")
				}
				if cm.Model != "" {
					widget.Chip(c, "model", cm.Model, th.C(theme.FgMuted), "agent/requests#request")
				}
				if cm.Btw {
					widget.Chip(c, "btw", "btw", th.C(theme.Accent), "agent/requests#btw")
				}
				if cm.Progress {
					widget.Chip(c, "progress", "working on it", th.C(theme.StatusWorking), "agent/requests#request")
				}
				if cm.Author == core.AuthorYou && !cm.Delivered && !cm.Btw && se.Agent.Provider != "" {
					widget.Chip(c, "queued", queuedLabel(se), th.C(tQueued), "agent/requests#request")
				}
			})
			markdownText(c, "body", cm.Body, inner, uiFont(13, th.C(theme.Fg)))
			if sourced && src.Comment != nil {
				src.Comment(c, string(se.ID), string(t.ID), cm.ID, inner)
			}
			for i, img := range cm.Images {
				c.Row(fmt.Sprint("img", i), ui.Style{Pad: ui.PadXY(10, 6), Gap: 8, Radius: 6, Stroke: th.C(tBlockLine), Border: 1, Cross: ui.Center},
					func(c *ui.Ctx) {
						c.Text("t", "pasted · "+filepath.Base(img), monoFont(10.5, th.C(theme.FgFaint)))
					})
			}
			if len(cm.Activity) > 0 {
				x.activity(c, se, cm.Activity, inner)
			}
			if len(cm.Evidence) > 0 || cm.Unbacked != "" {
				c.Row("evidence", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
					for i, e := range cm.Evidence {
						evidenceChip(c, i, x.h, se.ID, e.Ref, e.Label, e.OK)
					}
					if cm.Unbacked != "" {
						c.Text("unbacked", "⚠ "+cm.Unbacked, monoFont(10.5, th.C(tReqUnbacked)))
					}
				})
			}
		})
	})
}

// activity is a reply's activity block: each edit, lookup and run, with its diff or run a click away.
func (x *requests) activity(c *ui.Ctx, se core.SessionState, acts []core.Activity, width float32) {
	th := c.Theme()
	c.Column("activity", ui.Style{W: ui.Px(width), Gap: 6}, func(c *ui.Ctx) {
		c.Text("k", "ACTIVITY", kickerFont(th.C(theme.FgFaint)))
		c.Column("rows", ui.Style{W: ui.Flex(1), Fill: th.C(tBlockBg), Stroke: th.C(tBlockLine), Border: 1, Radius: 6, Clip: true}, func(c *ui.Ctx) {
			for i, a := range acts {
				if i > 0 {
					c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
				}
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(12, 0), Gap: 10, Cross: ui.Center, Role: ui.RoleListItem, Label: a.Title},
					func(c *ui.Ctx) {
						glyph, tone := "✓", th.C(tToolOK)
						if a.Failed {
							glyph, tone = "✕", th.C(tToolFailed)
						}
						c.Text("g", glyph, monoFont(11.5, tone))
						verb, rest, _ := strings.Cut(a.Title, " ")
						c.Text("v", verb, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
						c.Text("r", truncate(relTo(se, rest), 70), monoFont(11.5, th.C(theme.FgMuted)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if a.Kind == "edit" && (a.Added > 0 || a.Removed > 0) {
							widget.DiffStat(c, "s", a.Added, a.Removed, widget.DiffStatOpts{})
						}
						if a.Run != "" {
							if r, ok := se.Run(a.Run); ok {
								evidenceChip(c, "run", x.h, se.ID, r.ID, runLabel(r), r.OK)
							}
						}
						if a.Kind == "edit" && a.Path != "" {
							if widget.Link(c, "view", "view", "agent/requests#activity") {
								x.run("editor.open", registry.Args{"path": anchorPath(se, &core.Anchor{File: a.Path})})
							}
						}
					})
			}
		})
	})
}

// composer is where you write to the request: what's attached, the text, its ways of going, and Send.
func (x *requests) composer(c *ui.Ctx, se core.SessionState, st *requestsState, has bool, width float32) {
	th := c.Theme()
	id := x.target(st)
	d := x.draftFor(st, id)
	c.Column("composer", ui.Style{W: ui.Px(width), Pad: ui.Pad(12), Gap: 10, Fill: th.C(theme.BgSurface), Stroke: th.C(theme.LineStrong), Border: 1,
		Radius: 8, Role: ui.RoleGroup, Label: "Composer", Help: "agent/requests#composer"}, func(c *ui.Ctx) {
		if len(d.attach) > 0 {
			c.Row("attach", ui.Style{Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
				for i, a := range d.attach {
					c.Row(i, ui.Style{Gap: 4, Pad: ui.PadXY(6, 2), Radius: 4, Stroke: th.C(tBlockLine), Border: 1, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", a.label(), monoFont(10.5, th.C(theme.Accent)))
						if widget.IconButton(c, "x", "✕", widget.IconButtonOpts{Label: "Remove " + a.label(), Help: "agent/requests#composer"}) {
							d.attach = slices.Delete(slices.Clone(d.attach), i, i+1)
						}
					})
				}
			})
		}
		in := c.Next("input")
		if d.compose {
			c.Focus(in)
			d.compose = false
		}
		placeholder := "Write a request"
		if has {
			placeholder = "Reply, or ask more"
		}
		r := widget.Input(c, "input", &d.text, widget.InputOpts{Label: "Write to the request", Placeholder: placeholder, KeyMode: RequestComposerKeyMode,
			Help: "agent/requests#composer"})
		switch {
		case r.Submitted:
			if err := x.send(context.Background(), st, id, false); err != nil {
				x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
			}
		case r.Cancelled:
			st.focusList = true
		}
		c.Row("tools", ui.Style{W: ui.Flex(1), Gap: 6, Cross: ui.Center}, func(c *ui.Ctx) {
			if widget.ToggleChip(c, "file", "@ FILE", false, widget.ToggleChipOpts{Tooltip: "Attach a file: ⌃a in the files picker", Help: "agent/requests#composer"}) {
				x.run("picker.open", registry.Args{"source": "files"})
			}
			if widget.ToggleChip(c, "symbol", "# SYMBOL", false, widget.ToggleChipOpts{Tooltip: "Attach a symbol: ⌃a in the symbols picker",
				Help: "agent/requests#composer"}) {
				x.run("picker.open", registry.Args{"source": "symbols"})
			}
			c.Box("sep", ui.Style{W: ui.Px(1), H: ui.Px(16), Fill: th.C(theme.Line)}, nil)
			d.urgent = widget.ToggleChip(c, "urgent", "URGENT · INTERRUPTS TURN", d.urgent, widget.ToggleChipOpts{Label: "Urgent",
				Tooltip: "Interrupt the agent's turn with it", Help: "agent/requests#urgent"})
			d.btw = widget.ToggleChip(c, "btw", "BTW", d.btw, widget.ToggleChipOpts{Label: "By the way",
				Tooltip: "A side question, answered by a fork outside the conversation", Help: "agent/requests#btw"})
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			var who []string
			if se.Agent.Provider != "" {
				who = append(who, strings.ToLower(providerName(se.Agent.Provider)))
			}
			if se.Agent.Model != "" {
				who = append(who, se.Agent.Model)
			}
			c.Text("md", strings.Join(append([]string{"markdown"}, strings.Join(who, " ")), " · "), monoFont(10.5, th.C(theme.FgGhost)))
			if widget.Button(c, "send", "Send", widget.ButtonOpts{Primary: true, Key: "⏎", Disabled: strings.TrimSpace(d.body()) == "",
				Help: "agent/requests#composer"}) {
				if err := x.send(context.Background(), st, id, false); err != nil {
					x.h.Notify(core.Notice{ID: "requests", Severity: core.SevError, Source: "requests", Text: err.Error()})
				}
			}
		})
	})
}

// requestSide is the request's right column: where it is and who has it, what it changed, what it spawned, and the
// requests about the same file.
func (x *requests) requestSide(c *ui.Ctx, se core.SessionState, st *requestsState, t core.Thread, has bool) {
	th := c.Theme()
	now := c.Now()
	c.Column("side", ui.Style{W: ui.Px(300), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Column("top", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true}, func(c *ui.Ctx) {
			fg := th.C(theme.Fg)
			if !has {
				section(c, "this", "This request", "", func(c *ui.Ctx) {
					anchor := "whole change"
					if a := st.newAnchor; a != nil {
						anchor = fmt.Sprintf("%s:%d", filepath.Base(a.File), a.Line)
					}
					stat(c, "anchor", "anchor", anchor, fg)
				})
				return
			}
			section(c, "this", "This request", "", func(c *ui.Ctx) {
				stat(c, "anchor", "anchor", threadPlace(t), fg)
				opened := ago(now, t.Created)
				if cm, ok := firstComment(t); ok && cm.Name != "" {
					opened += " · " + cm.Name
				}
				stat(c, "opened", "opened", opened, fg)
				word, tok := statusLabel(se, t)
				if groupOf(t) == groupWorking && se.Agent.Working && !se.Agent.TurnStarted.IsZero() {
					d := now.Sub(se.Agent.TurnStarted)
					word = fmt.Sprintf("working · %d:%02d", int(d.Minutes()), int(d.Seconds())%60)
				}
				stat(c, "agent", "agent", word, th.C(tok))
			})
			if len(t.Touched) > 0 {
				ledgers := map[string]registry.Ledger{}
				section(c, "changed", "Changed by this request", "", func(c *ui.Ctx) {
					for i, f := range t.Touched {
						c.Row(i, ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
							c.Text("m", "M", monoFont(11.5, th.C(theme.Warn)))
							c.Text("p", truncate(relTo(se, f.Path), 30), monoFont(11.5, th.C(theme.FgMuted)))
							c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
							if n, ok := touchedLedger(x.h, se, f, ledgers); ok {
								readBar(c, "read", n)
								c.Text("rd", fmt.Sprintf("%d/%d", n.Read, n.Total()), monoFont(10, ledgerTone(th, n)))
							}
							widget.DiffStat(c, "s", f.Added, f.Removed, widget.DiffStatOpts{})
						})
					}
				})
			}
			var tasks []core.Task
			for _, k := range se.Tasks {
				if k.Thread == t.ID {
					tasks = append(tasks, k)
				}
			}
			var spawned, related []core.Thread
			for _, o := range se.Threads {
				switch {
				case o.ID == t.ID:
				case o.Origin == string(t.ID):
					spawned = append(spawned, o)
				case t.Anchor != nil && o.Anchor != nil && o.Anchor.File == t.Anchor.File:
					related = append(related, o)
				}
			}
			if len(tasks)+len(spawned) > 0 {
				section(c, "spawned", "Spawned", "", func(c *ui.Ctx) {
					for _, o := range spawned {
						x.threadLink(c, se, o)
					}
					taskRail(c, "tasks", tasks)
				})
			}
			if len(related) > 0 {
				section(c, "related", "Related", "", func(c *ui.Ctx) {
					for _, o := range related {
						x.threadLink(c, se, o)
					}
				})
			}
		})
		c.Column("keys", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(16, 12), Gap: 4}, func(c *ui.Ctx) {
			c.Text("a", "j/k next request · ⏎ focus composer", monoFont(10, th.C(theme.FgGhost)))
			c.Text("b", "o open anchor · R resolve", monoFont(10, th.C(theme.FgGhost)))
		})
	})
}

// threadLink is another request as a row that opens it.
func (x *requests) threadLink(c *ui.Ctx, se core.SessionState, t core.Thread) {
	th := c.Theme()
	_, tok := statusLabel(se, t)
	if widget.Clickable(c, "t."+string(t.ID), ui.Style{Dir: ui.Row, W: ui.Flex(1), Gap: 8, Cross: ui.Center, Label: t.Heading(), Help: "agent/requests#request"},
		func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(6), H: ui.Px(6), Radius: 3, Fill: th.C(tok)}, nil)
			c.Text("t", truncate(t.Heading(), 36), monoFont(11.5, th.C(theme.FgMuted)))
		}).Clicked {
		x.run("request.open", registry.Args{"thread": string(t.ID)})
	}
}

// runLabel is a run's counts, as its evidence chip says them.
func runLabel(r core.TestRun) string {
	if r.Failed > 0 {
		return fmt.Sprintf("%d failed, %d passed", r.Failed, r.Passed)
	}
	return fmt.Sprintf("%d passed", r.Passed)
}

// evidenceChip is a link to a test run: ✓ or ✕, its counts and id; a click opens the run.
func evidenceChip(c *ui.Ctx, key any, h plugin.Host, session core.SessionID, run, label string, ok bool) {
	th := c.Theme()
	tone, glyph := th.C(tToolOK), "✓"
	if !ok {
		tone, glyph = th.C(tToolFailed), "✕"
	}
	if widget.Clickable(c, key, ui.Style{Dir: ui.Row, H: ui.Px(20), Pad: ui.PadXY(7, 0), Gap: 6, Cross: ui.Center, Radius: 10, Fill: theme.WithAlpha(tone, .08),
		Stroke: theme.WithAlpha(tone, .4), Border: 1, Focusable: true, Label: "Test run " + run + ": " + label, Tooltip: "Open the run",
		Help: "agent/requests#evidence"}, func(c *ui.Ctx) {
		c.Text("g", glyph, monoFont(10.5, tone))
		c.Text("l", label, monoFont(10.5, tone))
		c.Text("r", "run "+run, monoFont(10, th.C(theme.FgFaint)))
	}).Clicked {
		_ = h.Run(context.Background(), "test.run.show", registry.Args{"id": run, "session": string(session)})
	}
}

type summaryView struct{ x *requests }

func (v summaryView) Title(string) string { return "Session" }
func (v summaryView) EdgeToEdge()         {}
func (v summaryView) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "summary", Fn: v.x.summary}
}

// summary is 01's right column: the requests in brief, the tasks rail, and the session's stats at the foot.
func (x *requests) summary(c *ui.Ctx) {
	th := c.Theme()
	se, ok := x.front()
	c.Column("summary", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Fill: th.C(theme.BgSurface), Role: ui.RoleRegion, Label: "Session summary",
		Help: "agent/requests#summary"}, func(c *ui.Ctx) {
		if !ok {
			noSessionNote(c)
			return
		}
		now := c.Now()
		c.Column("top", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true}, func(c *ui.Ctx) {
			counts := map[reqGroup]int{}
			for _, t := range se.Threads {
				counts[groupOf(t)]++
			}
			c.Row("head", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 8}, Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("k", "REQUESTS", kickerFont(th.C(theme.FgFaint)))
				if n := counts[groupNeedsYou]; n > 0 {
					widget.Chip(c, "needs", fmt.Sprintf("%d needs you", n), th.C(theme.StatusNeedsYou), "agent/requests#summary")
				}
				if n := counts[groupWorking]; n > 0 {
					widget.Chip(c, "working", fmt.Sprintf("%d working", n), th.C(theme.StatusWorking), "agent/requests#summary")
				}
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				if n := counts[groupResolved]; n > 0 {
					c.Text("res", fmt.Sprintf("%d resolved", n), monoFont(11, th.C(theme.FgFaint)))
				}
			})
			for i, t := range x.visible(se, &requestsState{}) {
				if i > 0 {
					c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
				}
				x.summaryRow(c, se, t, now)
			}
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			done, all := taskCount(se.Tasks)
			section(c, "tasks", "Tasks", fmt.Sprintf("%d/%d", done, all), func(c *ui.Ctx) {
				if all == 0 {
					c.Text("none", "No tasks yet: the agent adds them as it plans.", uiFont(12, th.C(theme.FgGhost)))
				}
				taskRail(c, "rail", se.Tasks)
			})
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		sessionStats(c, x.h, se, now)
	})
}

func (x *requests) summaryRow(c *ui.Ctx, se core.SessionState, t core.Thread, now time.Time) {
	th := c.Theme()
	g := groupOf(t)
	title, meta := th.C(theme.Fg), th.C(theme.FgFaint)
	if g == groupResolved {
		title, meta = th.C(theme.FgMuted), th.C(theme.FgGhost)
	}
	var note []string
	switch g {
	case groupResolved:
		note = []string{"resolved", ago(now, t.Updated)}
	case groupWorking:
		note = []string{threadPlace(t), strings.ToLower(providerName(se.Agent.Provider)) + " working", ago(now, t.Updated)}
	case groupNeedsYou:
		note = []string{threadPlace(t), "awaiting you", ago(now, t.Updated)}
	default:
		word, _ := statusLabel(se, t)
		note = []string{threadPlace(t), word, ago(now, t.Updated)}
	}
	if widget.Clickable(c, string(t.ID), ui.Style{Dir: ui.Column, W: ui.Flex(1), Pad: ui.PadXY(12, 9), Gap: 4, Role: ui.RoleListItem, Label: t.Heading(),
		Help: "agent/requests#summary"}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{Gap: 9, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Box("dot", ui.Style{W: ui.Px(7), H: ui.Px(7), Radius: 3.5, Fill: th.C(groupTokens[g])}, nil)
			c.Text("t", truncate(t.Heading(), 44), uiFont(13, title))
		})
		c.Box("m", ui.Style{Pad: ui.Insets{L: 16}}, func(c *ui.Ctx) {
			c.Text("t", strings.Join(note, " · "), monoFont(10.5, meta))
		})
	}).Clicked {
		x.run("request.open", registry.Args{"thread": string(t.ID)})
	}
}
