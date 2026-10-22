package agent

import "context"

// ProcessStopper is a session that stops a process a turn left running (TurnEnd.Running), by its ID.
type ProcessStopper interface {
	StopProcess(ctx context.Context, id string) error
}
