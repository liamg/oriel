package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tDiagError     = theme.Declare("diagnostic.error", "error")
	tDiagWarning   = theme.Declare("diagnostic.warning", "warn")
	tDiagInfo      = theme.Declare("diagnostic.info", "fg.muted")
	_              = theme.Declare("diagnostic.hint", "fg.faint")
	tDiagErrorLine = theme.Declare("diagnostic.error.line", "alpha(error, 8%)")
)

// problems are the registry's diagnostics (errors.md, ide.md): marked on their lines in the editor, and listed in
// the Problems view.
type problems struct {
	disposer
	sh *shell
	h  plugin.Host
}

func (p *problems) Manifest() plugin.Manifest { return plugin.Manifest{ID: "problems"} }

func (p *problems) Activate(_ context.Context, h plugin.Host) error {
	p.h = h
	p.add(h.Command(registry.Command{ID: "Problems", Title: "List every diagnostic: Lua errors, language servers'", Run: func(ctx registry.Ctx, _ registry.Args) error {
		return h.Run(ctx, "view.open", registry.Args{"id": "problems", "place": "dock", "edge": "bottom", "title": "Problems", "size": 260})
	}}))
	p.add(h.View("problems", problemsView{p}))
	p.add(h.Decorations("diagnostics", registry.DecorationFunc(p.decorations)))
	return nil
}

func diagToken(sev string) theme.TokenID {
	switch sev {
	case registry.SevError:
		return tDiagError
	case registry.SevWarning:
		return tDiagWarning
	}
	return tDiagInfo
}

// The decorations name diagnostic.<severity> and diagnostic.error.line as their styles.
var _ = tDiagErrorLine

// decorations mark each diagnostic in view: a sign, the error's line tinted, its span underlined, its message
// after the line.
func (p *problems) decorations(ctx registry.DecorationCtx) []registry.Decoration {
	var out []registry.Decoration
	for _, d := range p.h.DiagnosticsFor(ctx.Path) {
		if d.Line < ctx.Top || d.Line >= ctx.Bottom || d.Line >= ctx.Snap.Lines() {
			continue
		}
		tok := "diagnostic." + d.Severity
		line := strings.TrimRight(string(ctx.Snap.Line(d.Line)), "\r\n")
		from, to := d.From, d.To
		if to <= from {
			from, to = len(line)-len(strings.TrimLeft(line, " \t")), len(line)
		}
		out = append(out, registry.Decoration{Kind: registry.Sign, Line: d.Line, Style: tok, Text: "●", Priority: 10},
			registry.Decoration{Kind: registry.Underline, Line: d.Line, From: from, To: min(to, len(line)), Style: tok, Priority: 10},
			registry.Decoration{Kind: registry.VirtualText, Line: d.Line, Text: firstLine(d.Message), Style: tok, Priority: 10})
		if d.Severity == registry.SevError {
			out = append(out, registry.Decoration{Kind: registry.LineBg, Line: d.Line, Style: "diagnostic.error.line", Edge: tok, Priority: 10})
		}
	}
	return out
}

// problemsView is the Problems panel: every diagnostic by file and line, a click opening it.
type problemsView struct{ p *problems }

func (v problemsView) Title(string) string {
	if ds, _ := v.p.h.Diagnostics(); len(ds) > 0 {
		return fmt.Sprintf("Problems %d", len(ds))
	}
	return "Problems"
}

func (v problemsView) Build(registry.ViewCtx) ui.Node { return &ui.Func{Key: "problems", Fn: v.p.view} }

func (p *problems) view(c *ui.Ctx) {
	th := c.Theme()
	ds, _ := p.h.Diagnostics()
	mono := func(size float32, col theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(col)}
	}
	c.Column("problems", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 4, ScrollY: true, Help: "errors#problems"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
			c.Text("t", "PROBLEMS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 10, Weight: typeset.SemiBold}, Color: th.C(theme.Accent)})
			c.Text("n", fmt.Sprintf("%d", len(ds)), mono(10.5, theme.FgFaint))
		})
		if len(ds) == 0 {
			c.Text("none", "No problems.", mono(11.5, theme.FgGhost))
		}
		for i, d := range ds {
			glyph := map[string]string{registry.SevError: "✕", registry.SevWarning: "⚠"}[d.Severity]
			in := widget.Clickable(c, i, ui.Style{Dir: ui.Row, W: ui.Flex(1), H: ui.Px(22), Gap: 10, Cross: ui.Center, Label: d.Message,
				Help: "errors#problems"}, func(c *ui.Ctx) {
				c.Box("sev", ui.Style{W: ui.Px(14)}, func(c *ui.Ctx) { c.Text("g", cmpOr(glyph, "ℹ"), mono(11, diagToken(d.Severity))) })
				c.Box("loc", ui.Style{W: ui.Px(220), Clip: true}, func(c *ui.Ctx) {
					c.Text("t", fmt.Sprintf("%s:%d", p.short(d.File), d.Line+1), mono(11.5, tLogSource))
				})
				c.Box("msg", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) { c.Text("t", firstLine(d.Message), mono(11.5, theme.Fg)) })
				c.Text("owner", d.Owner, mono(10.5, theme.FgFaint))
			})
			if in.Clicked {
				if err := p.h.Run(context.Background(), "editor.open", registry.Args{"path": d.File, "line": d.Line + 1}); err != nil {
					p.h.Notify(core.Notice{ID: "problems.open", Severity: core.SevWarning, Key: true, Text: err.Error()})
				}
			}
		}
	})
}

// short is a file as Problems shows it: from the config or work directory if it's under one, else with ~.
func (p *problems) short(file string) string {
	for _, dir := range []string{p.sh.cfg.ConfigDir, p.sh.cfg.WorkDir} {
		if rel, err := filepath.Rel(dir, file); dir != "" && err == nil && !strings.HasPrefix(rel, "..") {
			return rel
		}
	}
	return tildePath(file)
}
