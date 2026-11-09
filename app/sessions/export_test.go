package sessions

import (
	"time"

	"github.com/liamg/oriel/host/stats"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/ui/widget"
)

// What the package's outside tests read and set of its plugins.

func (x *Jobs) Open() bool      { return x.open }
func (x *Jobs) Close()          { x.open = false }
func (x *Jobs) Busy() bool      { return x.busy() }
func (x *Jobs) Submit(run bool) { x.submit(run) }

// SetClock has the page read the time from now, in loc.
func (x *Jobs) SetClock(now func() time.Time, loc *time.Location) { x.now, x.loc = now, loc }

// Form is the New job dialog's job, while it's open.
func (x *Jobs) Form() (prompt, agent string, open bool) {
	if x.form == nil {
		return "", "", false
	}
	return x.form.prompt, x.form.agent, true
}

// FormAgents is how far the dialog's read of the agents is.
func (x *Jobs) FormAgents() widget.LoadState {
	if x.form == nil {
		return widget.LoadState{}
	}
	return x.form.agents.State()
}

// RunCosts are the first job's runs' costs, each with its split by repo and the repos it worked in.
func (x *Jobs) RunCosts() (costs []float64, shares [][]stats.Share, repos [][]string, ok []bool) {
	for _, rr := range x.rows()[0].runs {
		c, sh, k := x.cost(rr)
		costs, shares, repos, ok = append(costs, c), append(shares, sh), append(repos, rr.run.Repos), append(ok, k)
	}
	return costs, shares, repos, ok
}

func (x *Launcher) Open() bool               { return x.open }
func (x *Launcher) Prompt() string           { return x.prompt }
func (x *Launcher) Provider() string         { return x.provider }
func (x *Launcher) Sandbox() bool            { return x.sandbox }
func (x *Launcher) OpenWith(a registry.Args) { x.openWith(a) }

// Cloning shows the Add repo dialog cloning, git saying progress.
func (x *Repos) Cloning(progress string) { x.dialog.busy, x.dialog.progress = true, progress }
