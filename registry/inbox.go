package registry

import (
	"time"

	"github.com/liamg/oriel/ui"
)

// InboxSource gives the inbox its cards (ui.md 12): what waits on you, stacked over the window while it does, and
// the Inbox panel's history. The asks of every session are one source; agentbus, GitHub or a plugin add their own.
type InboxSource struct {
	Name string // unique among sources: "asks"
	// Cards are the source's cards now. It runs on the app's loop as the inbox is drawn, so it answers from what it has.
	Cards  func() []InboxCard
	Source string
}

func (InboxSource) ContributionKind() string   { return "inbox.source" }
func (s InboxSource) ContributionName() string { return s.Name }

// InboxCard is one card: a question, a review request, a held connection, a security-key touch, or a line of
// history (Done) such as "CI passed".
type InboxCard struct {
	ID      string // unique in its source
	From    string // its source's name; InboxCards sets it
	Type    string // what kind of card: "network", "review-request"; its help anchor
	Group   string // the Inbox panel's filter it's under: GroupAgents, GroupBus, GroupGitHub, GroupSandbox
	Mine    bool   // for you by name: the ME filter
	Session string // the session it's for, whose tab counts it; "" for none
	Icon    string // a glyph for its inbox row
	Tone    string // a theme token: its edge, and its tab's badge
	Title   string
	Sub     string // its inbox row's second line: "sandbox · flaky-e2e · go mod download"
	Created time.Time
	Expires time.Time // a countdown ring to then; zero for none
	// Interrupt stacks it at the window's bottom right while it waits; Banner draws it on its own at the top, over
	// everything (a security-key touch). Otherwise it's only in the Inbox panel.
	Interrupt bool
	Banner    bool
	Done      bool // history: nothing waits on it; it never stacks or counts

	// The card's face: Kicker, Title, Detail, Code, Body, Actions and Note in a widget.ActionCard. Draw, when set,
	// draws the whole card instead.
	Kicker, Detail, Code, Note string
	Body                       func(c *ui.Ctx)
	Actions                    []InboxAction
	Draw                       func(c *ui.Ctx, key string, p CardPlace)
	// Dismissed hears that it was dismissed: taken off the stack, it waits on in the Inbox panel. A source that drops
	// it then (a note that needs no answer) does it here.
	Dismissed func()
}

// InboxAction is one of a card's buttons: Run, or else Command with Args.
type InboxAction struct {
	Label   string
	Key     string // typed while the card has focus; an upper-case letter is ⇧ and the letter
	Primary bool
	Command string
	Args    Args
	Run     func()
}

// CardPlace is where a card is drawn: stacked over the window (it says whose it is), or in the Inbox panel.
type CardPlace struct {
	Stacked bool
	// Dismiss takes the card off the stack: its ✕ and Escape. Nil where it can't be dismissed.
	Dismiss func()
}

// The Inbox panel's filters.
const (
	GroupAgents  = "agents"
	GroupBus     = "bus"
	GroupGitHub  = "github"
	GroupSandbox = "sandbox"
)

// Key is the card's key in the inbox: its source's name and its ID.
func (c InboxCard) Key() string { return c.From + "/" + c.ID }

// InboxCards are every source's cards, by source name then in the order each gives them. A source that panics gives
// none.
func InboxCards(r Registry) []InboxCard {
	var out []InboxCard
	for _, c := range r.Contributions("inbox.source") {
		s := c.(InboxSource)
		if s.Cards == nil {
			continue
		}
		var cs []InboxCard
		Call(func() error { cs = s.Cards(); return nil })
		for _, card := range cs {
			card.From = s.Name
			out = append(out, card)
		}
	}
	return out
}
