package app

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/ext"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/text"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// fix is agent.fix: a request with the trouble attached, about its file and line.
func (x *explainExt) fix(ctx registry.Ctx, a registry.Args) error {
	t := registry.TroubleOf(a)
	args := registry.Args{"text": t.FixText(), "context": strings.TrimSpace(t.Subject + "\n\n" + t.Context)}
	if t.File != "" {
		args["file"], args["line"] = t.File, max(t.Line, 1)
	}
	return x.h.Run(ctx, "request.new", args)
}

// keep asks about req off the loop, its answer kept under key for explainKept to show where it was asked.
func (x *explainExt) keep(key string, req registry.ExplainRequest) {
	a := &answer{loading: true}
	x.mu.Lock()
	x.kept[key] = a
	x.mu.Unlock()
	x.busy.Add(1)
	go func() {
		defer x.busy.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		md, by, err := registry.Explain(ctx, x.h, req)
		label := ""
		if by.Label != nil {
			registry.Call(func() error { label = by.Label(); return nil })
		}
		x.h.Do(func() {
			x.mu.Lock()
			a.text, a.err, a.loading, a.by = md, err, false, label
			x.mu.Unlock()
			if err != nil {
				x.notify(err)
			}
		})
	}()
}

// keptAnswer is the answer asked for under key, if one was.
func (x *explainExt) keptAnswer(key string) (answer, bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	a, ok := x.kept[key]
	if !ok {
		return answer{}, false
	}
	return *a, true
}

// askTwins are "ask agent to explain" and "ask agent to fix" about t, side by side (ide.md, "Explain sits next to
// fix everywhere"). Explain answers under key, which explainKept draws where it was asked; fix writes a request.
func askTwins(c *ui.Ctx, h ext.Host, key string, t registry.Trouble, help string) {
	askTwinsLabelled(c, h, key, t, help, "ask agent to explain", "ask agent to fix")
}

// askTwinsLabelled is askTwins with its buttons' own words ("Explain with agent", "Fix CI with agent").
func askTwinsLabelled(c *ui.Ctx, h ext.Host, key string, t registry.Trouble, help, explainLabel, fixLabel string) {
	fix := t.Args()
	explain := maps.Clone(fix)
	explain["key"] = key
	run := func(cmd string, a registry.Args) {
		if err := h.Run(context.Background(), cmd, a); err != nil {
			h.Notify(core.Notice{ID: cmd, Severity: core.SevWarning, Source: "explain", Key: true, Text: err.Error()})
		}
	}
	if widget.Button(c, "explain", explainLabel, widget.ButtonOpts{Disabled: !h.Available(registry.ExplainCommand, explain), Help: help}) {
		run(registry.ExplainCommand, explain)
	}
	if widget.Button(c, "fix", fixLabel, widget.ButtonOpts{Primary: true, Disabled: !h.Available(registry.FixCommand, fix), Help: help}) {
		run(registry.FixCommand, fix)
	}
}

// explainKept is the answer askTwins asked for under key, once it was: an explain card that changes nothing.
func explainKept(c *ui.Ctx, sh *shell, key, help string) {
	if sh.explain == nil {
		return
	}
	a, ok := sh.explain.keptAnswer(key)
	if !ok {
		return
	}
	th := c.Theme()
	mono := func(size float32, tok theme.TokenID) ui.TextStyle {
		return ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: size}, Color: th.C(tok)}
	}
	popup(c, "explain", ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Gap: 6, Help: help}, func(c *ui.Ctx) {
		c.Text("label", "EXPLAIN · CHANGES NOTHING", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9.5, Weight: typeset.SemiBold,
			Tracking: .14}, Color: th.C(theme.FgFaint)})
		switch {
		case a.loading:
			c.Text("wait", "Asking…", mono(11, theme.FgFaint))
		case a.err != nil:
			c.Text("err", a.err.Error(), mono(11, theme.Error))
		default:
			markdown(c, "md", lspMarkdown(a.text), 760)
			if a.by != "" {
				c.Text("by", a.by, mono(10, theme.FgFaint))
			}
		}
	})
}

// diagnosticTrouble is the diagnostics on one line as something to fix or explain: what they say, and the code
// around them.
func diagnosticTrouble(snap text.Snapshot, ds []registry.Diagnostic) registry.Trouble {
	d := ds[0]
	var msgs []string
	for _, x := range ds {
		msgs = append(msgs, fmt.Sprintf("%s %s: %s", x.Owner, x.Severity, x.Message))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d:\n%s\n", d.File, d.Line+1, strings.Join(msgs, "\n"))
	if snap.Lines() > d.Line {
		from, to := max(0, d.Line-5), min(snap.Lines()-1, d.Line+5)
		b.WriteString("\nThe code around it:\n```\n")
		for l := from; l <= to; l++ {
			b.WriteString(strings.TrimRight(string(snap.Line(l)), "\r\n") + "\n")
		}
		b.WriteString("```\n")
	}
	where := fmt.Sprintf("%s:%d", filepath.Base(d.File), d.Line+1)
	return registry.Trouble{Kind: "diagnostic", Subject: fmt.Sprintf("%s at %s: %s", d.Owner, where, firstLine(d.Message)), Context: b.String(),
		File: d.File, Line: d.Line + 1, EndLine: d.Line + 1, Fix: fmt.Sprintf("Fix the %s %s at %s: %s", d.Owner, d.Severity, where, firstLine(d.Message))}
}
