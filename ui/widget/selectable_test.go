package widget

import (
	"strings"
	"testing"

	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
)

// dragSelect drags across the first run of text that starts with prefix and answers what's selected.
func (h *harness) dragSelect(prefix string) string {
	h.t.Helper()
	var at *render.Op
	for i, op := range h.list.Ops {
		if op.Kind == render.OpGlyphs && strings.HasPrefix(op.Run.Text, prefix) {
			at = &h.list.Ops[i]
			break
		}
	}
	if at == nil {
		h.t.Fatalf("no %q drawn", prefix)
	}
	y := at.At.Y - at.Run.Ascent/2
	h.drag(render.Point{X: at.At.X + 12, Y: y}, render.Point{X: at.At.X + at.Run.Advance - 1, Y: y})
	return h.u.SelectedText()
}

// What a message says, in a window, a notice, an ask's card or a prompt, selects to be copied, though the box it's in
// takes focus and clicks: an error is read out of it, not retyped.
func TestMessagesInCardsAndWindowsSelect(t *testing.T) {
	const msg = "dial tcp 10.0.0.4:443: connection refused"
	for name, build := range map[string]func(c *ui.Ctx){
		"window": func(c *ui.Ctx) {
			Window(c, "w", &WindowState{}, WindowOpts{Title: "ORI", Default: render.XYWH(20, 20, 600, 300)}, func(c *ui.Ctx) {
				c.Text("t", msg, ui.TextStyle{})
			})
		},
		"notice": func(c *ui.Ctx) {
			NoticeCard(c, "n", NoticeCardOpts{Title: "Agent failed", Body: msg, Close: true, Width: 600})
		},
		"ask card": func(c *ui.Ctx) {
			ActionCard(c, "a", ActionCardOpts{Kicker: "SANDBOX", Title: "Couldn't start", Detail: msg, Width: ui.Px(600),
				Actions: []CardAction{{Label: "Retry"}}}, nil)
		},
		"prompt": func(c *ui.Ctx) {
			PromptCard(c, "p", PromptCardOpts{Title: msg, Options: []PromptOption{{Key: "y", Label: "Allow"}}})
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t, theme.Dark, render.Point{X: 900, Y: 600}, build)
			h.frame()
			if got := h.dragSelect("dial tcp"); !strings.Contains(got, "connection refused") {
				t.Errorf("a drag across the message selected %q", got)
			}
		})
	}
}
