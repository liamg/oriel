package app

import (
	"context"
	"fmt"
	"strings"

	"github.com/liamg/oriel/app/fuzzy"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/lsp"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tPopupBg    = theme.Declare("lsp.popup.bg", "bg.raised")
	tPopupLine  = theme.Declare("lsp.popup.border", "line.strong")
	tCompSel    = theme.Declare("lsp.completion.selected", "accent.bg")
	tCompMatch  = theme.Declare("lsp.completion.match", "accent")
	tKindFunc   = theme.Declare("lsp.kind.function", "amber")
	tKindType   = theme.Declare("lsp.kind.type", "cyan")
	tKindValue  = theme.Declare("lsp.kind.value", "violet")
	tKindOther  = theme.Declare("lsp.kind.other", "fg.faint")
	tSigActive  = theme.Declare("lsp.signature.active", "accent")
	codeMono    = typeset.FontSpec{Family: typeset.Mono, Size: 12}
	docFontSize = float32(12)
)

// kindGlyph is a completion kind's mark and colour in the menu.
func kindGlyph(k lsp.CompletionKind) (string, theme.TokenID) {
	switch k {
	case 2, 3, 4: // method, function, constructor
		return "ƒ", tKindFunc
	case 7, 8, 22, 25: // class, interface, struct, type parameter
		return "T", tKindType
	case 21, 20: // constant, enum member
		return "c", tKindValue
	case 6, 5, 10: // variable, field, property
		return "v", tKindValue
	case 15: // snippet
		return "=", tKindOther
	case 9: // module
		return "m", tKindOther
	case 14: // keyword
		return "k", tKindOther
	}
	return "·", tKindOther
}

type lspOverlay struct{ x *lspExt }

func (v lspOverlay) Build(registry.ViewCtx) ui.Node { return &ui.Func{Key: "lsp", Fn: v.x.overlay} }

// overlay draws the popups at the editor's cursor, and closes those the cursor has left.
func (x *lspExt) overlay(c *ui.Ctx) {
	ed := x.sh.editor
	if ed == nil {
		return
	}
	p := ed.current()
	pos := p.Cursor()
	mode := ed.e.Mode()
	if x.comp.open && (mode != "insert" || pos.Line != x.comp.line || pos.Col < x.comp.start) {
		x.comp.close()
	}
	if x.sig != nil && (mode != "insert" || pos.Line != x.sig.line) {
		x.sig = nil
	}
	if x.hover != nil && (pos.Line != x.hover.at.Line || pos.Col != x.hover.at.Col || mode != "normal") {
		x.hover = nil
	}
	r, ok := c.Rect(ed.caret)
	if !ok {
		return
	}
	below := render.Point{X: r.Min.X, Y: r.Max.Y + 2}
	above := render.Point{X: r.Min.X, Y: r.Min.Y - 4}
	if x.sig != nil {
		at := above
		c.Box("sig", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &at, Self: render.Point{Y: 1}}, Scale: partScale(x.h.Options(), "ui.hover.font_size")},
			func(c *ui.Ctx) { x.signatureCard(c) })
	}
	if x.comp.open {
		c.Box("comp", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &below}, Scale: partScale(x.h.Options(), "ui.completion.font_size")},
			func(c *ui.Ctx) { x.completionMenu(c) })
	}
	if x.hover != nil {
		c.Box("hover", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &below}, Scale: partScale(x.h.Options(), "ui.hover.font_size")},
			func(c *ui.Ctx) { x.hoverView(c) })
	}
	if x.actions != nil {
		x.actionsMenu(c, below)
	}
	if x.rename != nil {
		x.renameInput(c, below)
	}
}

func popup(c *ui.Ctx, key any, st ui.Style, body func(c *ui.Ctx)) {
	th := c.Theme()
	st.Fill, st.Stroke, st.Border, st.Radius = th.C(tPopupBg), th.C(tPopupLine), 1, 6
	c.Column(key, st, body)
}

