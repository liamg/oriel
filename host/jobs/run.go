//oriel:kind job

package jobs

import (
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
)

// A run's status.
const (
	RunRunning = "running"
	RunDone    = "done"
	RunBlocked = "blocked"
	RunStopped = "stopped"
	RunSkipped = "skipped" // a scheduled run that didn't start: the last was still going (Job.Skips)
)

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

// blockedMark starts a blocked run's answer (the job pack's).
const blockedMark = "**Blocked:**"

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
			r.Summary = strings.Join(strings.Fields(strings.TrimPrefix(strings.TrimSpace(c.Body), blockedMark)), " ")
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
