package core

import (
	"fmt"
	"slices"
)

func init() {
	RegisterEvent[HunksReviewed]("HunksReviewed")
}

// Review is what of a file's change has been marked reviewed in a session: its changes by ID, a hash of what each
// removes and adds (gitx.Hunk.ID), so a mark stays with its change while the rest of the file is edited.
type Review struct {
	Project string   `json:"project"`
	File    string   `json:"file"` // relative to the project, slash-separated
	Hunks   []string `json:"hunks"`
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
	i := slices.IndexFunc(se.Reviews, func(r Review) bool { return r.Project == e.Project && r.File == e.File })
	r := Review{Project: e.Project, File: e.File}
	if i >= 0 {
		r.Hunks = slices.Clone(se.Reviews[i].Hunks)
	}
	for _, h := range e.Hunks {
		k, found := slices.BinarySearch(r.Hunks, h)
		switch {
		case e.Reviewed && !found:
			r.Hunks = slices.Insert(r.Hunks, k, h)
		case !e.Reviewed && found:
			r.Hunks = slices.Delete(r.Hunks, k, k+1)
		}
	}
	out := slices.Clone(se.Reviews)
	switch {
	case i < 0 && len(r.Hunks) > 0:
		out = append(out, r)
	case i >= 0 && len(r.Hunks) > 0:
		out[i] = r
	case i >= 0:
		out = slices.Delete(out, i, i+1)
	}
	se.Reviews = out
	return nil
}