// completionMenu is the menu: each item's kind, label (the typed letters lit) and detail, and the highlighted one's
// docs beside it.
func (x *lspExt) completionMenu(c *ui.Ctx) {
	th := c.Theme()
	p := x.sh.editor.current()
	ln := p.Buf().Line(x.comp.line)
	word := string(ln[x.comp.start:min(p.Cursor().Col, len(ln))])
	n := min(len(x.comp.shown), 10)
	top := max(0, min(x.comp.sel-n+1, len(x.comp.shown)-n))
	c.Row("menu", ui.Style{Gap: 0}, func(c *ui.Ctx) {
		popup(c, "list", ui.Style{W: ui.Px(380), Pad: ui.PadXY(0, 4), Help: "editor/pane#completion", Role: ui.RoleList, Label: "Completions"}, func(c *ui.Ctx) {
			for i := top; i < top+n; i++ {
				it := x.comp.items[x.comp.shown[i]]
				fill := render.Color{}
				if i == x.comp.sel {
					fill = th.C(tCompSel)
				}
				c.Row(i, ui.Style{W: ui.Flex(1), H: ui.Px(24), Pad: ui.PadXY(8, 0), Gap: 8, Cross: ui.Center, Fill: fill, Clip: true,
					Role: ui.RoleListItem, Label: it.Label, Selected: i == x.comp.sel}, func(c *ui.Ctx) {
					g, tok := kindGlyph(it.Kind)
					c.Box("kind", ui.Style{W: ui.Px(16), H: ui.Px(16), Radius: 3, Fill: theme.WithAlpha(th.C(tok), .15), Main: ui.Center, Cross: ui.Center},
						func(c *ui.Ctx) {
							c.Text("g", g, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(tok)})
						})
					label := ui.TextStyle{Font: codeMono, Color: th.C(theme.Fg)}
					_, hits, _ := fuzzy.Match(word, it.Label)
					c.Row("label", ui.Style{}, func(c *ui.Ctx) {
						marked(c, it.Label, hits, label, ui.TextStyle{Font: label.Font, Color: th.C(tCompMatch)})
					})
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					detail := it.LabelDetail
					if detail == "" {
						detail = it.Detail
					}
					c.Text("d", truncate(detail, 28), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10.5}, Color: th.C(theme.FgFaint)})
				})
			}
		})
		it, i, ok := x.selected()
		if !ok {
			return
		}
		x.resolve(i)
		if it.Detail == "" && it.Doc.Text == "" {
			return
		}
		popup(c, "doc", ui.Style{W: ui.Px(300), Pad: ui.Pad(10), Gap: 8, Help: "editor/pane#completion"}, func(c *ui.Ctx) {
			if it.Detail != "" {
				c.Text("detail", it.Detail, ui.TextStyle{Font: codeMono, Color: th.C(theme.Fg)})
			}
			if it.Doc.Text != "" {
				markdown(c, "md", it.Doc, 280)
			}
		})
	})
}

// resolve fills in an item's docs the first time it's highlighted.
func (x *lspExt) resolve(i int) {
	if _, ok := x.comp.resolved[i]; ok {
		return
	}
	_, _, d := x.current()
	if d == nil || d.client == nil {
		return
	}
	it := x.comp.items[i]
	x.comp.resolved[i] = it
	gen, c := x.comp.gen, d.client
	async(x, func(ctx context.Context) (lsp.Completion, error) { return c.ResolveCompletion(ctx, it) }, func(r lsp.Completion, err error) {
		if err == nil && x.comp.gen == gen && x.comp.resolved != nil {
			x.comp.resolved[i] = r
		}
	})
}

