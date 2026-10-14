package app

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
)

// The New job dialog (44's panel 2): a title, the prompt, where it runs, the agent and the schedule. New job, Edit,
// Duplicate and the launcher's Save as job all open it; Save and Save and run go through jobs.save.

var (
	runsIn   = []string{"Sandbox", "Host", "Default"}
	pinOf    = []string{"sandbox", "host", ""}
	everyOf  = []string{"", worker.EveryWeekday, worker.EveryWeek, worker.EveryDays}
	everyFor = []string{"MANUAL", "WEEKDAYS", "WEEKLY", "CUSTOM"}
)

type jobForm struct {
	name    string // the job edited; "" for a new one
	worker  core.WorkerID
	title   string
	prompt  string
	runsIn  int
	agent   string // the provider
	model   string
	effort  string
	every   int
	day     int // a weekly schedule's, into days
	custom  []string
	at      string
	focus   bool
	busy    bool
	err     string
	gen     int
	agents  []worker.ProviderInfo
	loading bool
}

func (f *jobForm) job() worker.Job {
	s := worker.Schedule{Every: everyOf[f.every], At: strings.TrimSpace(f.at)}
	switch s.Every {
	case "":
		s.At = ""
	case worker.EveryWeek:
		s.Days = []time.Weekday{days[f.day]}
	case worker.EveryDays:
		for _, d := range days {
			if slices.Contains(f.custom, dayName(d)) {
				s.Days = append(s.Days, d)
			}
		}
	}
	return worker.Job{Name: f.name, Worker: f.worker, Title: f.title, Prompt: f.prompt, Sandbox: pinOf[f.runsIn], Provider: f.agent,
		Model: f.model, Effort: f.effort, Schedule: s}
}

