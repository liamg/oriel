package core

import "slices"

func init() {
	RegisterEvent[ReposChanged]("ReposChanged")
}

// ReposChanged is the registered repos as they are now; Added are the ones this change brought.
type ReposChanged struct {
	Repos []Repo   `json:"repos"`
	Added []RepoID `json:"added,omitempty"`
}

func (e ReposChanged) Scope() SessionID { return "" }

func (e ReposChanged) Apply(s *State) error {
	s.Repos = slices.Clone(e.Repos)
	return nil
}

// Repo is the registered repo with id.
func (s State) Repo(id RepoID) (Repo, bool) {
	if i := slices.IndexFunc(s.Repos, func(r Repo) bool { return r.ID == id }); i >= 0 {
		return s.Repos[i], true
	}
	return Repo{}, false
}