// signatureCard is the call's signature with the parameter being typed lit, and its docs.
func (x *lspExt) signatureCard(c *ui.Ctx) {
	th := c.Theme()
	h := x.sig.help
	s := h.Signatures[min(max(h.Active, 0), len(h.Signatures)-1)]
	popup(c, "card", ui.Style{Pad: ui.PadXY(10, 6), Gap: 4, Help: "editor/pane#signature"}, func(c *ui.Ctx) {
		base := ui.TextStyle{Font: codeMono, Color: th.C(theme.FgMuted)}
		lit := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 12, Weight: typeset.SemiBold}, Color: th.C(tSigActive)}
		c.Row("label", ui.Style{}, func(c *ui.Ctx) {
			from, to := -1, -1
			if a := s.ActiveParam; a >= 0 && a < len(s.Params) {
				if pr := s.Params[a]; pr.Start >= 0 && pr.End > pr.Start && pr.End <= len(s.Label) {
					from, to = pr.Start, pr.End
				} else if i := strings.Index(s.Label, s.Params[a].Label); i >= 0 {
					from, to = i, i+len(s.Params[a].Label)
				}
			}
			if from < 0 {
				c.Text("t", s.Label, base)
				return
			}
			c.Text("a", s.Label[:from], base)
			c.Text("p", s.Label[from:to], lit)
			c.Text("z", s.Label[to:], base)
		})
		if s.Doc.Text != "" {
			markdown(c, "doc", s.Doc, 460)
		}
	})
}

// hoverView is the K card.
func (x *lspExt) hoverView(c *ui.Ctx) {
	popup(c, "card", ui.Style{W: ui.Px(520), Pad: ui.Pad(12), Gap: 8, Help: "editor/pane#hover"}, func(c *ui.Ctx) {
		markdown(c, "md", x.hover.md, 494)
		c.Text("keys", "esc to close", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10}, Color: c.Theme().C(theme.FgGhost)})
	})
}

// actionsMenu is the code actions, grouped by kind, focused until one's taken or it's closed.
func (x *lspExt) actionsMenu(c *ui.Ctx, at render.Point) {
	m := x.actions
	groups := []struct{ prefix, title string }{{"quickfix", "Quick fix"}, {"refactor", "Refactor"}, {"source", "Source"}, {"", "Other"}}
	var items []widget.MenuItem
	var index []int
	th := c.Theme()
	for _, g := range groups {
		first := true
		for i, a := range m.actions {
			kind := a.Kind
			inGroup := g.prefix == "" && !strings.HasPrefix(kind, "quickfix") && !strings.HasPrefix(kind, "refactor") && !strings.HasPrefix(kind, "source") ||
				g.prefix != "" && strings.HasPrefix(kind, g.prefix)
			if !inGroup {
				continue
			}
			if first {
				items, index = append(items, widget.MenuItem{Label: g.title, Heading: true}), append(index, -1)
				first = false
			}
			icon, tone := "↗", th.C(theme.FgFaint)
			if strings.HasPrefix(kind, "quickfix") {
				icon, tone = "●", th.C(theme.Brand)
			}
			items = append(items, widget.MenuItem{Label: a.Title, Icon: icon, IconTone: tone, Detail: m.client.Spec().Name, Disabled: a.Disabled != "",
				Help: "editor/pane#code-actions"})
			index = append(index, i)
		}
	}
	for j, it := range m.agent {
		if j == 0 {
			items, index = append(items, widget.MenuItem{Label: "Agent", Heading: true}), append(index, -1)
		}
		items = append(items, widget.MenuItem{Label: it.Label, Icon: "◈", IconTone: th.C(theme.Accent), Disabled: !x.h.Available(it.Command, it.Args),
			Help: "editor/pane#code-actions"})
		index = append(index, len(m.actions)+j)
	}
	id := ui.Child(c.Next("actions"), "menu")
	if c.Interaction(id).Keys == nil && !c.FocusWithin(id) {
		c.Focus(id)
	}
	c.Box("actions", ui.Style{Z: ui.LayerPopup, Float: &ui.Float{Point: &at}}, func(c *ui.Ctx) {
		chosen, closed := widget.Menu(c, "menu", items, widget.MenuOpts{Width: 380, Help: "editor/pane#code-actions"})
		switch {
		case closed:
			x.actions = nil
		case chosen >= 0 && index[chosen] >= len(m.actions):
			x.actions = nil
			it := m.agent[index[chosen]-len(m.actions)]
			if err := x.h.Run(context.Background(), it.Command, it.Args); err != nil {
				x.h.Notify(core.Notice{ID: "lsp.code_action", Severity: core.SevWarning, Key: true, Text: err.Error()})
			}
		case chosen >= 0 && index[chosen] >= 0:
			x.actions = nil
			x.runAction(m, m.actions[index[chosen]])
		}
	})
}

