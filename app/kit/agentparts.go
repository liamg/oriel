package kit

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// A tool call's tones: it went through, or it failed.
var (
	TToolOK     = theme.Declare("agent.tool.ok", "ok")
	TToolFailed = theme.Declare("agent.tool.failed", "error")
)

// ParamString is an agent tool's string parameter k, trimmed; a number as it's written.
func ParamString(p map[string]any, k string) string {
	switch v := p[k].(type) {
	case string:
		return strings.TrimSpace(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return ""
}

// CountPill is a small round count: a tab's cards waiting, the inbox's.
func CountPill(c *ui.Ctx, key any, n int, fill render.Color) {
	th := c.Theme()
	c.Box(key, ui.Style{H: ui.Px(15), Pad: ui.PadXY(5, 0), Radius: 7.5, Fill: fill, Cross: ui.Center, Role: ui.RoleText,
		Label: fmt.Sprintf("%d waiting on you", n)}, func(c *ui.Ctx) {
		c.Text("n", fmt.Sprint(n), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Mono, Size: 9, Weight: typeset.Bold}, Color: th.C(theme.AccentInk)})
	})
}

// RunLabel is a run's counts, as its evidence chip says them.
func RunLabel(r core.TestRun) string {
	if r.Failed > 0 {
		return fmt.Sprintf("%d failed, %d passed", r.Failed, r.Passed)
	}
	return fmt.Sprintf("%d passed", r.Passed)
}

// EvidenceChip is a link to a test run: ✓ or ✕, its counts and id; a click opens the run.
func EvidenceChip(c *ui.Ctx, h plugin.Host, key any, session core.SessionID, run, label string, ok bool) {
	th := c.Theme()
	tone, glyph := th.C(TToolOK), "✓"
	if !ok {
		tone, glyph = th.C(TToolFailed), "✕"
	}
	if widget.Clickable(c, key, ui.Style{Dir: ui.Row, H: ui.Px(20), Pad: ui.PadXY(7, 0), Gap: 6, Cross: ui.Center, Radius: 10, Fill: theme.WithAlpha(tone, .08),
		Stroke: theme.WithAlpha(tone, .4), Border: 1, Focusable: true, Label: "Test run " + run + ": " + label, Tooltip: "Open the run",
		Help: "agent/requests#evidence"}, func(c *ui.Ctx) {
		c.Text("g", glyph, MonoFont(10.5, tone))
		c.Text("l", label, MonoFont(10.5, tone))
		c.Text("r", "run "+run, MonoFont(10, th.C(theme.FgFaint)))
	}).Clicked {
		_ = h.Run(context.Background(), "test.run.show", registry.Args{"id": run, "session": string(session)})
	}
}

var TMeterContext = theme.Declare("agent.meter.context", "amber")

// StartingHelp is the help on an agent starting.
const StartingHelp = "agent/console#starting"
