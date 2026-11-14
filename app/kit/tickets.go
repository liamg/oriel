package kit

import (
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
)

// TicketSession is the session to continue a ticket in, rather than start another: one whose ticket is key.
func TicketSession(h plugin.Host, key string) (core.Session, bool) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, false
	}
	return NewestSession(h, func(st core.SessionState) bool { return strings.EqualFold(strings.TrimSpace(st.Ticket), key) })
}

// ContinueLabel is how a ticket with a session says so: "Continue · fix-prices".
func ContinueLabel(se core.Session) string { return "Continue · " + se.Name() }

// TicketStageTone is a ticket stage's colour: violet before it starts, cyan in progress, pink in review.
func TicketStageTone(stage string) string {
	switch stage {
	case registry.TicketStarted:
		return "status.working"
	case registry.TicketInReview:
		return "status.reviewer"
	}
	return "status.permission"
}
