package app

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/render"
	"github.com/liamg/oriel/render/typeset"
	"github.com/liamg/oriel/theme"
	"github.com/liamg/oriel/ui"
	"github.com/liamg/oriel/ui/widget"
	"github.com/liamg/oriel/worker"
	"github.com/liamg/oriel/worker/stats"
)

// The Jobs page (44's panel 1): the jobs on the left, the selected one's prompt, schedule and runs on the right, over
// the main area beside the sidebar.

// runsShown is how many runs the runs card lists before "N more".
const runsShown = 6

func (x *jobsPlugin) page(c *ui.Ctx) {
	if !x.open || c.Theme().IsProbe() {
		return
	}
	rows := x.rows()
	widget.Page(c, "jobs", &x.open, widget.PageOpts{Over: mainArea(), KeyMode: JobsKeyMode, Label: "Jobs page", Help: "agent/jobs#page"}, func(c *ui.Ctx) {
		th := c.Theme()
		x.header(c, rows)
		c.Row("body", ui.Style{W: ui.Flex(1), H: ui.Flex(1)}, func(c *ui.Ctx) {
			x.list(c, rows)
			c.Box("divider", ui.Style{W: ui.Px(1), H: ui.Flex(1), Fill: th.C(theme.Line)}, nil)
			c.Column("detail", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true, Pad: ui.Insets{L: 18, R: 18, T: 14, B: 18}, Gap: 12,
				Fill: th.C(theme.BgBase)}, func(c *ui.Ctx) {
				r, _, ok := x.selected(rows)
				if !ok {
					x.empty(c)
					return
				}
				x.detail(c, r)
			})
		})
	})
}

func (x *jobsPlugin) header(c *ui.Ctx, rows []jobRow) {
	th := c.Theme()
	c.Row("top", ui.Style{W: ui.Flex(1), H: ui.Px(42), Pad: ui.PadXY(18, 0), Gap: 12, Cross: ui.Center, Fill: th.C(tStatsHead)}, func(c *ui.Ctx) {
		c.Text("h", "JOBS", ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 14, Weight: typeset.SemiBold, Tracking: .04},
			Color: th.C(theme.Fg)})
		runs, running := 0, 0
		for _, r := range rows {
			runs += len(r.runs)
			for _, rr := range r.runs {
				if rr.run.Status == worker.RunRunning {
					running++
				}
			}
		}
		meta := []string{counted(len(rows), "job"), fmt.Sprintf("%d running", running), counted(runs, "run")}
		if ws := x.workerNames(); ws != "" {
			meta = append(meta, "on "+ws)
		} else {
			meta = append(meta, "no worker connected")
		}
		c.Text("m", strings.Join(meta, " · "), monoFont(11, th.C(theme.FgFaint)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		if x.err != "" {
			c.Text("err", truncate(x.err, 90), monoFont(11, th.C(theme.Error)))
		}
		if widget.Button(c, "new", "+ New job", widget.ButtonOpts{Key: "n", Help: "agent/jobs#new", Disabled: !x.h.Available("jobs.new", nil)}) {
			x.run("jobs.new", nil)
		}
		if widget.Button(c, "stats", "Stats", widget.ButtonOpts{Key: "kind=job", Help: "agent/jobs#page"}) {
			x.run("stats.open", registry.Args{"kind": string(core.KindJob)})
		}
		if widget.Button(c, "close", "Close", widget.ButtonOpts{Key: "esc", Help: "agent/jobs#page"}) {
			x.open = false
		}
	})
	c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
}

func (x *jobsPlugin) run(id string, a registry.Args) {
	if err := x.h.Run(context.Background(), id, a); err != nil {
		x.err = err.Error()
	}
}

