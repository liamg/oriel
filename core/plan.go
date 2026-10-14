package core

// DocsProject is the project an anchor names for the docs at a workspace's root, outside every project: a plan's
// PLAN.md. Its File is relative to the session's Dir.
const DocsProject = "__docs__"

// Planning reports whether the session is a plan waiting on Start work.
func (s SessionState) Planning() bool { return s.StartWork && !s.Started }

// PlanThread is the thread the session's prompt opened, where Start work is said: its first not anchored to a place.
func (s SessionState) PlanThread() ThreadID {
	for _, t := range s.Threads {
		if t.Anchor == nil && !t.Btw {
			return t.ID
		}
	}
	return ""
}

// Holding are the threads that hold up Start work: every open one but the plan's own and side questions.
func (s SessionState) Holding() []Thread {
	plan := s.PlanThread()
	var out []Thread
	for _, t := range s.Threads {
		if t.Open() && !t.Btw && len(t.Comments) > 0 && t.ID != plan {
			out = append(out, t)
		}
	}
	return out
}