// marked draws s with the runes at positions hits in hit's style.
func marked(c *ui.Ctx, s string, hits []int, base, hit ui.TextStyle) {
	on := map[int]bool{}
	for _, h := range hits {
		on[h] = true
	}
	var run strings.Builder
	lit := false
	k := 0
	flush := func() {
		if run.Len() == 0 {
			return
		}
		st := base
		if lit {
			st = hit
		}
		c.Text(k, run.String(), st)
		k++
		run.Reset()
	}
	for i, r := range []rune(s) {
		if on[i] != lit {
			flush()
			lit = on[i]
		}
		run.WriteRune(r)
	}
	flush()
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// markdown draws a server's markup: fenced code in mono, rules as lines, the rest as wrapped text.
func markdown(c *ui.Ctx, key any, m lsp.Markup, width float32) {
	th := c.Theme()
	body := ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: docFontSize}, Color: th.C(theme.FgMuted)}
	if m.Kind != "markdown" {
		c.Column(key, ui.Style{W: ui.Px(width), Gap: 6}, func(c *ui.Ctx) {
			for i, p := range strings.Split(strings.TrimSpace(m.Text), "\n\n") {
				widget.Wrap(c, i, p, width, body)
			}
		})
		return
	}
	markdownText(c, key, m.Text, width, body)
}

// plainMarkdown takes inline markup out of a paragraph: `code`, **bold**, *em* and [links](url).
func plainMarkdown(s string) string {
	r := strings.NewReplacer("`", "", "**", "", "__", "", "~~", "")
	s = r.Replace(s)
	for {
		i := strings.Index(s, "](")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], ")")
		open := strings.LastIndex(s[:i], "[")
		if j < 0 || open < 0 {
			break
		}
		s = s[:open] + s[open+1:i] + s[i+j+1:]
	}
	return s
}

// lspStatus is the status line's server segment: the current file's server and what it's doing.
type lspStatus struct{ x *lspExt }

func (v lspStatus) Build(registry.ViewCtx) ui.Node {
	return &ui.Func{Key: "lspstatus", Fn: v.x.statusSegment}
}

func (x *lspExt) statusSegment(c *ui.Ctx) {
	if x.sh.editor == nil || !x.sh.editor.shown() {
		return
	}
	_, _, d := x.current()
	if d == nil || d.client == nil {
		return
	}
	st := d.client.Status()
	dot, tok := "●", theme.OK
	state := "ready"
	switch st.State {
	case lsp.Starting:
		dot, tok, state = "◌", theme.FgFaint, "starting"
	case lsp.Busy:
		tok, state = theme.Warn, "busy"
		if len(st.Work) > 0 {
			w := st.Work[len(st.Work)-1]
			state = w.Title
			if w.Percent >= 0 {
				state += fmt.Sprintf(" %d%%", w.Percent)
			}
		}
	case lsp.Crashed, lsp.Stopped:
		dot, tok, state = "✕", theme.Error, "stopped"
	}
	c.Row("lsp", ui.Style{H: ui.Flex(1), Gap: 6, Cross: ui.Center, Help: "editor/pane#lsp", Role: ui.RoleStatus, Label: "Language server"}, func(c *ui.Ctx) {
		c.Text("name", st.Server, statusText(c, tStatusFg))
		c.Text("dot", dot, statusText(c, tok))
		c.Text("state", state, statusText(c, tStatusFg))
	})
}
