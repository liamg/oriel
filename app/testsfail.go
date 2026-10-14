package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/testrun"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

var (
	tFailBg     = theme.DeclareOn("tests.failure.bg", "alpha(error, 6%)", "bg.base")
	tFailBorder = theme.Declare("tests.failure.border", "alpha(error, 45%)")
	tWantBg     = theme.DeclareOn("tests.want.bg", "alpha(warn, 18%)", "bg.base")
	tGotBg      = theme.DeclareOn("tests.got.bg", "alpha(error, 18%)", "bg.base")
	tDiffMark   = theme.Declare("tests.diff.mark", "fg")
)

// failure is a failing result with where it failed, and the run it's from.
type failure struct {
	res *testrun.Result
	run *testrun.Run
	n   int // the run's number among those kept
}

// failures are the failing results of the run in progress or the last one, that say where they failed.
func (x *testsExt) failures() []failure {
	var out []failure
	runs := x.history.Runs()
	x.mu.Lock()
	live := x.live
	x.mu.Unlock()
	seen := map[string]bool{}
	add := func(r *testrun.Run, n int) {
		for _, res := range r.Failed() {
			if res.Test != "" && res.Failure != nil && res.Failure.Loc != nil && !seen[res.Key()] {
				seen[res.Key()] = true
				out = append(out, failure{res, r, n})
			}
		}
	}
	if live != nil {
		x.mu.Lock()
		add(live, len(runs)+1)
		x.mu.Unlock()
	}
	if len(runs) > 0 {
		add(runs[len(runs)-1], len(runs))
	}
	return out
}

// failureBlocks are the failures of this file, each a block under its assertion's line (TR-2).
func (x *testsExt) failureBlocks(ctx registry.DecorationCtx, d *discovery) []registry.Decoration {
	var out []registry.Decoration
	for _, f := range x.failures() {
		loc := f.res.Failure.Loc
		if filepath.Clean(loc.File) != filepath.Clean(ctx.Path) || loc.Line < ctx.Top || loc.Line >= ctx.Bottom {
			continue
		}
		out = append(out, registry.Decoration{Kind: registry.Block, Line: loc.Line, Priority: 30,
			Node: &ui.Func{Key: "failure", Fn: func(c *ui.Ctx) { x.failureView(c, f) }}})
	}
	return out
}

// failureView is the block: the test and where it failed, want and got side by side with what differs marked, its
// output, and jump, debug, explain and fix; an explanation under it once asked for.
func (x *testsExt) failureView(c *ui.Ctx, f failure) {
	th := c.Theme()
	res, fl := f.res, f.res.Failure
	key := res.Key()
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	label := func(c *ui.Ctx, s string) {
		c.Text("label", s, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold, Tracking: .14},
			Color: th.C(theme.FgFaint)})
	}
	c.Column("failure", ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Gap: 8, Fill: th.C(tFailBg), Stroke: th.C(tFailBorder), Border: 1, Radius: 6,
		Help: "editor/pane#tests", Role: ui.RoleRegion, Label: "Failure"}, func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("x", "✕", mono(11, tTestFailed))
			c.Text("name", res.Test, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 11.5, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
			c.Text("loc", fmt.Sprintf("· %s:%d", filepath.Base(fl.Loc.File), fl.Loc.Line+1), mono(10.5, theme.FgFaint))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			parts := []string{fmt.Sprintf("run #%d", f.n)}
			if d := durationText(res.Duration); d != "" {
				parts = append(parts, d)
			}
			c.Text("run", strings.Join(append(parts, f.run.Adapter), " · "), mono(10.5, theme.FgFaint))
		})
		c.Row("cols", ui.Style{W: ui.Flex(1), Gap: 12}, func(c *ui.Ctx) {
			if fl.Want != "" || fl.Got != "" {
				a, b := diffSpan(fl.Want, fl.Got)
				for i, side := range []struct {
					name, s  string
					from, to int
					bg       theme.TokenID
				}{{"WANT", fl.Want, a, len(fl.Want) - b, tWantBg}, {"GOT", fl.Got, a, len(fl.Got) - b, tGotBg}} {
					c.Column(i, ui.Style{W: ui.Px(220), Gap: 4}, func(c *ui.Ctx) {
						label(c, side.name)
						c.Row("v", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(6, 3), Fill: th.C(side.bg), Radius: 3, Clip: true}, func(c *ui.Ctx) {
							base := mono(11.5, theme.Fg)
							mark := base
							mark.Font.Weight = typeset.Bold
							mark.Color = th.C(tDiffMark)
							to := max(side.to, side.from)
							c.Text("a", side.s[:side.from], base)
							c.Box("m", ui.Style{Fill: theme.WithAlpha(th.C(side.bg), 1)}, func(c *ui.Ctx) { c.Text("t", side.s[side.from:to], mark) })
							c.Text("z", side.s[to:], base)
						})
					})
				}
			}
			c.Column("out", ui.Style{W: ui.Flex(1), Gap: 4, Clip: true}, func(c *ui.Ctx) {
				label(c, "OUTPUT")
				lines := failureOutput(res)
				for i, l := range lines[max(0, len(lines)-6):] {
					c.Text(i, l, mono(11, theme.FgMuted))
				}
			})
		})
		c.Row("acts", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			ctx := context.Background()
			if widget.Button(c, "jump", "jump to assertion", widget.ButtonOpts{Help: "editor/pane#tests"}) {
				x.h.Run(ctx, "editor.open", registry.Args{"path": fl.Loc.File, "line": fl.Loc.Line + 1})
			}
			if widget.Button(c, "debug", "debug this case", widget.ButtonOpts{Disabled: !x.h.Available("debug.test", nil), Help: "editor/pane#tests"}) {
				x.h.Run(ctx, "debug.test", registry.Args{"key": key, "file": fl.Loc.File, "line": fl.Loc.Line + 1})
			}
			if widget.Button(c, "explain", "ask agent to explain", widget.ButtonOpts{Disabled: len(registry.Explainers(x.h)) == 0,
				Help: "editor/pane#tests"}) {
				x.explain(key)
			}
			fix := registry.Args{"text": fmt.Sprintf("Fix the failing test %s.", res.Test), "context": x.failureContext(res)}
			if widget.Button(c, "fix", "ask agent to fix", widget.ButtonOpts{Primary: true, Disabled: !x.h.Available("request.new", fix),
				Help: "editor/pane#tests"}) {
				x.h.Run(ctx, "request.new", fix)
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("since", x.failingSince(key), mono(10.5, theme.FgFaint))
		})
		x.mu.Lock()
		ex := x.explains[key]
		x.mu.Unlock()
		if ex != nil {
			popup(c, "explain", ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Gap: 6, Help: "editor/pane#tests"}, func(c *ui.Ctx) {
				label(c, "EXPLAIN · CHANGES NOTHING")
				switch {
				case ex.loading:
					c.Text("wait", "Asking…", mono(11, theme.FgFaint))
				case ex.err != nil:
					c.Text("err", ex.err.Error(), mono(11, tTestFailed))
				default:
					markdown(c, "md", lspMarkdown(ex.text), 760)
				}
			})
		}
	})
}

