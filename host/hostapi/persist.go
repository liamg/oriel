package hostapi

import (
	"time"

	"github.com/liamg/oriel/core"
)

type SearchReq struct {
	Query    string `json:"query"`
	Archived bool   `json:"archived,omitempty"` // archived sessions too
}

// Hit is a match in a session's threads or transcript.
type Hit struct {
	Session core.SessionID `json:"session"`
	Thread  core.ThreadID  `json:"thread,omitempty"`
	Item    string         `json:"item,omitempty"`
	Text    string         `json:"text"`
}

// StatsSessionReq asks for one session's stats.
type StatsSessionReq struct {
	Session core.SessionID `json:"session"`
	Split   bool           `json:"split,omitempty"`  // charge each repo its share; otherwise the whole session
	Offset  int            `json:"offset,omitempty"` // the app's zone, seconds east of UTC
}

// UsageHistoryReq asks for the plan readings a host has kept: a provider's, or every one, since a time.
type UsageHistoryReq struct {
	Provider string    `json:"provider,omitempty"`
	Since    time.Time `json:"since,omitzero"`
}