// workerNames are the connected workers' names.
func (x *jobsPlugin) workerNames() string {
	var out []string
	for _, w := range x.h.State().Workers {
		if w.Connected {
			out = append(out, w.Title())
		}
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

func (x *jobsPlugin) workerName(id core.WorkerID) string {
	for _, w := range x.h.State().Workers {
		if w.ID == id {
			return w.Title()
		}
	}
	return string(id)
}

// list is the jobs, filtered, a row each.
func (x *jobsPlugin) list(c *ui.Ctx, rows []jobRow) {
	th := c.Theme()
	q := strings.ToLower(strings.TrimSpace(x.filter))
	shown := slices.DeleteFunc(slices.Clone(rows), func(r jobRow) bool {
		return q != "" && !strings.Contains(strings.ToLower(r.job.Title+" "+r.job.Name+" "+r.job.Prompt), q)
	})
	c.Column("list", ui.Style{W: ui.Px(330), H: ui.Flex(1), Fill: th.C(theme.BgSurface)}, func(c *ui.Ctx) {
		c.Row("filter", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 12, T: 10, B: 6}}, func(c *ui.Ctx) {
			if r := widget.Input(c, "q", &x.filter, widget.InputOpts{Label: "Filter jobs", Placeholder: "⌕ filter jobs", Help: "agent/jobs#list"}); r.Cancelled {
				x.filter = ""
			}
		})
		c.Column("rows", ui.Style{W: ui.Flex(1), H: ui.Flex(1), ScrollY: true}, func(c *ui.Ctx) {
			if len(shown) == 0 {
				c.Column("none", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 12)}, func(c *ui.Ctx) {
					msg := "No jobs yet. A job is a prompt you run more than once: n writes one."
					if q != "" {
						msg = "No job matches."
					}
					widget.Wrap(c, "t", msg, 300, uiFont(12, th.C(theme.FgFaint)))
				})
				return
			}
			sel := 0
			for i, r := range shown {
				if jobKey(r.job) == x.selKey {
					sel = i
				}
			}
			res := widget.Rows(c, "jobs", len(shown), &sel, widget.RowsOpts{Label: "Jobs", KeyMode: JobsKeyMode, Help: "agent/jobs#list",
				RowLabel: func(i int) string { return shown[i].job.Title }}, func(c *ui.Ctx, i int, _ bool) {
				x.jobRowView(c, shown[i], jobKey(shown[i].job) == x.selKey)
			})
			if res.Changed || res.Activated >= 0 || x.selKey == "" {
				if k := jobKey(shown[sel].job); k != x.selKey {
					x.selKey, x.runSel, x.confirm = k, 0, ""
				}
			}
		})
		c.Box("rule", ui.Style{W: ui.Flex(1), H: ui.Px(1), Fill: th.C(theme.Line)}, nil)
		c.Column("foot", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(14, 10), Gap: 3}, func(c *ui.Ctx) {
			where := "~/.oriel/jobs"
			if ws := x.workerNames(); ws != "" {
				where += " on " + ws
			}
			note(c, "a", where+" · one file per job")
			note(c, "b", "runs are workspaces; deleting a job keeps them")
		})
	})
}

func (x *jobsPlugin) jobRowView(c *ui.Ctx, r jobRow, on bool) {
	th := c.Theme()
	fill, edge := render.Color{}, render.Color{}
	if on {
		fill, edge = th.C(tStatsRowOn), th.C(theme.Accent)
	}
	c.Row("row", ui.Style{W: ui.Flex(1), Fill: fill}, func(c *ui.Ctx) {
		c.Box("edge", ui.Style{W: ui.Px(2), H: ui.Flex(1), Fill: edge}, nil)
		c.Column("body", ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: 12, R: 14, T: 9, B: 9}, Gap: 5, Clip: true}, func(c *ui.Ctx) {
			c.Row("l1", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
				c.Text("t", truncate(r.job.Title, 40), uiFont(13, th.C(theme.Fg)))
				c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
				n := "no runs"
				if len(r.runs) > 0 {
					n = counted(len(r.runs), "run")
				}
				c.Text("n", n, monoFont(10, th.C(theme.FgGhost)))
			})
			c.Row("l2", ui.Style{W: ui.Flex(1), Gap: 7, Cross: ui.Center}, func(c *ui.Ctx) {
				last, ok := r.last()
				switch {
				case !ok:
					c.Text("w", "never run · saved "+r.job.CreatedAt.In(x.loc).Format("Jan 2"), monoFont(10, th.C(theme.FgFaint)))
				default:
					widget.Chip(c, "st", last.run.Status, th.C(runTone(last.run.Status)), "agent/jobs#runs")
					when := x.when(last.se.Created)
					if last.run.Status == worker.RunRunning {
						when = "started " + ago(x.now(), last.se.Created) + " ago"
					}
					c.Text("w", when, monoFont(10, th.C(theme.FgFaint)))
				}
				pinChip(c, "pin", r.job.Sandbox)
				if r.job.Schedule.Every != "" {
					c.Text("sch", r.job.Schedule.String(), monoFont(10, th.C(theme.FgFaint)))
				}
			})
		})
	})
}

