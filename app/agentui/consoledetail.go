package agentui

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// detailOpen is the console's details dialog: a tool call or an ask, in full.
type detailOpen struct {
	session core.SessionID
	item    string // a tool call's transcript item
	ask     string // an ask's id
	scroll  kit.Scroll
}

// detailCommand is agent.console.details: {item} or {ask} of {id} (the session in front) in a dialog, in full.
func (x *Console) detailCommand() registry.Command {
	return registry.Command{ID: "agent.console.details", Title: "Show a tool call {item} or an ask {ask} of session {id} in full, with its status, input and result",
		Run: func(_ registry.Ctx, a registry.Args) error {
			id := core.SessionID(a.String("id"))
			if id == "" {
				se, ok := x.front()
				if !ok {
					return fmt.Errorf("no session is in front")
				}
				id = se.ID
			}
			if a.String("item") == "" && a.String("ask") == "" {
				return fmt.Errorf("say which: {item} or {ask}")
			}
			x.detail = &detailOpen{session: id, item: a.String("item"), ask: a.String("ask")}
			return nil
		}}
}

// detailView draws the details dialog over everything, from the session's state each frame: a call's result lands
// while it's open, an ask answered elsewhere closes it.
func (x *Console) detailView() uireg.View {
	return uireg.ViewFunc(func(uireg.ViewCtx) ui.Node {
		return &ui.Func{Key: "console-detail", Fn: func(c *ui.Ctx) {
			d := x.detail
			if d == nil {
				return
			}
			ref, ok := x.h.Session(d.session)
			if !ok {
				x.detail = nil
				return
			}
			se := ref.Snapshot()
			var it core.Item
			var ask core.Ask
			if d.item != "" {
				i := slices.IndexFunc(se.Transcript, func(it core.Item) bool { return it.ID == d.item })
				if i < 0 {
					x.detail = nil
					return
				}
				it = se.Transcript[i]
			} else if ask, ok = x.ask(se.ID, d.ask); !ok {
				x.detail = nil
				return
			}
			th := c.Theme()
			size := c.Size()
			centre := render.Point{X: size.X / 2, Y: size.Y / 2}
			w := min(860, size.X-32)
			if widget.Scrim(c, "detail", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: "agent/console#details"}, func(c *ui.Ctx) {
				box := c.Next("box")
				for _, k := range c.Interaction(box).Keys {
					if k.Key == "Esc" {
						x.detail = nil
					}
				}
				c.Column("box", ui.Style{Focusable: true, DefaultFocus: true, KeyMode: widget.ScrimKeyMode, W: ui.Px(w), H: ui.Px(min(700, size.Y-48)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1,
					Radius: 10, Clip: true, Trap: true, Interactive: true, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
					Role: ui.RoleDialog, Label: "Details", Help: "agent/console#details"}, func(c *ui.Ctx) {
					c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(48), Pad: ui.PadXY(18, 0), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
						title := "Ask · " + string(ask.Kind)
						if d.item != "" {
							title = "Tool call · " + cmp.Or(it.Tool, "tool")
						}
						c.Text("t", strings.ToUpper(title), kit.MonoFont(11, th.C(theme.FgMuted)))
						c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
						if widget.IconButton(c, "close", "✕", widget.IconButtonOpts{Label: "Close", Help: "agent/console#details"}) {
							x.detail = nil
						}
					})
					c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
					kit.Scrolled(c, "body", &d.scroll, ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, ui.Style{Pad: ui.Pad(18), Gap: 14},
						func(c *ui.Ctx, _ func(string) ui.ID) {
							if d.item != "" {
								x.toolDetail(c, se, it, w-40)
							} else {
								x.askDetail(c, se, ask, w-40)
							}
						})
				})
			}) {
				x.detail = nil
			}
		}}
	})
}

