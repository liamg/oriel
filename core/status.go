package core

// DeriveStatus is a session's status from what's in it, by crit's rules (crit-reference.md → Status colours):
// a question, a lost login or a permission waits on you first; then the agent at work; then a reply you haven't answered, or
// work queued for an agent that isn't running; then work queued for one that is.
func DeriveStatus(se SessionState) Status {
	if se.Archived {
		return StatusOff
	}
	for _, a := range se.Asks {
		if a.Kind == AskQuestion || a.Kind == AskLogin {
			return StatusNeedsYou
		}
	}
	if len(se.Asks) > 0 {
		return StatusPermission
	}
	if se.Agent.Working {
		return StatusWorking
	}
	for _, a := range se.Subagents {
		if a.Running {
			return StatusWorking
		}
	}
	owes := false
	for _, t := range se.Threads {
		if !t.Open() || t.Btw {
			continue
		}
		if t.Busy {
			return StatusWorking
		}
		if t.AgentAnswered() {
			return StatusNeedsYou
		}
		owes = owes || t.WaitsOnAgent()
	}
	if owes {
		if !se.Agent.Running {
			return StatusNeedsYou
		}
		return StatusWorking
	}
	if se.Agent.Error != "" {
		return StatusNeedsYou
	}
	if se.Agent.Provider == "" && len(se.Threads) == 0 && se.Kind != KindDirect && se.Kind != KindScratch {
		return StatusOff
	}
	return StatusQuiet
}