// pinChip is where a job runs: sandbox, host, or the worker's default.
func pinChip(c *ui.Ctx, key any, sandbox string) {
	th := c.Theme()
	label, tone := cmpOr(sandbox, "default"), th.C(theme.FgFaint)
	switch sandbox {
	case "sandbox":
		tone = th.C(theme.StatusQuiet)
	case "host":
		tone = th.C(theme.Warn)
	}
	c.Row(key, ui.Style{Pad: ui.PadXY(5, 0), H: ui.Px(15), Cross: ui.Center, Radius: 3, Stroke: theme.WithAlpha(tone, .45), Border: 1,
		Role: ui.RoleText, Label: label}, func(c *ui.Ctx) {
		c.Text("t", label, monoFont(9.5, tone))
	})
}

// when is a run's start as the rows show it: "today 07:14", "yesterday 16:40", "Oct 11 · 3d ago".
func (x *jobsPlugin) when(t time.Time) string {
	now := x.now().In(x.loc)
	t = t.In(x.loc)
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, x.loc) }
	switch d := day(now).Sub(day(t)); {
	case d <= 0:
		return "today " + t.Format("15:04")
	case d <= 24*time.Hour:
		return "yesterday " + t.Format("15:04")
	case d < 7*24*time.Hour:
		return t.Format("Jan 2") + " · " + ago(now, t) + " ago"
	}
	return t.Format("Jan 2")
}

// started is a run's start in the runs table: "today 12:13", "Oct 7 · 07:00".
func (x *jobsPlugin) started(t time.Time) string {
	now := x.now().In(x.loc)
	t = t.In(x.loc)
	if t.Year() == now.Year() && t.YearDay() == now.YearDay() {
		return "today " + t.Format("15:04")
	}
	return t.Format("Jan 2 · 15:04")
}

func (x *jobsPlugin) empty(c *ui.Ctx) {
	th := c.Theme()
	c.Column("empty", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(4, 40), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", "Save a prompt you'd run more than once, then press Run.", uiFont(13, th.C(theme.FgMuted)))
		note(c, "n", "Each run is a session of kind job in a fresh workspace, given the prompt as JOB.md.")
	})
}

// took is how long a run ran: so far while it runs.
func (x *jobsPlugin) took(r runRow) time.Duration {
	if r.run.Status == worker.RunRunning {
		return x.now().Sub(r.se.Created)
	}
	return r.run.Took()
}