// toolDetail is a call in full: its status, what it ran, and its result once it has one.
func (x *Console) toolDetail(c *ui.Ctx, se core.SessionState, it core.Item, width float32) {
	th := c.Theme()
	c.Row("status", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		switch {
		case !it.Done:
			widget.Spinner(c, "spin", widget.SpinnerOpts{Name: "Running", Help: "agent/console#details"})
			c.Text("s", "running", kit.MonoFont(11, th.C(tToolRunning)))
		case it.IsError:
			c.Text("s", "failed", kit.MonoFont(11, th.C(kit.TToolFailed)))
		default:
			c.Text("s", "done", kit.MonoFont(11, th.C(kit.TToolOK)))
		}
		if d := msText(it.Duration); d != "" {
			c.Text("d", d, kit.MonoFont(11, th.C(theme.FgFaint)))
		}
		if r, ok := runOf(se, it.ID); ok {
			kit.EvidenceChip(c, "run", x.h, se.ID, r.ID, kit.RunLabel(r), r.OK)
		}
	})
	if isShell(it.Tool) {
		detailBlock(c, "cmd", "COMMAND", "$ "+kit.MainInput(it.Input), width, th.C(theme.Fg))
	} else if it.Title != "" {
		detailBlock(c, "what", "CALL", it.Title, width, th.C(theme.Fg))
	}
	detailBlock(c, "input", "INPUT", prettyJSON(it.Input), width, th.C(theme.FgMuted))
	switch {
	case !it.Done:
		c.Row("wait", ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Spinner(c, "spin", widget.SpinnerOpts{Name: "Waiting for the result", Size: 12, Help: "agent/console#details"})
			c.Text("t", "waiting for the result…", kit.MonoFont(11, th.C(theme.FgFaint)))
		})
	case it.Diff != nil:
		c.Text("dk", "RESULT", kit.MonoFont(9.5, th.C(theme.FgFaint)))
		miniDiff(c, it.Diff)
	default:
		tone := th.C(theme.FgMuted)
		if it.IsError {
			tone = th.C(theme.Error)
		}
		detailBlock(c, "out", "RESULT", cmp.Or(it.Output, "(nothing)"), width, tone)
	}
}

// askDetail is an ask in full, with its card's answers under it.
func (x *Console) askDetail(c *ui.Ctx, se core.SessionState, a core.Ask, width float32) {
	th := c.Theme()
	if a.Tool != "" {
		detailBlock(c, "tool", "TOOL", a.Tool, width, th.C(theme.Fg))
	}
	if a.Summary != "" {
		detailBlock(c, "sum", map[bool]string{true: "COMMAND", false: "WHAT"}[isShell(a.Tool)], a.Summary, width, th.C(theme.Fg))
	}
	if a.Input != "" {
		detailBlock(c, "input", "INPUT", prettyJSON(a.Input), width, th.C(theme.FgMuted))
	}
	if a.Detail != "" {
		detailBlock(c, "detail", "DETAIL", a.Detail, width, th.C(theme.FgMuted))
	}
	x.askCard(c, "card", se, a, false, false, nil)
}

// detailBlock is a captioned block of text, wrapped, in the mono face.
func detailBlock(c *ui.Ctx, key, caption, text string, width float32, tone render.Color) {
	th := c.Theme()
	c.Column(key, ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		c.Text("k", caption, kit.MonoFont(9.5, th.C(theme.FgFaint)))
		c.Column("v", ui.Style{W: ui.Flex(1), Pad: ui.Pad(10), Gap: 1, Fill: th.C(theme.BgBase), Radius: 6}, func(c *ui.Ctx) {
			lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
			if len(lines) > 2000 {
				lines = append(lines[:1000:1000], append([]string{fmt.Sprintf("… %d lines …", len(lines)-2000)}, lines[len(lines)-1000:]...)...)
			}
			for i, ln := range lines {
				widget.Wrap(c, i, kit.ExpandTabs(ln), width-20, kit.MonoFont(11.5, tone))
			}
		})
	})
}

// prettyJSON is s indented, or s as it is when it isn't JSON.
func prettyJSON(s string) string {
	var b bytes.Buffer
	if json.Indent(&b, []byte(s), "", "  ") != nil {
		return s
	}
	return b.String()
}
