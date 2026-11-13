package stats

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// Machine is this machine's load in the HUD, and the status line, from the session
// host's MachineChanged readings.
type Machine struct {
	kit.Disposer
	Load *sysload.Sampler // the machine's load, sampled while it's on screen
	h    plugin.Host

	mu   sync.Mutex
	load []loadReading // CPU over the last loadSpan

	shown *kit.WhileShown // the sampler, wanted while the load is on screen
}

// loadSpan is how far back the machine's load is drawn.
const loadSpan = 15 * time.Minute

// loadReading is the machine's CPU at a time.
type loadReading struct {
	at  time.Time
	cpu float64
}

func (x *Machine) Manifest() plugin.Manifest { return plugin.Manifest{ID: "machine"} }

func (x *Machine) Activate(_ context.Context, h plugin.Host) error {
	x.h = h
	x.shown = &kit.WhileShown{Want: func() func() { return func() {} }, Focus: h}
	if x.Load != nil {
		x.shown.Want = x.Load.Want
	}
	x.Add(h.Framed(x.shown.Framed))
	x.Add(registry.DisposeFunc(x.shown.Drop))
	x.Add(h.Subscribe(core.Filter{}, func(ev core.Event) {
		if ev, ok := ev.(core.MachineChanged); ok {
			x.sampled(ev.Machine)
		}
	}))
	x.Add(h.Contribute(uireg.Indicator{Name: "machine", Slot: uireg.SlotHUD, Order: 20, Title: "This machine", Help: kit.HUDHelp + "#machine",
		Note:  func() string { return fmt.Sprintf("%dm", int(loadSpan.Minutes())) },
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "machine", Fn: x.panel}, nil }}))
	x.Add(h.Contribute(uireg.Indicator{Name: "machine.status", Slot: uireg.SlotStatusRight, Order: 70, Global: true, Help: kit.HUDHelp + "#indicators",
		Build: func(registry.Ctx) (ui.Node, error) { return &ui.Func{Key: "machine", Fn: x.status}, nil }}))
	return nil
}

// sampled keeps a reading, dropping those past loadSpan.
func (x *Machine) sampled(m core.MachineState) {
	if m.Sampled.IsZero() {
		return
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	rs := x.load
	if n := len(rs); n > 0 && !m.Sampled.After(rs[n-1].at) {
		if m.Sampled.Equal(rs[n-1].at) {
			return
		}
		rs = nil // the clock went back
	}
	rs = append(rs, loadReading{m.Sampled, m.CPU})
	for len(rs) > 0 && m.Sampled.Sub(rs[0].at) > loadSpan {
		rs = rs[1:]
	}
	x.load = rs
}

func (x *Machine) loadOf() []float64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	var out []float64
	for _, r := range x.load {
		out = append(out, r.cpu)
	}
	return out
}

func loadTone(th *theme.Resolved, cpu float64) render.Color {
	if cpu >= .8 {
		return th.C(theme.Warn)
	}
	return th.C(theme.StatusWorking)
}

// panel is the HUD's: the machine's sessions, CPU and memory, and its CPU over the last 15 minutes.
func (x *Machine) panel(c *ui.Ctx) {
	x.shown.Keep()
	th := c.Theme()
	st := x.h.State()
	m := st.Machine
	c.Column("machine", ui.Style{W: ui.Flex(1), Gap: 5}, func(c *ui.Ctx) {
		c.Row("h", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
			kit.HUDDot(c, "dot", th.C(theme.StatusQuiet))
			c.Text("n", cmp.Or(m.Host, "this machine"), ui.TextStyle{Font: typeset.FontSpec{Family: typeset.UI, Size: 12, Weight: typeset.SemiBold},
				Color: th.C(theme.Fg)})
			if m.Cores > 0 {
				c.Text("w", fmt.Sprintf("%d cores", m.Cores), kit.MonoFont(10, th.C(theme.FgFaint)))
			}
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			c.Text("s", kit.Counted(len(st.Sessions), "session"), kit.MonoFont(10.5, th.C(theme.FgMuted)))
		})
		c.Row("load", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
			c.Column("kv", ui.Style{Gap: 4}, func(c *ui.Ctx) {
				mem := 0.0
				if m.MemTotal > 0 {
					mem = float64(m.Mem) / float64(m.MemTotal)
				}
				for _, r := range []struct {
					k, v string
					f    float64
					tone render.Color
				}{{"CPU", fmt.Sprintf("%d%%", kit.Pct(m.CPU)), m.CPU, loadTone(th, m.CPU)}, {"MEM", kit.Bytes(int64(m.Mem)), mem, th.C(theme.StatusWorking)}} {
					c.Row(r.k, ui.Style{Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
						c.Row("k", ui.Style{W: ui.Px(28)}, func(c *ui.Ctx) { c.Text("t", r.k, kit.MonoFont(10, th.C(theme.FgMuted))) })
						widget.Meter(c, "m", r.f, widget.MeterOpts{Tone: r.tone, Width: ui.Px(80), Height: 6, Label: r.k})
						c.Text("v", r.v, kit.Mono(10.5, typeset.SemiBold, th.C(theme.Fg)))
					})
				}
			})
			if load := x.loadOf(); len(load) > 1 {
				widget.Chart(c, "spark", []widget.ChartSeries{{Name: "CPU", Values: load, Tone: loadTone(th, m.CPU)}},
					widget.ChartOpts{Kind: widget.ChartSpark, Height: 28, Max: 1, Label: "CPU, the last 15 minutes"})
			}
		})
	})
}

// status is the status line's: the machine's CPU, which opens the Performance page.
func (x *Machine) status(c *ui.Ctx) {
	x.shown.Keep()
	th := c.Theme()
	m := x.h.State().Machine
	if m.Sampled.IsZero() {
		return
	}
	o := widget.IndicatorOpts{Label: "CPU", Value: fmt.Sprintf("%d%%", kit.Pct(m.CPU)), Help: kit.HUDHelp + "#indicators"}
	if m.CPU >= .8 {
		o.Dot, o.Tone = th.C(theme.Warn), th.C(theme.Warn)
	}
	if widget.Clickable(c, "cpu", ui.Style{Radius: 4, Label: fmt.Sprintf("CPU %d%%, Performance", kit.Pct(m.CPU)),
		Help: PerfHelp}, func(c *ui.Ctx) { widget.Indicator(c, "i", o) }).Clicked {
		if err := x.h.Run(context.Background(), "perf.open", nil); err != nil {
			x.h.Log().Error("machine", "command", "perf.open", "err", err)
		}
	}
}
