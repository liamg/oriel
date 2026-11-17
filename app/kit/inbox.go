package kit

import "github.com/liamg/oriel/registry"

// InboxService is every InboxSource's cards, as the inbox keeps them.
const InboxService = "inbox"

// Inbox is what the cards waiting on you say about each session, for its tab (the session tabs).
type Inbox interface {
	// Count is how many cards wait in session, and the tone of the first: its tab's badge.
	Count(session string) (n int, tone string)
	// Calling says a Prompt card waits in session while another session is in front: the newest's key (a new one
	// calls again) and tone. Its tab calls for you.
	Calling(session string) (key, tone string, ok bool)
}

func InboxOf(r registry.Registry) (Inbox, bool) { return registry.ServiceOf[Inbox](r, InboxService) }
