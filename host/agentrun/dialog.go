package agentrun

import (
	"context"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
)

// dialogAsk is the card for a dialog the agent's terminal shows.
type dialogAsk struct{ stop context.CancelFunc }

// terminalDialog puts a card on the session for a dialog its agent's terminal shows, until the dialog is answered.
func (h *session) terminalDialog(ev agent.Dialog) {
	h.mu.Lock()
	old := h.dialog
	if ev.Shown && old != nil {
		h.mu.Unlock()
		return
	}
	h.dialog = nil
	var mine *dialogAsk
	var ctx context.Context
	if ev.Shown {
		mine = &dialogAsk{}
		ctx, mine.stop = context.WithCancel(h.ctx)
		h.dialog = mine
	}
	h.mu.Unlock()
	if old != nil {
		old.stop()
	}
	if mine == nil {
		return
	}
	h.r.env.Go("a terminal dialog", func() {
		h.askUser(ctx, core.Ask{Kind: core.AskTerminal, Summary: ev.Title})
		h.mu.Lock()
		if h.dialog == mine {
			h.dialog = nil
		}
		h.mu.Unlock()
		mine.stop()
	})
}