func (x *jobsPlugin) detail(c *ui.Ctx, r jobRow) {
	th := c.Theme()
	j := r.job
	key := jobKey(j)
	c.Row("title", ui.Style{W: ui.Flex(1), Gap: 10, Cross: ui.Center}, func(c *ui.Ctx) {
		c.Text("t", j.Title, ui.TextStyle{Font: typeset.FontSpec{Family: typeset.Display, Size: 17, Weight: typeset.SemiBold}, Color: th.C(theme.Fg)})
		c.Text("slug", j.Name, monoFont(11, th.C(theme.FgGhost)))
		c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
		args := registry.Args{"name": j.Name, "worker": string(j.Worker)}
		if widget.Button(c, "edit", "Edit", widget.ButtonOpts{Key: "e", Help: "agent/jobs#job"}) {
			x.run("jobs.edit", args)
		}
		if widget.Button(c, "dup", "Duplicate", widget.ButtonOpts{Help: "agent/jobs#job"}) {
			x.run("jobs.duplicate", args)
		}
		label := "Delete"
		if x.confirm == key {
			label = "Sure?"
		}
		if widget.Button(c, "del", label, widget.ButtonOpts{Ghost: true, Help: "agent/jobs#job"}) {
			if x.confirm == key {
				x.confirm = ""
				x.do("jobs.delete", args)
			} else {
				x.confirm = key
			}
		}
		if widget.Button(c, "run", "▶ Run", widget.ButtonOpts{Primary: true, Key: "⌘↵", Help: "agent/jobs#job"}) {
			x.do("jobs.run", args)
		}
	})
	c.Row("meta", ui.Style{W: ui.Flex(1), Gap: 8, Cross: ui.Center}, func(c *ui.Ctx) {
		pinChip(c, "pin", j.Sandbox)
		faint := th.C(theme.FgFaint)
		if j.Sandbox != "" {
			c.Text("pinned", "pinned", monoFont(11, th.C(theme.FgGhost)))
		}
		dot := func(k string) { c.Text(k, "·", monoFont(11, th.C(theme.FgGhost))) }
		dot("d1")
		agent := strings.TrimSpace(strings.ToLower(providerName(j.Provider)) + " " + j.Model + " " + j.Effort)
		if j.Provider == "" {
			agent = "the worker's agent"
		}
		c.Text("agent", agent, monoFont(11, th.C(theme.Fg)))
		dot("d2")
		c.Text("worker", x.workerName(j.Worker), monoFont(11, faint))
		if len(r.runs) == 0 {
			return
		}
		dot("d3")
		count := map[string]int{}
		var total float64
		var spent time.Duration
		ended := 0
		for _, rr := range r.runs {
			count[rr.run.Status]++
			cost, _, _ := x.cost(rr)
			total += cost
			if t := rr.run.Took(); t > 0 && rr.run.Status == worker.RunDone {
				spent += t
				ended++
			}
		}
		var parts []string
		for _, s := range []string{worker.RunDone, worker.RunBlocked, worker.RunStopped, worker.RunRunning} {
			if n := count[s]; n > 0 {
				parts = append(parts, fmt.Sprintf("%d %s", n, s))
			}
		}
		c.Text("runs", counted(len(r.runs), "run")+": "+strings.Join(parts, ", "), monoFont(11, faint))
		dot("d4")
		c.Text("total", money(total)+" total", monoFont(11, th.C(theme.Fg)))
		if ended > 0 {
			dot("d5")
			c.Text("avg", "avg "+hm(spent/time.Duration(ended)), monoFont(11, faint))
		}
	})
	x.prompt(c, j)
	x.schedule(c, j)
	x.runs(c, r)
}

// repoWord finds the registered repos' names a prompt mentions.
var repoWord = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9._-]*`)

// named are the worker's repos a prompt names, in the order it names them.
func (x *jobsPlugin) named(j worker.Job) []string {
	_, repos := x.load()
	known := map[string]bool{}
	for _, r := range repos[j.Worker] {
		known[string(r.ID)] = true
	}
	var out []string
	for _, w := range repoWord.FindAllString(j.Prompt, -1) {
		w = strings.TrimRight(w, ".-_")
		if known[w] && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// prompt is the job's prompt, the repos it names picked out in their colours.
func (x *jobsPlugin) prompt(c *ui.Ctx, j worker.Job) {
	th := c.Theme()
	st := x.h.State()
	names := x.named(j)
	tones := map[string]render.Color{}
	for i, n := range names {
		tones[n] = repoTone(th, st, n, i)
	}
	statsCard(c, "prompt", ui.Flex(1), "PROMPT", "JOB.md for each run", func(c *ui.Ctx) {
		if len(names) == 0 {
			note(c, "none", "names no repo · the agent adds what it needs")
			return
		}
		note(c, "names", "names")
		for i, n := range names {
			widget.Rich(c, i, []widget.Span{{Text: n, Code: true, Tone: tones[n]}}, 200, monoFont(11, th.C(theme.Fg)), widget.RichOpts{})
		}
		note(c, "adds", "· the agent adds them")
	}, func(c *ui.Ctx) {
		width := float32(600)
		if r, ok := c.Rect(c.ID()); ok {
			width = r.Dx() - 24
		}
		base := uiFont(13, th.C(theme.FgMuted))
		c.Column("text", ui.Style{W: ui.Flex(1), Gap: 4, Pad: ui.PadXY(2, 4)}, func(c *ui.Ctx) {
			for i, line := range strings.Split(j.Prompt, "\n") {
				if strings.TrimSpace(line) == "" {
					c.Box(i, ui.Style{H: ui.Px(4)}, nil)
					continue
				}
				widget.Rich(c, i, promptSpans(line, tones), width, base, widget.RichOpts{Code: monoFont(12, th.C(theme.Fg))})
			}
		})
	})
}

// promptSpans are a line of a prompt as spans: `code`, and the repos it names in their tones.
func promptSpans(line string, tones map[string]render.Color) []widget.Span {
	var out []widget.Span
	plain := func(s string) {
		last := 0
		for _, m := range repoWord.FindAllStringIndex(s, -1) {
			w := strings.TrimRight(s[m[0]:m[1]], ".-_")
			tone, ok := tones[w]
			if !ok {
				continue
			}
			if m[0] > last {
				out = append(out, widget.Span{Text: s[last:m[0]]})
			}
			out = append(out, widget.Span{Text: w, Code: true, Tone: tone})
			last = m[0] + len(w)
		}
		if last < len(s) {
			out = append(out, widget.Span{Text: s[last:]})
		}
	}
	for i, part := range strings.Split(line, "`") {
		if i%2 == 1 {
			out = append(out, widget.Span{Text: part, Code: true})
			continue
		}
		plain(part)
	}
	return out
}

