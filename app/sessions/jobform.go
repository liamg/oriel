package sessions

import (
	"cmp"
	"context"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/app/kit/agentkit"

	"github.com/liamg/oriel/registry/services"

	"github.com/liamg/oriel/app/kit"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
)

// The New job dialog: a title, the prompt, where it runs, the agent and the schedule. New job, Edit,
// Duplicate and the launcher's Save as job all open it; Save and Save and run go through jobs.save.

var (
	runsIn   = []string{"Sandbox", "Host", "Default"}
	pinOf    = []string{"sandbox", "host", ""}
	everyOf  = []string{"", host.EveryWeekday, host.EveryWeek, host.EveryDays}
	everyFor = []string{"MANUAL", "WEEKDAYS", "WEEKLY", "CUSTOM"}
)

type jobForm struct {
	name   string // the job edited; "" for a new one
	title  string
	prompt string
	runsIn int
	agent  string // the provider
	model  string
	effort string
	every  int
	day    int // a weekly schedule's, into days
	custom []string
	at     string
	focus  bool
	busy   bool
	err    string
	agents *plugin.Load[[]host.ProviderInfo]
	picked bool // the agent was given, or picked once the providers arrived
}

func (f *jobForm) job() host.Job {
	s := host.Schedule{Every: everyOf[f.every], At: strings.TrimSpace(f.at)}
	switch s.Every {
	case "":
		s.At = ""
	case host.EveryWeek:
		s.Days = []time.Weekday{days[f.day]}
	case host.EveryDays:
		for _, d := range days {
			if slices.Contains(f.custom, dayName(d)) {
				s.Days = append(s.Days, d)
			}
		}
	}
	return host.Job{Name: f.name, Title: f.title, Prompt: f.prompt, Sandbox: pinOf[f.runsIn], Provider: f.agent,
		Model: f.model, Effort: f.effort, Schedule: s}
}

// formFor is a dialog holding j.
func formFor(j host.Job) *jobForm {
	f := &jobForm{name: j.Name, title: j.Title, prompt: j.Prompt, runsIn: max(slices.Index(pinOf, j.Sandbox), 0),
		agent: j.Provider, model: j.Model, effort: j.Effort, every: max(slices.Index(everyOf, j.Schedule.Every), 0), at: cmp.Or(j.Schedule.At, "07:00"),
		focus: true}
	for _, d := range j.Schedule.Days {
		f.custom = append(f.custom, dayName(d))
		if i := slices.Index(days, d); i >= 0 {
			f.day = i
		}
	}
	return f
}

// newForm opens the dialog on a new job, with what the args give filled in.
func (x *Jobs) newForm(a registry.Args) {
	j := host.Job{Title: a.String("title"), Prompt: a.String("prompt"), Provider: a.String("provider"), Model: a.String("model"),
		Effort: a.String("effort")}
	switch v := a["sandbox"].(type) {
	case bool:
		j.Sandbox = map[bool]string{true: "sandbox", false: "host"}[v]
	case string:
		j.Sandbox = v
	}
	if v, ok := a["schedule"]; ok {
		j.Schedule, _ = scheduleFrom(v)
	}
	x.show(formFor(j))
}

// editForm opens the dialog on a job: to change it, or as a new copy of it.
func (x *Jobs) editForm(a registry.Args, copy bool) error {
	j, err := x.target(a)
	if err != nil {
		return err
	}
	if copy {
		j.Name, j.Title = "", j.Title+" (copy)"
	}
	x.show(formFor(j))
	return nil
}

func (x *Jobs) show(f *jobForm) {
	x.closeForm()
	f.picked = f.agent != ""
	f.agents = plugin.NewLoad(x.h, func(ctx context.Context, _ func(string, float64)) ([]host.ProviderInfo, error) {
		c, err := kit.SessionCalls(x.h)
		if err != nil {
			return nil, err
		}
		return c.ProviderInfo(ctx), nil
	}, plugin.LoadOpts{Timeout: 20 * time.Second})
	f.agents.Start()
	x.form = f
}

func (x *Jobs) closeForm() {
	if x.form != nil && x.form.agents != nil {
		x.form.agents.Reset()
	}
	x.form = nil
}

