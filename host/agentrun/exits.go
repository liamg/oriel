package agentrun

import (
	"fmt"
	"time"

	"github.com/liamg/oriel/core"
)

// An agent that exits on its own is started again, unless it has exited exitLimit times within exitWindow.
const (
	exitLimit  = 3
	exitWindow = 2 * time.Minute
)

// SetRestartOnExit says whether an agent that exits on its own is started again (sessions.restart_on_exit).
func (x *Runner) SetRestartOnExit(on bool) {
	x.mu.Lock()
	x.keepExited = !on
	x.mu.Unlock()
}

func (x *Runner) restartsOnExit() bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return !x.keepExited
}

// exited starts the agent again after its process ended by itself, if its kind restarts it, unless it keeps exiting.
func (h *session) exited() {
	se := h.snapshot()
	if !h.r.restartsOnExit() || !h.r.deps.Kinds.Spec(se).Can(core.RestartsAgent) || se.Archived || se.Agent.Provider == "" || h.r.env.IsClosed() {
		return
	}
	now := h.r.env.Now()
	h.mu.Lock()
	kept := h.exits[:0]
	for _, t := range h.exits {
		if now.Sub(t) < exitWindow {
			kept = append(kept, t)
		}
	}
	h.exits = append(kept, now)
	looping := len(h.exits) >= exitLimit
	if looping {
		h.exits = nil
	}
	h.mu.Unlock()
	if !looping {
		h.restart("it exited")
		return
	}
	why := fmt.Sprintf("The agent exited %d times in %s, so it wasn't started again.", exitLimit, exitWindow)
	h.setAgent(func(a *core.AgentState) { a.Error = why })
	_ = h.r.st.Emit(core.Notified{Notice: core.Notice{ID: "agent.exits", Severity: core.SevError, Source: "host", Session: h.id,
		Title: "Agent keeps exiting", Text: why, Time: now.UTC(),
		Actions: []core.Action{{Title: "Resume", Command: "session.open", Args: map[string]any{"id": string(h.id)}}}}})
}

// forgetExits clears the count of recent exits: a turn worked, or the user resumed the agent.
func (h *session) forgetExits() {
	h.mu.Lock()
	h.exits = nil
	h.mu.Unlock()
}
