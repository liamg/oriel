package host

import (
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

func terminalAsk(se core.SessionState) (core.Ask, bool) {
	for _, a := range se.Asks {
		if a.Kind == core.AskTerminal {
			return a, true
		}
	}
	return core.Ask{}, false
}

// A dialog on the agent's terminal asks its session to go there, once while it's shown, and the ask goes once it's
// answered.
func TestATerminalDialogAsksForYou(t *testing.T) {
	r := newRig(t)
	id, a := r.workSession("Fix it")
	a.WaitSent(t)
	a.Emit(agent.Dialog{Title: "Allow external CLAUDE.md file imports?", Shown: true})
	a.Emit(agent.Dialog{Title: "Allow external CLAUDE.md file imports?", Shown: true})
	// The status follows the ask in an event of its own.
	se := r.eventually(id, "the terminal ask", func(se core.SessionState) bool {
		_, ok := terminalAsk(se)
		return ok && se.Status == core.StatusNeedsYou
	})
	if ask, _ := terminalAsk(se); ask.Summary != "Allow external CLAUDE.md file imports?" || se.Status != core.StatusNeedsYou || len(se.Asks) != 1 {
		t.Errorf("asked %+v with the session %s", se.Asks, se.Status)
	}
	a.Emit(agent.Dialog{})
	r.eventually(id, "the terminal ask gone", func(se core.SessionState) bool { _, ok := terminalAsk(se); return !ok })
}
