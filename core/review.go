package core

import (
	"fmt"
	"slices"
	"time"
)

func init() {
	RegisterEvent[HunksReviewed]("HunksReviewed")
	RegisterEvent[LinesSeen]("LinesSeen")
	RegisterEvent[CommitGateOverridden]("CommitGateOverridden")
}

// Review is what of a file's change has been reviewed in a session: its changes marked by ID, a hash of what each
// removes and adds (gitx.Hunk.ID), so a mark stays with its change while the rest of the file is edited; and its
// lines seen in the diff, by LineKey.
type Review struct {
	Project string   `json:"project"`
	File    string   `json:"file"` // relative to the project, slash-separated
	Hunks   []string `json:"hunks"`
	Read    []string `json:"read,omitempty"`    // on screen long enough to read
	Skimmed []string `json:"skimmed,omitempty"` // on screen, but not for long
}

// LineState is how far a line of a change has been reviewed: the review ledger's read, skimmed or unread.
type LineState int

const (
	LineUnread LineState = iota
	LineSkimmed
	LineRead
)

func (s LineState) String() string { return [...]string{"unread", "skimmed", "read"}[s] }

// LineKey names the nth line (0-based) a change adds, so it keeps its name while the file is edited elsewhere.
func LineKey(change string, n int) string { return fmt.Sprintf("%s.%d", change, n) }

// ReviewOf is a project's file's review in the session; empty when nothing of it has been reviewed.
func (s SessionState) ReviewOf(project, file string) Review {
	for _, r := range s.Reviews {
		if r.Project == project && r.File == file {
			return r
		}
	}
	return Review{Project: project, File: file}
}

// State is a line's state: read when its change is marked reviewed or it was read in the diff.
func (r Review) State(change, line string) LineState {
	_, marked := slices.BinarySearch(r.Hunks, change)
	_, read := slices.BinarySearch(r.Read, line)
	if marked || read {
		return LineRead
	}
	if _, ok := slices.BinarySearch(r.Skimmed, line); ok {
		return LineSkimmed
	}
	return LineUnread
}

func (r Review) empty() bool { return len(r.Hunks) == 0 && len(r.Read) == 0 && len(r.Skimmed) == 0 }

// setReview puts a file's review in the session, or takes it out when empty, leaving older copies alone.
func (se *SessionState) setReview(r Review) {
	i := slices.IndexFunc(se.Reviews, func(o Review) bool { return o.Project == r.Project && o.File == r.File })
	out := slices.Clone(se.Reviews)
	switch {
	case i < 0 && !r.empty():
		out = append(out, r)
	case i >= 0 && !r.empty():
		out[i] = r
	case i >= 0:
		out = slices.Delete(out, i, i+1)
	}
	se.Reviews = out
}

// insert adds keys to a sorted set, cloning it first.
func insert(set []string, keys ...string) []string {
	set = slices.Clone(set)
	for _, k := range keys {
		if i, found := slices.BinarySearch(set, k); !found {
			set = slices.Insert(set, i, k)
		}
	}
	return set
}

// ReviewedHunks are the changes of a project's file marked reviewed.
func (s SessionState) ReviewedHunks(project, file string) map[string]bool {
	out := map[string]bool{}
	for _, r := range s.Reviews {
		if r.Project == project && r.File == file {
			for _, h := range r.Hunks {
				out[h] = true
			}
		}
	}
	return out
}

// HunksReviewed marks changes of a file reviewed, or takes the mark off.
type HunksReviewed struct {
	Session  SessionID `json:"session"`
	Project  string    `json:"project"`
	File     string    `json:"file"`
	Hunks    []string  `json:"hunks"`
	Reviewed bool      `json:"reviewed"`
}

func (e HunksReviewed) Scope() SessionID { return e.Session }

func (e HunksReviewed) Apply(s *State) error {
	if e.File == "" || len(e.Hunks) == 0 {
		return fmt.Errorf("a review needs a file and its hunks")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	r := se.ReviewOf(e.Project, e.File)
	r.Hunks = slices.Clone(r.Hunks)
	for _, h := range e.Hunks {
		k, found := slices.BinarySearch(r.Hunks, h)
		switch {
		case e.Reviewed && !found:
			r.Hunks = slices.Insert(r.Hunks, k, h)
		case !e.Reviewed && found:
			r.Hunks = slices.Delete(r.Hunks, k, k+1)
		}
	}
	se.setReview(r)
	return nil
}

// LinesSeen records lines of a file's change seen in the diff, by LineKey: read, or only skimmed. A line read
// stays read.
type LinesSeen struct {
	Session SessionID `json:"session"`
	Project string    `json:"project"`
	File    string    `json:"file"`
	Read    []string  `json:"read,omitempty"`
	Skimmed []string  `json:"skimmed,omitempty"`
}

func (e LinesSeen) Scope() SessionID { return e.Session }

func (e LinesSeen) Apply(s *State) error {
	if e.File == "" || len(e.Read)+len(e.Skimmed) == 0 {
		return fmt.Errorf("seen lines need a file and lines")
	}
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	r := se.ReviewOf(e.Project, e.File)
	r.Read = insert(r.Read, e.Read...)
	var skim []string
	for _, k := range e.Skimmed {
		if _, read := slices.BinarySearch(r.Read, k); !read {
			skim = append(skim, k)
		}
	}
	r.Skimmed = slices.DeleteFunc(insert(r.Skimmed, skim...), func(k string) bool {
		_, read := slices.BinarySearch(r.Read, k)
		return read
	})
	se.setReview(r)
	return nil
}

// The commit gate (agents.md, "Guardrails"): how a commit is stopped while agent-written lines are unread. Hard
// blocks it until "commit anyway", soft asks once, off lets it through.
const (
	GateHard = "hard"
	GateSoft = "soft"
	GateOff  = "off"
)

// DefaultGate is a kind of session's commit gate when nothing else sets one: hard for work, plan and a new repo,
// soft for jobs, off for the rest (reviews, direct).
func DefaultGate(k Kind) string {
	switch k {
	case KindWork, KindPlan, KindNewRepo:
		return GateHard
	case KindJob:
		return GateSoft
	}
	return GateOff
}

// GateOverride is a commit made past a commit gate that didn't pass: anyway past a hard one, or confirmed past a
// soft one.
type GateOverride struct {
	Project string    `json:"project,omitempty"`
	Gate    string    `json:"gate"`              // its name: "ledger"
	Mode    string    `json:"mode"`              // GateHard or GateSoft
	Unread  int       `json:"unread"`            // what it counted as left: the agent-written lines not read
	Trailer string    `json:"trailer,omitempty"` // what the commit carries for it: "Unreviewed-Agent-Lines: 38"
	Commit  string    `json:"commit,omitempty"`  // its hash
	Subject string    `json:"subject,omitempty"`
	Time    time.Time `json:"time,omitzero"`
}

// CommitGateOverridden records a commit made past the gate, in the session's log and state.
type CommitGateOverridden struct {
	Session  SessionID    `json:"session"`
	Override GateOverride `json:"override"`
}

func (e CommitGateOverridden) Scope() SessionID { return e.Session }

func (e CommitGateOverridden) Apply(s *State) error {
	se, err := s.mustSession(e.Session)
	if err != nil {
		return err
	}
	se.Overrides = append(slices.Clone(se.Overrides), e.Override)
	return nil
}
