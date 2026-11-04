package core

// DeriveStatus is a session's status from what's in it, by crit's rules:
// a question, a lost login, a security-key touch, a terminal's dialog or a permission waits on you first; then the agent at work; then a reply you haven't answered, or
// work queued for an agent that isn't running; then work queued for one that is.
func DeriveStatus(se SessionState) Status {
	if se.Archived {
		return StatusOff
	}
	for _, a := range se.Asks {
		if a.Kind == AskQuestion || a.Kind == AskLogin || a.Kind == AskKey || a.Kind == AskTerminal {
			return StatusNeedsYou
		}
	}
	if len(se.Asks) > 0 {
		return StatusPermission
	}
	if se.Agent.Working || len(se.Agent.Background) > 0 {
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
		if !se.Agent.Live() {
			return StatusNeedsYou
		}
		return StatusWorking
	}
	if se.Agent.Error != "" {
		return StatusNeedsYou
	}
	if se.Agent.Provider == "" && len(se.Threads) == 0 && se.Kind != KindDirect {
		return StatusOff
	}
	return StatusQuiet
}