// settle picks the default agent once the providers arrive, unless the job names one.
func (f *jobForm) settle(st core.State) {
	ps, ok := f.agents.Value()
	if !ok || f.picked {
		return
	}
	f.picked = true
	if id, model := kit.DefaultAgent(st, ps); id != "" {
		f.agent, f.model = id, cmp.Or(f.model, model)
	}
}

// submit saves the dialog's job, running it too when run; the dialog closes and the page shows it once it's saved.
func (x *Jobs) submit(run bool) {
	f := x.form
	if f == nil || f.busy {
		return
	}
	if reason := widget.Waiting(f.agents.State()).Reason(); reason != "" {
		f.err = "To save: " + reason + "."
		return
	}
	j := f.job()
	if strings.TrimSpace(j.Prompt) == "" {
		f.err = "A job is a prompt: say what it should do."
		return
	}
	if err := j.Schedule.Check(); err != nil {
		f.err = err.Error()
		return
	}
	a := registry.Args{"title": j.Title, "prompt": j.Prompt, "sandbox": j.Sandbox, "provider": j.Provider, "model": j.Model, "effort": j.Effort,
		"schedule": j.Schedule, "run": run}
	if j.Name != "" {
		a["name"] = j.Name
	}
	f.busy, f.err = true, ""
	plugin.Go(x.h, "jobs: save", func() {
		saved, err := x.save(context.Background(), a)
		x.h.Do(func() {
			if x.form != f {
				return
			}
			f.busy = false
			if err != nil {
				f.err = err.Error()
				return
			}
			x.closeForm()
			x.open, x.selKey = true, jobKey(saved)
		})
		x.h.Redraw()
	})
}

func (x *Jobs) dialog(c *ui.Ctx) {
	f := x.form
	if f == nil {
		return
	}
	open := true
	title := "New job"
	if f.name != "" {
		title = "Edit " + f.name
	}
	widget.Modal(c, "jobform", &open, widget.ModalOpts{Title: title, Width: 620, Help: "agent/jobs#new"}, func(c *ui.Ctx) { x.formBody(c, f) })
	if !open {
		x.closeForm()
	}
}

// field is a text field of the dialog's: Tab moves on, Escape closes, ⌘↵ saves and runs.
func (x *Jobs) field(c *ui.Ctx, key string, text *string, label, placeholder string, focus bool, o widget.InputOpts) {
	if focus {
		c.RequestFocus(c.Next(key))
	}
	o.Label, o.Placeholder, o.Help, o.TabMoves, o.KeyMode = label, placeholder, "agent/jobs#new", true, JobFormKeyMode
	if widget.Input(c, key, text, o).Cancelled {
		x.closeForm()
	}
}

