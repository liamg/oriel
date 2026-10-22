package agentrun

import (
	"fmt"
	"strings"

	"github.com/liamg/oriel/core"
)

// carryOnMax is how many times in a row the agent is told to carry on before the user next writes to it.
const carryOnMax = 3

// resumedLead opens the carry-on notice of an agent started again after Oriel was.
const resumedLead = "Oriel restarted and started you again in this conversation; a turn that was running was cut off. "

// busy says something of the agent's is still running to wake it, or it waits on the user's answer.
func busy(se core.SessionState) bool {
	if len(se.Agent.Background) > 0 || len(se.Agent.Queue) > 0 || len(se.Asks) > 0 {
		return true
	}
	for _, s := range se.Subagents {
		if s.Running {
			return true
		}
	}
	return false
}

// owed is what the agent owes in se: the threads waiting on it, and the tasks not done whose thread isn't with the
// user.
func owed(se core.SessionState) (threads []core.Thread, tasks []core.Task) {
	for _, t := range se.Threads {
		if t.WaitsOnAgent() {
			threads = append(threads, t)
		}
	}
	for _, k := range se.Tasks {
		if k.Status == core.TaskCompleted {
			continue
		}
		if t, ok := se.Thread(k.Thread); ok && !t.WaitsOnAgent() {
			continue
		}
		tasks = append(tasks, k)
	}
	return threads, tasks
}

// carryOn tells the agent to carry on with what it owes with nothing left running, unless it was told already with
// nothing moved since, or carryOnMax times since the user last wrote. lead says why it stopped. It reports whether
// the work isn't quiet: something owed or still running.
func (h *session) carryOn(lead string) bool {
	se := h.snapshot()
	if busy(se) {
		return true
	}
	threads, tasks := owed(se)
	if len(threads)+len(tasks) == 0 {
		return false
	}
	var at []string
	for _, t := range threads {
		at = append(at, fmt.Sprint(t.ID, len(t.Comments)))
	}
	for _, k := range tasks {
		at = append(at, k.ID+string(k.Status))
	}
	key := strings.Join(at, " ")
	h.mu.Lock()
	if key == h.carriedAt || h.carried >= carryOnMax {
		h.mu.Unlock()
		return true
	}
	h.carriedAt = key
	h.carried++
	h.mu.Unlock()
	h.note(lead + carryOnNotice(se, threads, tasks))
	return true
}

// carryOnNotice names what the agent owes and asks it to carry on.
func carryOnNotice(se core.SessionState, threads []core.Thread, tasks []core.Task) string {
	var parts []string
	for _, t := range threads {
		about := t.Title
		if c, ok := firstComment(t); ok && about == "" {
			about = core.Summary(c.Body)
		}
		parts = append(parts, fmt.Sprintf("thread %s (%s: %s)", t.ID, where(se, t), clipLine(about)))
	}
	for _, k := range tasks {
		parts = append(parts, fmt.Sprintf("task %q", k.Subject))
	}
	return "Your turn ended with work still owed and nothing running to wake you: " + strings.Join(parts, "; ") + ". " +
		"Don't describe the next step: carry on and do it now. End your turn only when each thread is resolved, handed " +
		"back to the user with a reply that isn't `working` (a question, or why you disagree), or waiting on background " +
		"work you started."
}

func firstComment(t core.Thread) (core.Comment, bool) {
	if len(t.Comments) == 0 {
		return core.Comment{}, false
	}
	return t.Comments[0], true
}

// clipLine cuts a line to 80 runes.
func clipLine(s string) string {
	if r := []rune(s); len(r) > 80 {
		return string(r[:79]) + "…"
	}
	return s
}
