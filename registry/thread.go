package registry

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
func ThreadSourceFor(r Registry, origin string) (ThreadSource, bool) {
	if origin == "" {
		return ThreadSource{}, false
	}
	for _, c := range r.Contributions("thread.source") {
		if s := c.(ThreadSource); s.Origin == origin {
			return s, true
		}
	}
	return ThreadSource{}, false
}