func (x *Jobs) formBody(c *ui.Ctx, f *jobForm) {
	th := c.Theme()
	focus := f.focus
	f.focus = false
	f.settle(x.h.State())
	c.Column("title", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Title", "from the prompt's first line when empty")
		x.field(c, "title", &f.title, "Title", "a name for the list", focus, widget.InputOpts{})
	})
	c.Column("prompt", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Prompt", "markdown · name the repos it needs")
		if widget.Prose(c, "prompt", &f.prompt, widget.ProseOpts{Label: "Prompt", Placeholder: "What should each run do? Name the repos; the agent adds them.",
			Help: "agent/jobs#new", TabMoves: true, KeyMode: JobFormKeyMode, Lines: 8, MinLines: 5}).Cancelled {
			x.closeForm()
		}
		kit.Note(c, "n", "no repo or branch here: the agent adds what the prompt names, fresh each run")
	})
	c.Column("runs", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Runs in", "")
		widget.Segmented(c, "in", runsIn, &f.runsIn, widget.SegmentedOpts{Label: "Runs in", Help: "agent/jobs#new"})
		kit.Note(c, "n", []string{"pinned for every run · container, gateway for git & gh", "pinned for every run · on this machine itself",
			"sandbox.default"}[f.runsIn])
	})
	c.Column("agent", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Agent", "provider · model · effort")
		agents, _ := f.agents.Value()
		widget.Async(c, "agents", f.agents.State(), widget.AsyncOpts{Policy: widget.Gate, Label: "Looking for agents", Retry: f.agents.Reload,
			Empty: "No agents installed. Each run uses the first agent installed on this machine when it runs.", IsEmpty: len(agents) == 0,
			Width: 560, Help: "agent/jobs#new"}, func(c *ui.Ctx) {
			c.Column("choices", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) { x.agentChoices(c, f, agents) })
		})
	})
	c.Column("schedule", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		kit.Heading(c, "h", "Schedule", "runs while Oriel is open · skips while the last run is going")
		c.Row("row", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Segmented(c, "every", everyFor, &f.every, widget.SegmentedOpts{Label: "Schedule", Help: "agent/jobs#schedule"})
			switch everyOf[f.every] {
			case "":
				return
			case host.EveryWeek:
				var names []string
				for _, d := range days {
					names = append(names, dayName(d))
				}
				if v, ok := widget.Select(c, "day", dayName(days[f.day]), names, widget.SelectOpts{Label: "Day", Help: "agent/jobs#schedule", Width: 120}); ok {
					f.day = slices.Index(names, v)
				}
			case host.EveryDays:
				if v, ok := widget.MultiSelect(c, "days", f.custom, func() []string {
					var names []string
					for _, d := range days {
						names = append(names, dayName(d))
					}
					return names
				}, widget.MultiSelectOpts{Label: "Days", Help: "agent/jobs#schedule", Width: 160}); ok {
					f.custom = v
				}
			}
			c.Text("at", "at", kit.MonoFont(11, th.C(theme.FgFaint)))
			_, err := time.Parse("15:04", strings.TrimSpace(f.at))
			x.field(c, "time", &f.at, "At", "07:00", false, widget.InputOpts{Width: ui.Px(80), Invalid: err != nil,
				Accept: func(r rune) bool { return r >= '0' && r <= '9' || r == ':' }})
		})
	})
	widget.Rule(c, "rule")
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if f.err != "" {
			c.Text("err", f.err, kit.MonoFont(11, th.C(theme.Error)))
		} else {
			file := f.name
			if file == "" {
				file = trimSlug(host.Sanitise(cmp.Or(f.title, kit.FirstLine(f.prompt))))
			}
			c.Text("file", "→ "+cmp.Or(file, "job")+".json", kit.MonoFont(10.5, th.C(theme.FgGhost)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "agent/jobs#new"}) {
			x.closeForm()
			return
		}
		wait := widget.Waiting(f.agents.State())
		busyLabel := "Saving"
		if wait.Pending() {
			busyLabel = "Looking for agents"
		}
		if widget.Button(c, "save", "Save", widget.ButtonOpts{Busy: f.busy || wait.Pending(), BusyLabel: busyLabel, Disabled: wait.Failed(), Help: "agent/jobs#new"}) {
			x.submit(false)
		}
		if widget.Button(c, "saverun", "Save and run", widget.ButtonOpts{Primary: true, Key: "⌘↵", Busy: f.busy || wait.Pending(), BusyLabel: busyLabel,
			Disabled: wait.Failed(), Help: "agent/jobs#new"}) {
			x.submit(true)
		}
	})
}

// agentChoices are the agents read, the job's picked, with its model and effort.
func (x *Jobs) agentChoices(c *ui.Ctx, f *jobForm, agents []host.ProviderInfo) {
	items := make([]widget.Choice, len(agents))
	cur := 0
	for i, p := range agents {
		items[i] = widget.Choice{Title: services.ProviderTitle(x.h, p.ID), Desc: providerNote(p, x.h.State().Usage)}
		if p.ID == f.agent {
			cur = i
		}
	}
	if widget.Choices(c, "provider", items, &cur, widget.ChoicesOpts{Label: "Agent", Help: "agent/jobs#new"}) {
		f.agent, f.model, f.effort = agents[cur].ID, kit.DefaultModel(agents[cur]), ""
	}
	if p := agents[cur]; len(p.Models) > 0 {
		if m, e, ok := agentkit.ModelSelects(c, p, cmp.Or(f.model, kit.DefaultModel(p)), f.effort, false, "agent/jobs#new"); ok {
			f.agent, f.model, f.effort = p.ID, m, e
		}
	}
}

// trimSlug is a slug's first six words, as the session host names a new job.
func trimSlug(s string) string {
	parts := strings.Split(s, "-")
	return strings.Join(parts[:min(len(parts), 6)], "-")
}
