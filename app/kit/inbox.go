package kit

import (
	"time"

	"github.com/liamg/oriel/registry"
	"github.com/liamg/oriel/registry/uireg"
	"github.com/liamg/oriel/ui"
)

// InboxService is every InboxSource's cards, as the inbox keeps them.
const InboxService = "inbox"

// WaitingCard is a card waiting on you, and when it came.
type WaitingCard struct {
	Card uireg.InboxCard
	At   time.Time
}

// Inbox is the cards waiting on you, for a surface that shows them itself (the session tabs).
type Inbox interface {
	// Waiting are the cards waiting on you, in the order they came.
	Waiting() []WaitingCard
	// Count is how many cards wait in session, and the tone of the first: its tab's badge.
	Count(session string) (n int, tone string)
	// ShowInline says which cards (by Key) a surface shows itself, so the stack over the window leaves them out; nil
	// for none.
	ShowInline(keys map[string]bool)
	// Card draws a card as the Inbox panel does, its actions answering it.
	Card(c *ui.Ctx, card uireg.InboxCard)
}

func InboxOf(r registry.Registry) (Inbox, bool) { return registry.ServiceOf[Inbox](r, InboxService) }