// schedule is the job's schedule as a strip: manual, every weekday, weekly, or custom (the dialog's).
func (x *jobsPlugin) schedule(c *ui.Ctx, j worker.Job) {
	th := c.Theme()
	s := j.Schedule
	at := cmpOr(s.At, "07:00")
	day := time.Monday
	if s.Every == worker.EveryWeek && len(s.Days) == 1 {
		day = s.Days[0]
	}
	labels := []string{"MANUAL", "EVERY WEEKDAY " + at, "WEEKLY " + strings.ToUpper(dayName(day)) + " " + at, "CUSTOM…"}
	cur := map[string]int{"": 0, worker.EveryWeekday: 1, worker.EveryWeek: 2, worker.EveryDays: 3}[s.Every]
	if s.Every == worker.EveryDays {
		labels[3] = strings.ToUpper(s.String())
	}
	c.Row("schedule", ui.Style{W: ui.Flex(1), Gap: 12, Cross: ui.Center}, func(c *ui.Ctx) {
		caption(c, "cap", "Schedule")
		pick := cur
		if widget.Segmented(c, "every", labels, &pick, widget.SegmentedOpts{Label: "Schedule", Help: "agent/jobs#schedule"}) {
			args := registry.Args{"name": j.Name, "worker": string(j.Worker)}
			switch pick {
			case 0:
				args["schedule"] = "manual"
			case 1:
				args["schedule"] = "weekdays " + at
			case 2:
				args["schedule"] = "weekly " + dayName(day) + " " + at
			case 3:
				x.run("jobs.edit", args)
				if f := x.form; f != nil && f.every != 3 {
					f.every = 3
					if len(f.custom) == 0 {
						f.custom = []string{dayName(day)}
					}
				}
				return
			}
			x.do("jobs.save", args)
		}
		c.Text("n", "runs on "+x.workerName(j.Worker)+" when it's awake · skips while the last run is going", monoFont(10, th.C(theme.FgGhost)))
	})
}

// cost is a run's estimated cost and its split by repo: from its stats once they're in, its agent's until then.
func (x *jobsPlugin) cost(r runRow) (float64, []stats.Share, bool) {
	if x.sh.stats != nil {
		if d, ok := x.sh.stats.detail(r.se.ID); ok && d.Totals.Cost > 0 {
			return d.Totals.Cost, d.Repos, true
		}
	}
	return r.se.Agent.Stats.Cost, nil, r.se.Agent.Stats.Cost > 0
}

var runCols = []statsCol{{"STATUS", 96}, {"STARTED", 120}, {"REPOS", 200}, {"SUMMARY", 0}, {"COST", 78}, {"TOOK", 48}, {"", 160}}

