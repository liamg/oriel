package explain

import (
	"context"
	"fmt"
	"slices"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// decorationKey is what the decorations of ctx are made from: the buffer's units as worked out last, the base they
// were worked out against, what each unit's status reads, and the card.
func (x *Plugin) decorationKey(ctx uireg.DecorationCtx) any {
	type made struct {
		units  *fileUnits
		base   int
		status uint64
		card   *explainCard
		sel    int
		reveal bool
	}
	_, base, _ := x.baseOf(ctx.Path)
	x.mu.Lock()
	k := made{units: x.bufs[ctx.Buffer], base: base, status: x.statusPrint(), card: x.card}
	x.mu.Unlock()
	if k.card != nil {
		k.sel, k.reveal = k.card.sel, k.card.reveal
	}
	return k
}

// decorations put a ? beside every unit, brighter on those the branch changed, and the card between the lines under
// what it explains, with those lines marked.
func (x *Plugin) decorations(ctx uireg.DecorationCtx) []uireg.Decoration {
	if ctx.Buffer == nil {
		return nil
	}
	var out []uireg.Decoration
	if fu := x.bufferUnits(ctx.Path, ctx.Buffer, ctx.FileBytes); fu != nil {
		for i, u := range fu.units {
			line := fu.lines[i]
			if line < ctx.Top || line >= ctx.Bottom {
				continue
			}
			st := x.unitStatus(ctx.Path, u, fu.hashes[i])
			tok := map[string]theme.TokenID{"open": tExplainMarkOn, "explained": tUnitsDone, "changed": tUnitsChanged}[st]
			switch {
			case tok != 0:
			case slices.Contains(fu.owners, i):
				tok = tExplainMark
			default:
				tok = tExplainMarkQuiet
			}
			word, _ := unitStatusWord(st)
			d := uireg.Decoration{Kind: uireg.Sign, Line: line, Text: "?", Style: tok.Name(), Priority: 15, Command: "explain.unit",
				Args: registry.Args{"file": ctx.Path, "line": line + 1}, Tooltip: "Explain " + fu.labels[i] + " · " + word}
			switch j := len(out) - 1; {
			case j < 0 || out[j].Line != line:
				out = append(out, d)
			case out[j].Style == tExplainMarkQuiet.Name():
				out[j] = d
			}
		}
	}
	cd := x.card
	if cd == nil || cd.buf != ctx.Buffer {
		return out
	}
	r, ok := cd.rangeOf(cd.sel)
	if !ok || r.End > ctx.Snap.Len() {
		return out
	}
	a, z := spanLines(ctx.Snap, r)
	for l := max(a, ctx.Top); l <= z && l < ctx.Bottom; l++ {
		out = append(out, uireg.Decoration{Kind: uireg.LineBg, Line: l, Style: tExplainUnitBg.Name(), Edge: tExplainEdge.Name(), Priority: 5})
	}
	if z >= ctx.Top && z < ctx.Bottom {
		out = append(out, uireg.Decoration{Kind: uireg.Block, Line: z, Priority: 28, Reveal: cd.reveal,
			Node: &ui.Func{Key: "explain.card", Fn: func(c *ui.Ctx) { x.cardView(c, cd) }}})
		cd.reveal = false
	}
	return out
}

// cardView is the card: EXPLAIN and the breadcrumb, wider and narrower; the answer; the depths, who answered, Ask a
// follow-up and Pin as note (25-explain-unit).
func (x *Plugin) cardView(c *ui.Ctx, cd *explainCard) {
	if x.card != cd {
		return
	}
	th := c.Theme()
	width := float32(900)
	if r, ok := c.Rect(c.Next("wrap")); ok {
		width = min(r.Dx(), 900)
	}
	const help = "editor/pane#explain"
	run := func(cmd string, a registry.Args) {
		if err := x.h.Run(context.Background(), cmd, a); err != nil {
			x.notify(err)
		}
	}
	c.Row("wrap", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		c.Column("card", ui.Style{W: ui.Px(width), Fill: th.C(tExplainCardBg), Stroke: th.C(tExplainCardBd), Border: 1, Radius: 8, Clip: true,
			Role: ui.RoleGroup, Label: "Explain " + cd.breadcrumb(), Help: help}, func(c *ui.Ctx) {
			c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(32), Pad: ui.PadXY(12, 0), Gap: 6, Cross: ui.Center, Clip: true}, func(c *ui.Ctx) {
				c.Text("t", "EXPLAIN", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
				c.Row("crumbs", ui.Style{Gap: 5, Cross: ui.Center, Clip: true, W: ui.Flex(1)}, func(c *ui.Ctx) {
					for i, cr := range cd.crumbs {
						if i > 0 {
							c.Text(fmt.Sprint("sep", i), "›", kit.MonoFont(11, th.C(theme.FgGhost)))
						}
						if widget.ToggleChip(c, i, cr.label, i == cd.sel, widget.ToggleChipOpts{Help: help, Label: "Explain " + cr.label}) != (i == cd.sel) {
							cd.sel = i
							x.ensure()
						}
					}
				})
				if widget.Link(c, "wider", "[ wider", help) {
					run("explain.widen", nil)
				}
				if widget.Link(c, "narrower", "] narrower", help) {
					run("explain.narrow", nil)
				}
				if widget.Link(c, "close", "✕", help) {
					run("explain.close", nil)
				}
			})
			c.Box("rule1", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tExplainCardBd)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 14, R: 14, T: 9, B: 10}, Gap: 8}, func(c *ui.Ctx) {
				a := x.shown(cd)
				st := a.state()
				if st.Phase == widget.Idle {
					st = widget.LoadState{Phase: widget.Pending, Since: c.Now()}
				}
				widget.Async(c, "answer", st, widget.AsyncOpts{Label: "Asking", Retry: func() { run("explain.refresh", nil) }, Help: help},
					func(c *ui.Ctx) { kit.MarkdownText(c, x.h, "md", a.text, width-28, kit.UIFont(13, th.C(theme.FgMuted))) })
				if x.changed(cd) {
					c.Row("changed", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Text("t", "changed since it was explained", kit.MonoFont(10.5, th.C(tUnitsChanged)))
						if widget.Link(c, "refresh", "refresh", help) {
							run("explain.refresh", nil)
						}
					})
				}
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tExplainCardBd)}, nil)
			c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(36), Pad: ui.PadXY(12, 0), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				depth := cd.depth
				if widget.Segmented(c, "depth", []string{"BRIEF", "DETAILED", "WHY · HISTORY"}, &depth, widget.SegmentedOpts{Help: help, Label: "Depth"}) {
					run("explain.depth", registry.Args{"depth": explainDepths[depth]})
				}
				by := ""
				if a := x.shown(cd); a != nil {
					by = a.by
				}
				c.Box("by", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", by, kit.MonoFont(10.5, th.C(theme.FgFaint))) })
				a := x.shown(cd)
				answered := a != nil && a.text != ""
				if widget.Button(c, "follow", "Ask a follow-up", widget.ButtonOpts{Help: help, Disabled: !x.h.Available("request.new", nil)}) {
					run("explain.follow-up", nil)
				}
				if widget.Button(c, "pin", "Pin as note", widget.ButtonOpts{Help: help, Disabled: !answered || !x.h.Available("request.comment", nil)}) {
					run("explain.pin", nil)
				}
			})
		})
	})
}
