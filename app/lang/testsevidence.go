package lang

import (
	"cmp"
	"fmt"
	"github.com/liamg/oriel/ui/widget"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// TestRunView is a session's test run as evidence: what ran, who ran it and for which request, its counts,
// what failed, and what it printed. An instance is "<session>/<run>".
const TestRunView = "test.run"

func (x *Tests) evidence() []registry.Disposable {
	return []registry.Disposable{
		x.h.Command(registry.Command{ID: "test.run.show", Title: "Show a session's test run: {id}, of {session} (the one in front by default)",
			Run: func(ctx registry.Ctx, a registry.Args) error {
				session := cmp.Or(a.String("session"), string(x.h.State().Focus))
				id := a.String("id")
				se, ok := x.h.Session(core.SessionID(session))
				if !ok {
					return fmt.Errorf("no session %q", session)
				}
				if _, ok := se.Snapshot().Run(id); !ok {
					return fmt.Errorf("no test run %q in %s", id, session)
				}
				return x.h.Run(ctx, "view.open", registry.Args{"id": TestRunView, "instance": session + "/" + id, "place": "tab"})
			}}),
		x.h.View(TestRunView, testRunView{x}),
	}
}

type testRunView struct{ x *Tests }

func (v testRunView) Title(instance string) string {
	_, id, _ := strings.Cut(instance, "/")
	return "run " + id
}
func (v testRunView) EdgeToEdge() {}
func (v testRunView) Build(vc uireg.ViewCtx) ui.Node {
	return &ui.Func{Key: "run", Fn: func(c *ui.Ctx) { v.x.runView(c, vc.Instance) }}
}

func (x *Tests) runView(c *ui.Ctx, instance string) {
	th := c.Theme()
	session, id, _ := strings.Cut(instance, "/")
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	c.Column("run", ui.Style{W: ui.Flex(1), H: ui.Flex(1), Pad: ui.PadXY(24, 18), Gap: 12, Fill: th.C(theme.BgBase), Role: ui.RoleRegion,
		Label: "Test run " + id, Help: "editor/pane#tests"}, func(c *ui.Ctx) {
		se, ok := x.h.Session(core.SessionID(session))
		var r core.TestRun
		if ok {
			r, ok = se.Snapshot().Run(id)
		}
		if !ok {
			c.Text("none", "This run isn't in the session any more.", mono(12, theme.FgFaint))
			return
		}
		glyph, tok := "✓", tTestPassed
		if !r.OK {
			glyph, tok = "✕", tTestFailed
		}
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("g", glyph, mono(14, tok))
			c.Text("cmd", r.Command, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 13, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		})
		var meta []string
		meta = append(meta, "RUN "+r.ID, "BY "+strings.ToUpper(cmp.Or(r.By, "you")))
		if r.Thread != "" {
			meta = append(meta, "EVIDENCE FOR THREAD T:"+strings.ToUpper(string(r.Thread)[:min(4, len(r.Thread))]))
		}
		if !r.Started.IsZero() && !r.Ended.IsZero() {
			meta = append(meta, kit.Duration(r.Ended.Sub(r.Started)))
		}
		c.Text("meta", strings.Join(meta, " · "), kit.KickerFont(th.C(theme.FgFaint)))
		c.Row("counts", ui.Style{Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("p", fmt.Sprintf("%d ✓", r.Passed), mono(12, tTestPassed))
			if r.Failed > 0 {
				c.Text("f", fmt.Sprintf("%d ✕", r.Failed), mono(12, tTestFailed))
			}
			if r.Skipped > 0 {
				c.Text("s", fmt.Sprintf("%d skipped", r.Skipped), mono(12, theme.FgFaint))
			}
			if r.Dir != "" {
				c.Text("d", r.Dir, mono(11, theme.FgGhost))
			}
		})
		for i, f := range r.Failing {
			c.Text(fmt.Sprint("fail", i), "✕ "+f, mono(11.5, tTestFailed))
		}
		widget.Rule(c, "rule")
		c.Text("k", "OUTPUT", kit.KickerFont(th.C(theme.FgFaint)))
		c.Column("out", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Clip: true, Gap: 1}, func(c *ui.Ctx) {
			for i, l := range strings.Split(r.Output, "\n") {
				tok := theme.FgMuted
				if strings.Contains(l, "FAIL") {
					tok = tTestFailed
				}
				c.Text(i, strings.ReplaceAll(l, "\t", "    "), mono(11.5, tok))
			}
		})
	})
}