func (x *jobsPlugin) runs(c *ui.Ctx, r jobRow) {
	th := c.Theme()
	want := runFilters[x.runFilter]
	all := append(slices.Clone(r.runs), skipRows(r.job)...)
	slices.SortStableFunc(all, func(a, b runRow) int { return b.start().Compare(a.start()) })
	shown := slices.DeleteFunc(all, func(rr runRow) bool { return want != "" && rr.run.Status != want })
	labels := []string{"ALL", "RUNNING", "DONE", "BLOCKED", "STOPPED"}
	statsCard(c, "runs", ui.Flex(1), "RUNS", fmt.Sprintf("%d · newest first · each a session of kind job", len(r.runs)), func(c *ui.Ctx) {
		widget.Segmented(c, "filter", labels, &x.runFilter, widget.SegmentedOpts{Label: "Show runs", Help: "agent/jobs#runs"})
	}, func(c *ui.Ctx) {
		tableHead(c, runCols)
		if len(shown) == 0 {
			c.Row("none", ui.Style{W: ui.Flex(1), H: ui.Px(30), Pad: ui.PadXY(6, 0), Cross: ui.Center}, func(c *ui.Ctx) {
				msg := "No runs yet: ▶ Run starts one."
				if want != "" {
					msg = "No " + want + " runs."
				}
				c.Text("t", msg, monoFont(11, th.C(theme.FgFaint)))
			})
			return
		}
		rest := shown[min(len(shown), runsShown):]
		shown = shown[:min(len(shown), runsShown)]
		n := len(shown)
		if len(rest) > 0 {
			n++
		}
		res := widget.Rows(c, "list", n, &x.runSel, widget.RowsOpts{Label: "Runs", KeyMode: JobsKeyMode, Help: "agent/jobs#runs",
			ClickSelects: true,
			Static:       func(i int) bool { return i >= len(shown) },
			Disabled:     func(i int) bool { return i < len(shown) && shown[i].skipped() },
			RowLabel: func(i int) string {
				rr := shown[i]
				return fmt.Sprintf("%s run, %s, %s", rr.run.Status, x.started(rr.start()), x.summary(rr))
			}}, func(c *ui.Ctx, i int, _ bool) {
			if i >= len(shown) {
				x.more(c, rest)
				return
			}
			x.runRowView(c, i, shown[i])
		})
		if res.Activated >= 0 && res.Activated < len(shown) {
			x.openRun(shown[res.Activated].se.ID)
		}
	})
}

// stoppedBy is who a run's stop names.
var stoppedBy = map[string]string{core.StoppedByYou: " by you", core.StoppedByAgent: " by the agent", core.StoppedByWorker: " as the worker shut down"}

// summary is what a run's row says it did.
func (x *jobsPlugin) summary(r runRow) string {
	if r.run.Status == worker.RunStopped {
		s := "Stopped" + stoppedBy[r.run.StoppedBy]
		if t := r.run.Took(); t > 0 {
			s += " after " + hm(t)
		}
		return s
	}
	return r.run.Summary
}

// openRun brings a run's session up as 44's panel 3 draws it: its console the main tab, the sidebar narrow.
func (x *jobsPlugin) openRun(id core.SessionID) {
	x.open = false
	x.do("session.open", registry.Args{"id": string(id), "then": []any{
		registry.Args{"command": "view.open", "args": registry.Args{"id": AgentConsoleView, "place": "tab", "title": "Agent"}},
		registry.Args{"command": "sidebar.form", "args": registry.Args{"form": "narrow"}},
	}})
}

func (x *jobsPlugin) runRowView(c *ui.Ctx, i int, r runRow) {
	th := c.Theme()
	st := x.h.State()
	c.Row("row", ui.Style{W: ui.Flex(1), Pad: ui.PadXY(0, 5), Cross: ui.Center}, func(c *ui.Ctx) {
		cell(c, 0, runCols[0].w, true, func(c *ui.Ctx) {
			widget.Chip(c, "st", r.run.Status, th.C(runTone(r.run.Status)), "agent/jobs#runs")
		})
		cell(c, 1, runCols[1].w, true, func(c *ui.Ctx) { c.Text("t", x.started(r.start()), monoFont(11, th.C(theme.Fg))) })
		if r.skipped() {
			c.Column(3, ui.Style{W: ui.Flex(1), Pad: ui.Insets{L: runCols[2].w + 6, R: 6}}, func(c *ui.Ctx) {
				c.Text("t", r.run.Summary, uiFont(12, th.C(theme.FgFaint)))
			})
			return
		}
		cell(c, 2, runCols[2].w, true, func(c *ui.Ctx) {
			for j, p := range r.run.Repos {
				c.Row(j, ui.Style{Gap: 4, Cross: ui.Center}, func(c *ui.Ctx) {
					swatch(c, "sw", repoTone(th, st, p, j))
					c.Text("t", p, monoFont(11, th.C(theme.FgMuted)))
				})
			}
		})
		c.Column(3, ui.Style{W: ui.Flex(1), Pad: ui.PadXY(6, 0), Clip: true}, func(c *ui.Ctx) {
			width := float32(300)
			if rc, ok := c.Rect(c.ID()); ok {
				width = rc.Dx() - 12
			}
			tone := th.C(theme.FgMuted)
			if r.run.Status == worker.RunBlocked {
				tone = th.C(theme.Fg)
			}
			widget.Wrap(c, "t", truncate(x.summary(r), 150), width, uiFont(12, tone))
		})
		cell(c, 4, runCols[4].w, false, func(c *ui.Ctx) { x.costCell(c, r) })
		took := ""
		if d := x.took(r); d > 0 {
			took = hm(d)
		}
		textCell(c, 5, runCols[5].w, took, th.C(theme.FgMuted))
		cell(c, 6, runCols[6].w, false, func(c *ui.Ctx) {
			live := r.se.Agent.Running
			label := "Resume"
			if live {
				label = "Open"
			}
			if widget.Button(c, "open", label, widget.ButtonOpts{Primary: r.run.Status == worker.RunRunning, Help: "agent/jobs#runs"}) {
				x.openRun(r.se.ID)
			}
			if r.run.Status == worker.RunRunning {
				return
			}
			label = "Delete"
			if x.confirm == string(r.se.ID) {
				label = "Sure?"
			}
			if widget.Button(c, "del", label, widget.ButtonOpts{Ghost: true, Help: "agent/jobs#runs"}) {
				if x.confirm == string(r.se.ID) {
					x.confirm = ""
					x.do("session.delete", registry.Args{"id": string(r.se.ID)})
				} else {
					x.confirm = string(r.se.ID)
				}
			}
		})
	})
}

