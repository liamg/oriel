package sessions

import (
	"cmp"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// StartWorkKeyMode is the Start work dialog's keymap mode: the dialog and its fields.
const StartWorkKeyMode = "startwork"

const startWorkHelp = "agent/plan#start-work"

// startWork is the Start work dialog: how the plan becomes work, run through
// session.start-work.
type startWork struct {
	id               core.SessionID
	clear, fresh     bool
	sandbox, openNet bool
	backend          string // the session's sandbox backend; "" is sandbox.backend
	model, effort    string
	shown            [2]string         // the model and effort it opened on: only a change is sent
	shownRun         [2]bool           // and the sandbox and open network
	provider         host.ProviderInfo // the session's provider, once the host says
	providers        *plugin.Load[host.ProviderInfo]
	branches         []startBranch
	focus            bool // focus the dialog next frame
	busy             bool
	err              string
}

// startBranch is a project's branch as the dialog edits it.
type startBranch struct{ project, branch, planned string }

// openStartWork opens the dialog on the plan in front, filled from the session: its branches, sandbox, network,
// model and effort.
func (x *Plan) openStartWork() error {
	se, ok := x.front()
	if !ok || !se.Planning() {
		return fmt.Errorf("no plan is in front to start work on")
	}
	d := &startWork{id: se.ID, clear: true, fresh: true, sandbox: se.Agent.Sandboxed, openNet: se.Agent.Network == "open", backend: se.Agent.Backend,
		model: se.Agent.Model, effort: se.Agent.Effort, shown: [2]string{se.Agent.Model, se.Agent.Effort}, focus: true,
		shownRun: [2]bool{se.Agent.Sandboxed, se.Agent.Network == "open"},
		provider: host.ProviderInfo{ID: se.Agent.Provider}}
	for _, p := range se.Projects {
		if p.ReadOnly && p.Repo != "" {
			d.branches = append(d.branches, startBranch{project: p.Name, branch: p.Branch, planned: p.Branch})
		}
	}
	x.startGen++
	x.dialog = d
	if se.Agent.Provider != "" {
		id := se.Agent.Provider
		d.providers = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) (host.ProviderInfo, error) {
			c, err := kit.SessionHost(x.h)
			if err != nil {
				return host.ProviderInfo{}, err
			}
			for _, p := range c.ProviderInfo(ctx) {
				if p.ID == id {
					return p, nil
				}
			}
			return host.ProviderInfo{}, fmt.Errorf("%s isn't set up on this machine", kit.ProviderName(id))
		}, plugin.LoadOpts{Timeout: 20 * time.Second})
		d.providers.Start()
	}
	return nil
}

// settle takes the provider's models once they arrive, and its default model when the session has none.
func (d *startWork) settle() {
	if d.providers == nil {
		return
	}
	p, ok := d.providers.Value()
	if !ok || len(d.provider.Models) > 0 {
		return
	}
	d.provider = p
	if d.model == "" && d.shown[0] == "" {
		d.model = kit.DefaultModel(p)
		d.shown[0] = d.model
	}
}

func (x *Plan) closeStartWork() {
	if x.dialog != nil && x.dialog.providers != nil {
		x.dialog.providers.Reset()
	}
	x.dialog = nil
	x.startGen++
}

// args are session.start-work's for what the dialog holds.
func (d *startWork) args() registry.Args {
	a := registry.Args{"id": string(d.id), "clear": d.clear, "fresh": d.fresh}
	if d.model != d.shown[0] || d.effort != d.shown[1] {
		a["model"], a["effort"] = d.model, d.effort
	}
	if d.sandbox != d.shownRun[0] {
		a["sandbox"] = d.sandbox
	}
	if d.sandbox && (d.sandbox != d.shownRun[0] || d.openNet != d.shownRun[1]) {
		a["network"] = "ask"
		if d.openNet {
			a["network"] = "open"
		}
	}
	branches := map[string]any{}
	for _, b := range d.branches {
		if v := strings.TrimSpace(b.branch); v != "" && v != b.planned {
			branches[b.project] = v
		}
	}
	if len(branches) > 0 {
		a["branches"] = branches
	}
	return a
}

