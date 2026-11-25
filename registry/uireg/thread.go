package uireg

import "github.com/liamg/oriel/registry"

import "github.com/liamg/oriel/ui"

// ThreadSource is where threads of one origin come from (a thread's Origin: "github"), and how their cards show it,
// wherever a thread is drawn: between the editor's or the diff's lines, and in a request. Threads with no source are
// Oriel's own.
type ThreadSource struct {
	Origin string
	Title  string // the card's name in place of "Thread": "GitHub review"
	// Head draws after the title in the card's head: the place, who started it, its state, when it was synced.
	Head func(c *ui.Ctx, session, thread string)
	// Comment draws under one of the thread's comments: a draft's Forward and Edit. nil draws nothing.
	Comment func(c *ui.Ctx, session, thread, comment string, width float32)
	Source  string
}

func (ThreadSource) ContributionKind() string   { return "thread.source" }
func (s ThreadSource) ContributionName() string { return s.Origin }

// ThreadSourceFor is the source of threads of origin, if one is registered.
func ThreadSourceFor(r registry.Contributions, origin string) (ThreadSource, bool) {
	if origin == "" {
		return ThreadSource{}, false
	}
	for _, s := range registry.ContributionsOf[ThreadSource](r) {
		if s.Origin == origin {
			return s, true
		}
	}
	return ThreadSource{}, false
}

// AnchorPlace shows a thread's place in a view of a feature's own in place of the editor: a plan's doc in the plan.
// Going to a thread's anchor (request.anchor) asks each; the first that Shows the path opens it.
type AnchorPlace struct {
	Name  string
	Label string // what going there is called: "Open in plan"
	// Shows says whether it shows path for session; Open opens it there at 1-based line, the thread in view.
	Shows  func(session, path string) bool
	Open   func(ctx registry.Ctx, session, path string, line int) error
	Source string
}

func (AnchorPlace) ContributionKind() string   { return "anchor.place" }
func (p AnchorPlace) ContributionName() string { return p.Name }

// AnchorPlaceFor is the place that shows path for session, if one does.
func AnchorPlaceFor(r registry.Contributions, session, path string) (AnchorPlace, bool) {
	for _, p := range registry.ContributionsOf[AnchorPlace](r) {
		if p.Shows != nil && p.Open != nil && p.Shows(session, path) {
			return p, true
		}
	}
	return AnchorPlace{}, false
}

// ThreadWhere is where a thread's card is drawn.
type ThreadWhere string

const (
	ThreadInCode    ThreadWhere = "code"    // between the editor's or the diff's lines
	ThreadInRequest ThreadWhere = "request" // in Requests
	ThreadInFinding ThreadWhere = "finding" // an AI review finding's card
)

// ThreadFoot draws at the foot of every thread's card, under its comments, between the lines and in Requests, and of
// every AI review finding's card (thread is then the finding's id): what a plugin offers to do with it, as a review's
// Keep and Drop. Draw draws nothing for one it has nothing for.
type ThreadFoot struct {
	Name   string
	Draw   func(c *ui.Ctx, session, thread string, where ThreadWhere, width float32)
	Source string
}

func (ThreadFoot) ContributionKind() string   { return "thread.foot" }
func (f ThreadFoot) ContributionName() string { return f.Name }

// DrawThreadFeet draws every ThreadFoot's for a thread, in the card's own column: a foot keys its nodes by its Name.
func DrawThreadFeet(r registry.Contributions, c *ui.Ctx, session, thread string, where ThreadWhere, width float32) {
	for _, f := range registry.ContributionsOf[ThreadFoot](r) {
		if f.Draw != nil {
			_ = registry.RecoverAsError(func() error { f.Draw(c, session, thread, where, width); return nil })
		}
	}
}