// diffSpan is how long the common start and end of two strings are, for marking what differs.
func diffSpan(a, b string) (prefix, suffix int) {
	for prefix < len(a) && prefix < len(b) && a[prefix] == b[prefix] {
		prefix++
	}
	for suffix < len(a)-prefix && suffix < len(b)-prefix && a[len(a)-1-suffix] == b[len(b)-1-suffix] {
		suffix++
	}
	return prefix, suffix
}

// failureOutput is a failure's output without the runner's own framing lines.
func failureOutput(res *testrun.Result) []string {
	var out []string
	for _, l := range res.Output {
		t := strings.TrimSpace(l)
		if t == "" || strings.HasPrefix(t, "=== RUN") || strings.HasPrefix(t, "=== PAUSE") || strings.HasPrefix(t, "=== CONT") {
			continue
		}
		out = append(out, strings.TrimRight(l, "\n"))
	}
	if len(out) == 0 && res.Failure != nil && res.Failure.Message != "" {
		out = strings.Split(res.Failure.Message, "\n")
	}
	return out
}

// failingSince is how long the test has failed: "first failed 2 runs ago".
func (x *testsExt) failingSince(key string) string {
	es := x.history.Entries(key)
	n := 0
	for i := len(es) - 1; i >= 0 && es[i].Status.Failing(); i-- {
		n++
	}
	switch {
	case n <= 1:
		return "first failed this run"
	default:
		return fmt.Sprintf("first failed %d %s ago", n-1, plural(n-1, "run", "runs"))
	}
}

// failureContext is what an explainer or the agent gets: the failure, its output, and the test's code.
func (x *testsExt) failureContext(res *testrun.Result) string {
	var b strings.Builder
	fl := res.Failure
	fmt.Fprintf(&b, "Test %s in %s failed", res.Test, res.Package)
	if fl.Loc != nil {
		fmt.Fprintf(&b, " at %s:%d", fl.Loc.File, fl.Loc.Line+1)
	}
	b.WriteString(".\n")
	if fl.Want != "" || fl.Got != "" {
		fmt.Fprintf(&b, "want %s\ngot  %s\n", fl.Want, fl.Got)
	}
	b.WriteString("\nOutput:\n" + strings.Join(failureOutput(res), "\n") + "\n")
	if fl.Loc != nil {
		if src, err := os.ReadFile(fl.Loc.File); err == nil {
			lines := strings.Split(string(src), "\n")
			from, to := max(0, fl.Loc.Line-15), min(len(lines), fl.Loc.Line+10)
			b.WriteString("\nThe test, around the assertion:\n" + strings.Join(lines[from:to], "\n") + "\n")
		}
	}
	return b.String()
}

// explain asks the first explainer about a failure; its answer shows under the failure.
func (x *testsExt) explain(key string) error {
	es := registry.Explainers(x.h)
	if len(es) == 0 {
		return fmt.Errorf("nothing can explain yet: it needs an agent")
	}
	res, _ := x.result(key)
	if res == nil || res.Failure == nil {
		return fmt.Errorf("no failure to explain")
	}
	ex := &explanation{loading: true}
	x.mu.Lock()
	x.explains[key] = ex
	x.mu.Unlock()
	req := registry.ExplainRequest{Kind: "test-failure", Subject: res.Test + " failed", Context: x.failureContext(res)}
	if res.Failure.Loc != nil {
		req.Files = []string{res.Failure.Loc.File}
	}
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		var text string
		err := registry.Call(func() (err error) { text, err = es[0].Explain(ctx, req); return err })
		x.h.Do(func() {
			x.mu.Lock()
			ex.loading, ex.text, ex.err = false, text, err
			x.mu.Unlock()
			if err != nil {
				x.h.Notify(core.Notice{ID: "tests.explain", Severity: core.SevWarning, Key: true, Text: err.Error()})
			}
		})
	}()
	return nil
}
