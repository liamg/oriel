package core

import (
	"encoding/json"
	"fmt"
	"slices"
)

func init() {
	RegisterEvent[HunksReviewed]("HunksReviewed")
	RegisterEvent[LinesSeen]("LinesSeen")
	RegisterEvent[CommitGateOverridden]("CommitGateOverridden")
	RegisterEvent[PRPushed]("PRPushed")
}

// PRPushed says a session's pull request's head moved to Head, as its code host was last read: its author pushed.
// It changes no state; plugins follow it (a review session offers the new commits).
type PRPushed struct {
	Session SessionID `json:"session"`
	Project string    `json:"project,omitempty"`
	Head    string    `json:"head"`
}

func (e PRPushed) Scope() SessionID { return e.Session }

func (e PRPushed) Apply(s *State) error {
	if e.Head == "" {
		return fmt.Errorf("a pushed PR needs its head")
	}
	return nil
}

// Review is what of a file's change has been reviewed in a session: its changes marked by ID, a hash of what each
// removes and adds (gitx.Hunk.ID), so a mark stays with its change while the rest of the file is edited.
type Review struct {
	Project string   `json:"project"`
	File    string   `json:"file"` // relative to the project, slash-separated
	Hunks   []string `json:"hunks"`
}

// ReviewOf is a project's file's review in the session; empty when nothing of it has been reviewed.
func (s SessionState) ReviewOf(project, file string) Review {
	for _, r := range s.Reviews {
		if r.Project == project && r.File == file {
			return r
		}
	}
	return Review{Project: project, File: file}
}

func (r Review) empty() bool { return len(r.Hunks) == 0 }

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

// LinesSeen was a record of lines of a change seen in the diff. Nothing records it now; logs that have it still
// replay, and it changes nothing.
type LinesSeen struct {
	Session SessionID `json:"session"`
}

func (e LinesSeen) Scope() SessionID { return e.Session }

func (e LinesSeen) Apply(*State) error { return nil }

// CommitGateOverridden was a record of a commit made past the commit gate. Nothing records it now; logs that have
// it still replay, and it changes nothing.
type CommitGateOverridden struct {
	Session  SessionID       `json:"session"`
	Override json.RawMessage `json:"override,omitempty"`
}

func (e CommitGateOverridden) Scope() SessionID { return e.Session }

func (e CommitGateOverridden) Apply(*State) error { return nil }