// runStartWork starts work as the dialog says; it closes once the session host has.
func (x *Plan) runStartWork() error {
	d := x.dialog
	if d == nil || d.busy {
		return nil
	}
	a := d.args()
	d.busy, d.err = true, ""
	gen, life := x.startGen, x.life
	go func() {
		err := plugin.Await(life, func(ctx registry.Ctx) error { return x.h.Run(ctx, "session.start-work", a) })
		x.h.Do(func() {
			if gen != x.startGen || x.dialog == nil {
				return
			}
			x.dialog.busy = false
			if err != nil {
				x.dialog.err = err.Error()
				return
			}
			x.closeStartWork()
		})
		x.h.Redraw()
	}()
	return nil
}

// startWorkView is the dialog, over everything, while it's open.
func (x *Plan) startWorkView(c *ui.Ctx) {
	d := x.dialog
	if d == nil {
		return
	}
	se, ok := x.h.State().Session(d.id)
	if !ok {
		x.closeStartWork()
		return
	}
	d.settle()
	th := c.Theme()
	size := c.Size()
	centre := render.Point{X: size.X / 2, Y: size.Y / 2}
	closed := widget.Scrim(c, "startwork", widget.ScrimOpts{Z: ui.LayerDialog, Fill: th.C(kit.TScrimOmni), Help: startWorkHelp}, func(c *ui.Ctx) {
		box := c.Next("box")
		if d.focus {
			c.RequestFocus(box)
			d.focus = false
		}
		c.Column("box", ui.Style{W: ui.Px(min(600, size.X-32)), Fill: th.C(kit.TOmniBg), Stroke: th.C(kit.TOmniBorder), Border: 1, Radius: 10, Clip: true,
			Trap: true, Interactive: true, Focusable: true, KeyMode: StartWorkKeyMode, Float: &ui.Float{Point: &centre, Self: render.Point{X: .5, Y: .5}},
			Role: ui.RoleDialog, Label: "Start work", Help: startWorkHelp}, func(c *ui.Ctx) {
			x.startWorkHeader(c, se.Snapshot())
			c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 24, R: 24, T: 18, B: 20}, Gap: 20}, func(c *ui.Ctx) {
				x.startWorkBody(c, d, se.Snapshot())
			})
			c.Box("rule2", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
			x.startWorkFooter(c, d)
		})
		for _, k := range c.Interaction(box).Keys {
			if kit.Notation(k) == "<Esc>" {
				x.closeStartWork()
			}
		}
	})
	if closed {
		x.closeStartWork()
	}
}

func (x *Plan) startWorkHeader(c *ui.Ctx, se core.SessionState) {
	th := c.Theme()
	c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(52), Pad: ui.PadXY(20, 0), Gap: 14, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("title", "START WORK", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.Bold, Tracking: .24},
			Color: th.C(theme.Fg)})
		c.Text("meta", se.Name, kit.MonoFont(10.5, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "close", "esc", widget.ButtonOpts{Ghost: true, Help: startWorkHelp}) {
			x.closeStartWork()
		}
	})
}

// threadCounts are the plan's threads Start work clears: those that go, and those carried into the work prompt.
func threadCounts(se core.SessionState) (gone, carried int) {
	plan := se.PlanThread()
	for _, t := range se.Threads {
		switch {
		case t.ID == plan:
		case !t.Btw && len(t.Comments) > 0 && (t.Open() || t.Status == core.ThreadDeferred):
			carried++
		default:
			gone++
		}
	}
	return gone, carried
}

