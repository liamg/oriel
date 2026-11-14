//oriel:kind job

package jobs

import (
	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
)

const (
	RunRunning = hostapi.RunRunning
	RunDone    = hostapi.RunDone
	RunBlocked = hostapi.RunBlocked
	RunStopped = hostapi.RunStopped
	RunSkipped = hostapi.RunSkipped
)

type Run = hostapi.Run

// blockedMark starts a blocked run's answer (the job pack's).
const blockedMark = hostapi.RunBlockedMark

// JobOf is the saved job a session is a run of; "" for any other session.
func JobOf(se core.SessionState) string { return hostapi.JobOf(se) }

// RunOf reads a job session's run from its request thread.
func RunOf(se core.SessionState) Run { return hostapi.RunOf(se) }
