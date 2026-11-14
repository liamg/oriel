package ship

import (
	"errors"

	"github.com/liamg/oriel/core"
)

// What the package's outside tests read and set of its plugins.

func (x *Ship) ShipOf(id core.SessionID, project string) (Read, bool) { return x.ship(id, project) }
func (x *Ship) Doing(id core.SessionID, project string) string        { return x.doing(id, project) }

// Fired says PrMerged has fired for url.
func (x *Ship) Fired(url string) bool {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.merged[url]
}

// Pending makes id's reading a first read running; fail ends it with err, and restore brings back what it was.
func (x *Ship) Pending(id core.SessionID) (fail func(err string), restore func()) {
	x.mu.Lock()
	defer x.mu.Unlock()
	read := x.reports[id]
	// Fixed until it fails: the reads the session's subscriptions start don't answer for it.
	pending := &shipReport{loading: true, fixed: true, doing: map[string]string{}, failed: map[string]string{}}
	x.reports[id] = pending
	fail = func(err string) {
		x.mu.Lock()
		defer x.mu.Unlock()
		pending.loading, pending.err, pending.fixed = false, errors.New(err), false
	}
	restore = func() {
		x.mu.Lock()
		defer x.mu.Unlock()
		x.reports[id] = read
	}
	return fail, restore
}

// SetLoading says a read of id's projects is running, or not.
func (x *Ship) SetLoading(id core.SessionID, loading bool) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.reports[id].loading = loading
}

// SetDoing shows step running on project.
func (x *Ship) SetDoing(id core.SessionID, project, step string) {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.reports[id].doing[project] = step
}

const DraftSaveAfter = draftSaveAfter

func (x *GitHub) Wait() { x.wait() }

func (x *Verify) Wait()                          { x.wait() }
func (x *Verify) Open() core.SessionID           { return x.open }
func (x *Verify) Answered(id core.SessionID) int { return x.quizzes[id].at }

type Verdict = verdict

const QuizLength = quizLength

var (
	ParseQuestions = parseQuestions
	ParseVerdict   = parseVerdict
)

type Sites = codeSites

func (x *CodeSites) Sites() *Sites { return x.sites }

func (c *codeSites) Config() string { return c.config }

// WholeReads is how many reads of every project of a session have started.
func (x *Ship) WholeReads(id core.SessionID) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	if r := x.reports[id]; r != nil {
		return r.started
	}
	return 0
}