func (x *Plan) startWorkBody(c *ui.Ctx, d *startWork, se core.SessionState) {
	th := c.Theme()
	c.Column("threads", ui.Style{W: ui.Flex(1), Gap: 2}, func(c *ui.Ctx) {
		gone, carried := threadCounts(se)
		kit.Heading(c, "head", "Threads", fmt.Sprintf("%d %s · %d carried", gone, kit.Plural(gone, "goes", "go"), carried))
		kit.ToggleRow(c, "clear", "Clear the plan's threads", "resolved go · open and deferred go into the prompt", &d.clear, false, startWorkHelp)
	})
	c.Column("agent", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
		kit.Heading(c, "head", "Agent", strings.TrimSpace(kit.ProviderName(d.provider.ID)+" · model · effort"))
		note := "from the plan docs and the work pack"
		if !d.fresh {
			note = "keeps the plan's conversation"
		}
		kit.ToggleRow(c, "fresh", "Restart the agent with a fresh context", note, &d.fresh, se.Agent.Provider == "", startWorkHelp)
		models := func(c *ui.Ctx) {
			p := d.provider
			if len(p.Models) == 0 && d.model != "" {
				p.Models = []agent.Model{{ID: d.model}}
			}
			if len(p.Models) > 0 {
				if m, e, ok := kit.ModelSelects(c, p, d.model, d.effort, false, startWorkHelp); ok {
					d.model, d.effort = m, e
				}
			}
		}
		c.Column("models", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
			models(c) // the session's model until its provider's list arrives
			if d.providers != nil {
				widget.Async(c, "load", d.providers.State(), widget.AsyncOpts{Policy: widget.Optional, Label: "Reading models",
					Fallback: "the session's model", Retry: d.providers.Reload, Help: startWorkHelp}, func(*ui.Ctx) {})
			}
		})
	})
	if len(d.branches) > 0 {
		c.Column("branches", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			kit.Heading(c, "head", "Branches", "one worktree each")
			for i := range d.branches {
				b := &d.branches[i]
				c.Row(i, ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
					c.Box("name", ui.Style{W: ui.Px(140), Clip: true}, func(c *ui.Ctx) {
						c.Text("t", b.project, kit.MonoFont(11.5, th.C(theme.Fg)))
					})
					r := widget.Input(c, "branch", &b.branch, widget.InputOpts{Label: "Branch for " + b.project, Placeholder: b.planned,
						Help: startWorkHelp, TabMoves: true, KeyMode: StartWorkKeyMode, Width: ui.Flex(1)})
					if r.Cancelled {
						x.closeStartWork()
					}
				})
			}
		})
	}
	c.Column("run", ui.Style{W: ui.Flex(1), Gap: 10}, func(c *ui.Ctx) {
		kit.Heading(c, "head", "Run on", "")
		kit.SandboxToggles(c, &d.sandbox, &d.openNet, kit.BackendPick{Setting: cmp.Or(d.backend, kit.SettingBackend(x.h))}, startWorkHelp)
	})
}

func (x *Plan) startWorkFooter(c *ui.Ctx, d *startWork) {
	th := c.Theme()
	c.Row("foot", ui.Style{W: ui.Flex(1), H: ui.Px(60), Pad: ui.PadXY(24, 0), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Column("about", ui.Style{W: ui.Flex(1), Clip: true}, func(c *ui.Ctx) {
			if d.err != "" {
				c.Text("err", d.err, kit.MonoFont(11, th.C(theme.Error)))
				return
			}
			c.Text("t", "Worktrees first, then the agent builds the plan.", kit.MonoFont(11, th.C(theme.FgMuted)))
		})
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Key: "esc", Help: startWorkHelp}) {
			x.closeStartWork()
		}
		if widget.Button(c, "start", "Start work", widget.ButtonOpts{Primary: true, Key: "⌘↵", Busy: d.busy, BusyLabel: "Starting",
			Disabled: !x.h.Available("session.start-work", nil), Help: startWorkHelp}) {
			_ = x.runStartWork()
		}
	})
}
