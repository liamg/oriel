package host

import "github.com/liamg/oriel/host/hostapi"

// The session host's calls, part by part, as the app's plugins make them: hostapi's, named here.
type (
	SessionCalls  = hostapi.SessionCalls
	SandboxCalls  = hostapi.SandboxCalls
	ShipCalls     = hostapi.ShipCalls
	CodeHostCalls = hostapi.CodeHostCalls
	RepoCalls     = hostapi.RepoCalls
	JobCalls      = hostapi.JobCalls
	StatsCalls    = hostapi.StatsCalls
)