// costCell is a run's cost over a strip of its split by repo, a link to its stats with the split in a card under the
// pointer.
func (x *jobsPlugin) costCell(c *ui.Ctx, r runRow) {
	th := c.Theme()
	st := x.h.State()
	cost, shares, ok := x.cost(r)
	if !ok {
		c.Text("none", "—", monoFont(11, th.C(theme.FgGhost)))
		return
	}
	var strip []widget.ChartSeries
	for i, s := range shares {
		strip = append(strip, widget.ChartSeries{Name: s.Repo, Values: []float64{s.Cost}, Tone: repoTone(th, st, s.Repo, i)})
	}
	anchor := c.Next("cost")
	if widget.Clickable(c, "cost", ui.Style{Dir: ui.Column, Gap: 3, W: ui.Px(60), Label: "Cost " + money(cost), NoHover: true, Help: "agent/jobs#cost"}, func(c *ui.Ctx) {
		c.Row("v", ui.Style{W: ui.Flex(1), Main: ui.End}, func(c *ui.Ctx) {
			c.Text("t", money(cost), mono(11.5, typeset.Medium, th.C(theme.Fg)))
		})
		if len(strip) > 0 {
			widget.Chart(c, "split", strip, widget.ChartOpts{Kind: widget.ChartStrip, Height: 3, Highlight: -1, Label: "Cost by repo", Help: "agent/jobs#cost"})
		}
	}).Clicked {
		x.run("stats.session", registry.Args{"id": string(r.se.ID)})
	}
	widget.HoverCard(c, "split", anchor, widget.HoverCardOpts{Label: "Cost by repo", Help: "agent/jobs#cost", Width: 190}, func(c *ui.Ctx) {
		c.Text("t", r.se.Created.In(x.loc).Format("Jan 2")+" run · "+money(cost)+" est.", mono(11, typeset.SemiBold, th.C(theme.Fg)))
		for i, s := range shares {
			kv(c, i, repoTone(th, st, s.Repo, i), s.Repo, "", money(s.Cost))
		}
		c.Text("more", "session stats ›", monoFont(10.5, th.C(theme.Accent)))
	})
}

// more is the runs past those listed: how many, since when, how they went and what they cost.
func (x *jobsPlugin) more(c *ui.Ctx, rest []runRow) {
	th := c.Theme()
	count := map[string]int{}
	var total float64
	for _, r := range rest {
		count[r.run.Status]++
		cost, _, _ := x.cost(r)
		total += cost
	}
	var parts []string
	for _, s := range runFilters[1:] {
		if n := count[s]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, s))
		}
	}
	faint := th.C(theme.FgFaint)
	c.Row("more", ui.Style{W: ui.Flex(1), H: ui.Px(30), Cross: ui.Center}, func(c *ui.Ctx) {
		textCellLeft(c, 0, runCols[0].w, fmt.Sprintf("%d more", len(rest)), faint)
		textCellLeft(c, 1, runCols[1].w, "since "+rest[len(rest)-1].start().In(x.loc).Format("Jan 2"), th.C(theme.FgGhost))
		textCellLeft(c, 2, runCols[2].w, "", faint)
		cell(c, 3, 0, true, func(c *ui.Ctx) { c.Text("t", strings.Join(parts, ", "), uiFont(12, th.C(theme.FgGhost))) })
		textCell(c, 4, runCols[4].w, money(total), th.C(theme.FgMuted))
		textCell(c, 5, runCols[5].w, "", faint)
		textCell(c, 6, runCols[6].w, "", faint)
	})
}

