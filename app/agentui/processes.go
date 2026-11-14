package agentui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit/agentkit"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// processCommands act on what the agent's last turn left running that won't wake it.
func (x *Console) processCommands() []registry.Command {
	return []registry.Command{
		{ID: "agent.process.stop", Title: "Stop a process the last turn of {session}'s agent (the session in front's) left running: {id}",
			Available: func(a registry.Args) bool { return x.canStop(x.processSession(a)) }, Run: x.stopProcess},
	}
}

func (x *Console) processSession(a registry.Args) core.SessionID {
	return core.SessionID(cmp.Or(a.String("session"), string(x.h.State().Focus)))
}

// canStop says id's provider stops what its turns leave running (agent.Features).
func (x *Console) canStop(id core.SessionID) bool {
	se, ok := x.h.Session(id)
	if !ok {
		return false
	}
	f, _ := agentkit.ProviderFeatures(x.h, se.Snapshot().Agent.Provider)
	return f.StopProcesses
}

func (x *Console) stopProcess(ctx registry.Ctx, a registry.Args) error {
	id, process := x.processSession(a), a.String("id")
	if id == "" {
		return errors.New("no session is in front")
	}
	if process == "" {
		return errors.New("which process? give its {id}")
	}
	if !x.canStop(id) {
		se, _ := x.h.Session(id)
		return fmt.Errorf("%s can't stop the processes it leaves running", services.ProviderTitle(x.h, se.Snapshot().Agent.Provider))
	}
	c, err := x.client(id)
	if err != nil {
		return err
	}
	what := process
	if se, ok := x.h.Session(id); ok {
		for _, t := range se.Snapshot().Agent.Processes {
			if t.ID == process {
				what = t.Description
			}
		}
	}
	return plugin.RunSlow(ctx, x.h, plugin.Slow{ID: "agent.process.stop." + string(id) + "." + process, Doing: "Stopping " + what},
		func(ctx context.Context) (struct{}, error) { return struct{}{}, c.StopProcess(ctx, id, process) }, nil)
}

// processUse is what's known of a process's pid and use: "pid 4242 · 1.5% cpu · 2.0 MB".
func processUse(t core.BackgroundTask) string {
	var parts []string
	if t.PID > 0 {
		parts = append(parts, fmt.Sprint("pid ", t.PID))
	}
	if t.CPU > 0 {
		parts = append(parts, fmt.Sprintf("%.1f%% cpu", t.CPU))
	}
	if t.Memory > 0 {
		parts = append(parts, kit.Bytes(t.Memory))
	}
	return strings.Join(parts, " · ")
}

// processes is the box of what the last turn left running that won't wake the agent, each with its Stop when the
// provider can.
func (x *Console) processes(c *ui.Ctx, se core.SessionState, st *consoleState, width float32, sel bool) {
	th := c.Theme()
	ps := se.Agent.Processes
	stop := x.canStop(se.ID)
	c.Column("processes", blockStyle(th, width, sel), func(c *ui.Ctx) {
		c.Row("head", ui.Style{W: ui.Flex(1), H: ui.Px(28), Pad: ui.PadXY(12, 0), Cross: ui.Center, Help: "agent/console#running"}, func(c *ui.Ctx) {
			c.Text("k", "RUNNING", kit.KickerFont(th.C(theme.FgFaint)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("n", fmt.Sprintf("%d %s · left running by the last turn", len(ps), kit.Plural(len(ps), "process", "processes")),
				kit.MonoFont(10, th.C(theme.FgFaint)))
		})
		for i, t := range ps {
			c.Box(fmt.Sprint("rule", i), ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(tBlockLine)}, nil)
			c.Row(t.ID, ui.Style{W: ui.Flex(1), H: ui.Px(34), Pad: ui.PadXY(12, 0), Gap: 10, Cross: ui.Center, Role: ui.RoleListItem, Label: t.Description},
				func(c *ui.Ctx) {
					kit.StateMark(c, "mark", kit.MarkRunning, time.Time{}, 10)
					c.Text("kind", cmp.Or(t.Kind, "process"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12.5, Weight: typeset.Medium},
						Color: th.C(theme.Fg)})
					c.Text("cmd", t.Description, kit.MonoFont(11.5, th.C(theme.FgMuted)))
					c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
					if use := processUse(t); use != "" {
						c.Text("use", use, kit.MonoFont(10, th.C(theme.FgGhost)))
					}
					if !stop || t.ID == "" {
						return
					}
					key := string(se.ID) + "/" + t.ID
					if widget.Button(c, "stop", "Stop", widget.ButtonOpts{Ghost: true, Busy: st.stopping[key], BusyLabel: "Stopping",
						Help: "agent/console#running"}) {
						st.stopping[key] = true
						done := registry.OnDone(context.Background(), func(err error) {
							delete(st.stopping, key)
							if err != nil {
								x.h.Notify(core.Notice{ID: "agent.console", Severity: core.SevError, Source: "agent", Text: err.Error()})
							}
						})
						_ = x.h.Run(done, "agent.process.stop", registry.Args{"session": string(se.ID), "id": t.ID})
					}
				})
		}
	})
}