// formFor is a dialog holding j.
func formFor(j worker.Job) *jobForm {
	f := &jobForm{name: j.Name, worker: j.Worker, title: j.Title, prompt: j.Prompt, runsIn: max(slices.Index(pinOf, j.Sandbox), 0),
		agent: j.Provider, model: j.Model, effort: j.Effort, every: max(slices.Index(everyOf, j.Schedule.Every), 0), at: cmpOr(j.Schedule.At, "07:00"),
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
func (x *jobsExt) newForm(a registry.Args) {
	j := worker.Job{Title: a.String("title"), Prompt: a.String("prompt"), Provider: a.String("provider"), Model: a.String("model"),
		Effort: a.String("effort"), Worker: core.WorkerID(a.String("worker"))}
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
func (x *jobsExt) editForm(a registry.Args, copy bool) error {
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

func (x *jobsExt) show(f *jobForm) {
	if x.form != nil {
		f.gen = x.form.gen + 1
	}
	x.form = f
	x.loadAgents()
}

// loadAgents asks the form's worker for its providers.
func (x *jobsExt) loadAgents() {
	f := x.form
	c, err := x.client(f.worker)
	if err != nil {
		f.err = err.Error()
		return
	}
	f.loading = true
	gen := f.gen
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ps, err := c.Providers(ctx)
		x.h.Do(func() {
			if x.form == nil || x.form.gen != gen {
				return
			}
			x.form.loading, x.form.agents = false, ps
			if err != nil {
				x.form.err = err.Error()
			}
			if x.form.agent == "" {
				for _, p := range ps {
					if p.ID == defaultProvider(ps) {
						x.form.agent, x.form.model = p.ID, cmpOr(x.form.model, defaultModel(p))
					}
				}
			}
		})
		x.h.Redraw()
	}()
}

// submit saves the dialog's job, running it too when run; the dialog closes and the page shows it once it's saved.
func (x *jobsExt) submit(run bool) {
	f := x.form
	if f == nil || f.busy {
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
		"worker": string(j.Worker), "schedule": j.Schedule, "run": run}
	if j.Name != "" {
		a["name"] = j.Name
	}
	f.busy, f.err = true, ""
	go func() {
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
			x.form, x.open, x.selKey = nil, true, jobKey(saved)
		})
		x.h.Redraw()
	}()
}

func (x *jobsExt) dialog(c *ui.Ctx) {
	f := x.form
	if f == nil {
		return
	}
	open := true
	title := "New job"
	if f.name != "" {
		title = "Edit " + f.name
	}
	widget.Modal(c, "jobform", &open, title, widget.ModalOpts{Width: 620, Help: "agent/jobs#new"}, func(c *ui.Ctx) { x.formBody(c, f) })
	if !open {
		x.form = nil
	}
}

// field is a text field of the dialog's: Tab moves on, Escape closes, ⌘↵ saves and runs.
func (x *jobsExt) field(c *ui.Ctx, key string, text *string, label, placeholder string, focus bool, o widget.InputOpts) {
	if focus {
		c.Focus(c.Next(key))
	}
	o.Label, o.Placeholder, o.Help, o.TabMoves, o.KeyMode = label, placeholder, "agent/jobs#new", true, JobFormKeyMode
	if widget.Input(c, key, text, o).Cancelled {
		x.form = nil
	}
}

func (x *jobsExt) formBody(c *ui.Ctx, f *jobForm) {
	th := c.Theme()
	focus := f.focus
	f.focus = false
	c.Column("title", ui.Style{W: ui.Flex(1)}, func(c *ui.Ctx) {
		heading(c, "h", "Title", "from the prompt's first line when empty")
		x.field(c, "title", &f.title, "Title", "a name for the list", focus, widget.InputOpts{})
	})
	c.Column("prompt", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		heading(c, "h", "Prompt", "markdown · name the repos it needs")
		x.field(c, "prompt", &f.prompt, "Prompt", "What should each run do? Name the repos; the agent adds them.", false, widget.InputOpts{})
		note(c, "n", "no repo or branch here: the agent adds what the prompt names, fresh each run")
	})
	c.Row("where", ui.Style{W: ui.Flex(1), Gap: 16}, func(c *ui.Ctx) {
		c.Column("runs", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			heading(c, "h", "Runs in", "")
			widget.Segmented(c, "in", runsIn, &f.runsIn, widget.SegmentedOpts{Label: "Runs in", Help: "agent/jobs#new"})
			note(c, "n", []string{"pinned for every run · container, gateway for git & gh", "pinned for every run · on the worker itself",
				"the worker's sandbox.default"}[f.runsIn])
		})
		c.Column("worker", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
			heading(c, "h", "Worker", "")
			x.workerSelect(c, f)
		})
	})
	c.Column("agent", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		heading(c, "h", "Agent", "provider · model · effort")
		if len(f.agents) == 0 {
			msg := "the worker's first agent"
			if f.loading {
				msg = "Asking the worker…"
			}
			c.Text("wait", msg, uiFont(12, th.C(theme.FgFaint)))
			return
		}
		items := make([]widget.Choice, len(f.agents))
		cur := 0
		for i, p := range f.agents {
			items[i] = widget.Choice{Title: providerName(p.ID), Desc: providerNote(p, x.h.State().Usage)}
			if p.ID == f.agent {
				cur = i
			}
		}
		if widget.Choices(c, "provider", items, &cur, widget.ChoicesOpts{Label: "Agent", Help: "agent/jobs#new"}) {
			f.agent, f.model, f.effort = f.agents[cur].ID, defaultModel(f.agents[cur]), ""
		}
		if p := f.agents[cur]; len(p.Models) > 0 {
			if m, e, ok := modelSelects(c, p, cmpOr(f.model, defaultModel(p)), f.effort, "agent/jobs#new"); ok {
				f.agent, f.model, f.effort = p.ID, m, e
			}
		}
	})
	c.Column("schedule", ui.Style{W: ui.Flex(1), Gap: 6}, func(c *ui.Ctx) {
		heading(c, "h", "Schedule", "runs on the worker when it's awake · skips while the last run is going")
		c.Row("row", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
			widget.Segmented(c, "every", everyFor, &f.every, widget.SegmentedOpts{Label: "Schedule", Help: "agent/jobs#schedule"})
			switch everyOf[f.every] {
			case "":
				return
			case worker.EveryWeek:
				var names []string
				for _, d := range days {
					names = append(names, dayName(d))
				}
				if v, ok := widget.Select(c, "day", dayName(days[f.day]), names, widget.SelectOpts{Label: "Day", Help: "agent/jobs#schedule", Width: 120}); ok {
					f.day = slices.Index(names, v)
				}
			case worker.EveryDays:
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
			c.Text("at", "at", monoFont(11, th.C(theme.FgFaint)))
			_, err := time.Parse("15:04", strings.TrimSpace(f.at))
			x.field(c, "time", &f.at, "At", "07:00", false, widget.InputOpts{Width: ui.Px(80), Invalid: err != nil,
				Accept: func(r rune) bool { return r >= '0' && r <= '9' || r == ':' }})
		})
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
	c.Row("foot", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		if f.err != "" {
			c.Text("err", truncate(f.err, 70), monoFont(11, th.C(theme.Error)))
		} else {
			file := f.name
			if file == "" {
				file = trimSlug(worker.Sanitise(cmpOr(f.title, firstLine(f.prompt))))
			}
			c.Text("file", "→ "+cmpOr(file, "job")+".json", monoFont(10.5, th.C(theme.FgGhost)))
		}
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if widget.Button(c, "cancel", "Cancel", widget.ButtonOpts{Help: "agent/jobs#new"}) {
			x.form = nil
			return
		}
		if widget.Button(c, "save", "Save", widget.ButtonOpts{Disabled: f.busy, Help: "agent/jobs#new"}) {
			x.submit(false)
		}
		if widget.Button(c, "saverun", "Save and run", widget.ButtonOpts{Primary: true, Key: "⌘↵", Disabled: f.busy, Help: "agent/jobs#new"}) {
			x.submit(true)
		}
	})
}

// trimSlug is a slug's first six words, as the worker names a new job.
func trimSlug(s string) string {
	parts := strings.Split(s, "-")
	return strings.Join(parts[:min(len(parts), 6)], "-")
}

// workerSelect picks the worker the job lives on.
func (x *jobsExt) workerSelect(c *ui.Ctx, f *jobForm) {
	var labels []string
	var ids []core.WorkerID
	current := ""
	for _, w := range x.h.State().Workers {
		if !w.Connected {
			continue
		}
		labels, ids = append(labels, cmpOr(w.Name, string(w.ID))), append(ids, w.ID)
		if w.ID == f.worker || f.worker == "" && w.ID == core.LocalWorker {
			current = labels[len(labels)-1]
		}
	}
	if current == "" && len(labels) > 0 {
		current = labels[0]
	}
	disabled := f.name != ""
	if v, ok := widget.Select(c, "worker", current, labels, widget.SelectOpts{Label: "Worker", Help: "agent/jobs#new", Width: 260, Disabled: disabled}); ok {
		if i := slices.Index(labels, v); i >= 0 && ids[i] != f.worker {
			f.worker, f.agents, f.agent, f.model, f.effort = ids[i], nil, "", "", ""
			f.gen++
			x.loadAgents()
		}
	}
}