func textCellLeft(c *ui.Ctx, key any, w float32, s string, tone render.Color) {
	cell(c, key, w, true, func(c *ui.Ctx) { c.Text("t", s, monoFont(11, tone)) })
}

// consoleSide is a run's job in its agent console (44's panel 3): the job, how this run is going and the last one
// went, what it has cost in each repo so far, and what happens when it ends.
func (x *jobsPlugin) consoleSide(c *ui.Ctx, id core.SessionID) {
	th := c.Theme()
	view, ok := x.h.State().Session(id)
	if !ok || view.Snapshot().Job == "" {
		return
	}
	se := view.Snapshot()
	var row jobRow
	for _, r := range x.rows() {
		if r.job.Name == se.Job && slices.ContainsFunc(r.runs, func(rr runRow) bool { return rr.se.ID == id }) {
			row = r
		}
	}
	cur := runRow{se: se, run: worker.RunOf(se)}
	section(c, "job", "Job", "", func(c *ui.Ctx) {
		c.Row("title", ui.Style{W: ui.Flex(1), Gap: 8}, func(c *ui.Ctx) {
			widget.Wrap(c, "t", cmpOr(row.job.Title, se.Job), 240, uiFont(13, th.C(theme.Fg)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			if widget.Link(c, "jobs", "Jobs ›", "agent/jobs#page") {
				x.run("jobs.open", registry.Args{"job": se.Job})
			}
		})
		c.Row("status", ui.Style{W: ui.Flex(1), Cross: ui.Center}, func(c *ui.Ctx) {
			c.Text("n", "status", monoFont(11.5, th.C(theme.FgMuted)))
			c.Box("gap", ui.Style{W: ui.Flex(1)}, nil)
			widget.Chip(c, "v", cur.run.Status, th.C(runTone(cur.run.Status)), "agent/jobs#runs")
		})
		started := se.Created.In(x.loc).Format("15:04")
		if d := x.took(cur); d > 0 {
			started += " · " + hm(d)
		}
		stat(c, "started", "started", started, th.C(theme.Fg))
		for _, rr := range row.runs {
			if rr.se.Created.Before(se.Created) {
				last := rr.run.Status
				if d := rr.run.Took(); d > 0 {
					last += " · " + hm(d)
				}
				stat(c, "last", "last run", last, th.C(runTone(rr.run.Status)))
				break
			}
		}
	})
	cost, shares, _ := x.cost(cur)
	section(c, "repos", "Repos touched", "cost so far", func(c *ui.Ctx) {
		st := x.h.State()
		var strip []widget.ChartSeries
		for i, s := range shares {
			strip = append(strip, widget.ChartSeries{Name: s.Repo, Values: []float64{s.Cost}, Tone: repoTone(th, st, s.Repo, i)})
		}
		if len(strip) > 0 {
			widget.Chart(c, "split", strip, widget.ChartOpts{Kind: widget.ChartStrip, Highlight: -1, Label: "Cost by repo", Help: "agent/jobs#cost"})
		}
		for i, s := range shares {
			kv(c, i, repoTone(th, st, s.Repo, i), s.Repo, "", money(s.Cost))
		}
		for i, n := range x.named(row.job) {
			if !slices.ContainsFunc(shares, func(s stats.Share) bool { return s.Repo == n }) && !slices.Contains(cur.run.Repos, n) {
				kv(c, fmt.Sprint("todo", i), theme.WithAlpha(repoTone(th, st, n, i), .4), n, "", "not yet")
			}
		}
		stat(c, "total", "total est.", money(cost), th.C(theme.Fg))
	})
	section(c, "ends", "When it ends", "", func(c *ui.Ctx) {
		widget.Wrap(c, "t", "The run row gets its status and the agent's summary; blocked also lands in Requests.", 290, uiFont(12, th.C(theme.FgFaint)))
	})
}
