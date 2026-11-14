//oriel:kind job

package hostapi

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// Job is a saved prompt with its pins. It names no repo or branch: the prompt names the repos,
// and the agent running it adds them. Each is one file under the host's jobs/.
type Job struct {
	Name     string `json:"name"` // its slug: the file, and what its runs are named after
	Title    string `json:"title"`
	Prompt   string `json:"prompt"`
	Sandbox  string `json:"sandbox,omitempty"` // "sandbox", "host", or "" for sandbox.default
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Effort   string `json:"effort,omitempty"`
	// Schedule runs it by itself; the zero one is manual.
	Schedule  Schedule  `json:"schedule,omitzero"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// Due is the occurrence of its schedule it last ran, or skipped.
	Due time.Time `json:"due,omitzero"`
	// Skips are the occurrences skipped while its last run was still going, the latest maxSkips, oldest first.
	Skips []time.Time `json:"skips,omitempty"`
}

// Run is a job session as its row shows it.
type Run struct {
	Status    string
	Summary   string // the agent's last word on the request: its answer, or how it's getting on
	Repos     []string
	Started   time.Time
	Ended     time.Time // the agent's last word, or when it was stopped; zero while it runs
	StoppedBy string    // a stopped run's: core.StoppedByYou…, "" when not known
}

// Took is how long it ran: to its last answer, or zero while it runs.
func (r Run) Took() time.Duration {
	if r.Ended.IsZero() || r.Started.IsZero() {
		return 0
	}
	return r.Ended.Sub(r.Started)
}

// Schedule is when a job runs by itself, in the host's local time. A run is skipped while the last is still going.
type Schedule struct {
	Every string         `json:"every,omitempty"` // "" for manual
	Days  []time.Weekday `json:"days,omitempty"`
	At    string         `json:"at,omitempty"` // "07:00"
}

func (s Schedule) clock() (int, int, error) {
	t, err := time.Parse("15:04", s.At)
	if err != nil {
		return 0, 0, fmt.Errorf("a schedule's time is HH:MM, not %q", s.At)
	}
	return t.Hour(), t.Minute(), nil
}

// Check says what's wrong with the schedule, or nil.
func (s Schedule) Check() error {
	switch s.Every {
	case "":
		return nil
	case EveryWeekday:
	case EveryWeek:
		if len(s.Days) != 1 {
			return errors.New("a weekly schedule names one day")
		}
	case EveryDays:
		if len(s.Days) == 0 {
			return errors.New("a custom schedule names its days")
		}
	default:
		return fmt.Errorf("a schedule is weekdays, weekly or days, not %q", s.Every)
	}
	_, _, err := s.clock()
	return err
}

func (s Schedule) on(d time.Weekday) bool {
	if s.Every == EveryWeekday {
		return d >= time.Monday && d <= time.Friday
	}
	return slices.Contains(s.Days, d)
}

// Next is when the schedule next fires after t, in t's zone; zero for a manual one.
func (s Schedule) Next(t time.Time) time.Time {
	if s.Every == "" || s.Check() != nil {
		return time.Time{}
	}
	h, m, _ := s.clock()
	for i := range 8 {
		d := t.AddDate(0, 0, i)
		at := time.Date(d.Year(), d.Month(), d.Day(), h, m, 0, 0, t.Location())
		if at.After(t) && s.on(at.Weekday()) {
			return at
		}
	}
	return time.Time{}
}

// Last is when the schedule last fired after from and by now, in from's zone; zero if it didn't.
func (s Schedule) Last(from, now time.Time) time.Time {
	var last time.Time
	for at := s.Next(from); !at.IsZero() && !at.After(now); at = s.Next(at) {
		last = at
	}
	return last
}

// String is how the schedule reads: "manual", "every weekday 07:00", "weekly Mon 07:00", "Mon, Thu 07:00".
func (s Schedule) String() string {
	var days []string
	for _, d := range s.Days {
		days = append(days, d.String()[:3])
	}
	switch s.Every {
	case EveryWeekday:
		return "every weekday " + s.At
	case EveryWeek:
		return "weekly " + strings.Join(days, ", ") + " " + s.At
	case EveryDays:
		return strings.Join(days, ", ") + " " + s.At
	}
	return "manual"
}

// How often a schedule runs a job.
const (
	EveryWeekday = "weekdays" // Monday to Friday
	EveryWeek    = "weekly"   // on Days[0]
	EveryDays    = "days"     // on each of Days: custom
)

// A run's status.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunBlocked = "blocked"
	RunStopped = "stopped"
	RunSkipped = "skipped" // a scheduled run that didn't start: the last was still going (Job.Skips)
)

// RunBlockedMark starts a blocked run's answer (the job pack's).
const RunBlockedMark = "**Blocked:**"

// JobOf is the saved job a session is a run of; "" for any other session.
func JobOf(se core.SessionState) string {
	d, _ := core.DataOf[core.JobData](se)
	return d.Job
}

// RunOf reads a job session's run from its request thread: done once it's resolved, blocked while the agent's answer
// waits on you, running while the agent has it, and stopped when it was stopped before either.
func RunOf(se core.SessionState) Run {
	r := Run{Started: se.Created}
	for _, p := range se.Projects {
		r.Repos = append(r.Repos, p.Name)
	}
	i := slices.IndexFunc(se.Threads, func(t core.Thread) bool { return !t.Btw })
	var t core.Thread
	if i >= 0 {
		t = se.Threads[i]
	}
	for _, c := range slices.Backward(t.Comments) {
		if c.Author == core.AuthorAgent {
			r.Summary = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(c.Body), RunBlockedMark)), " ")
			r.Ended = c.Time
			break
		}
	}
	asking := slices.ContainsFunc(se.Asks, func(a core.Ask) bool { return a.Kind == core.AskQuestion })
	switch {
	case i >= 0 && !t.Open():
		r.Status = RunDone
	case asking || i >= 0 && t.AgentAnswered():
		r.Status = RunBlocked
		if asking && !t.AgentAnswered() {
			r.Ended, r.Summary = time.Time{}, askText(se.Asks)
		}
	case se.Agent.Live() && !se.Archived:
		r.Status, r.Ended = RunRunning, time.Time{}
	default:
		r.Status, r.StoppedBy = RunStopped, se.Agent.StoppedBy
		if !se.Agent.StoppedAt.IsZero() {
			r.Ended = se.Agent.StoppedAt
		}
	}
	return r
}

// askText is the agent's open question.
func askText(asks []core.Ask) string {
	for _, a := range asks {
		if a.Kind == core.AskQuestion {
			if len(a.Questions) > 0 {
				return a.Questions[0].Question
			}
			return a.Summary
		}
	}
	return ""
}
