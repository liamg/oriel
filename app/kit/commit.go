package kit

import (
	"fmt"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/plugin"
	"github.com/liamg/oriel/registry"
	"strings"
	"time"
)

// CommitWait is how long a commit waits for its gates to finish counting, checking every CommitPoll.
const (
	CommitWait = 15 * time.Second
	CommitPoll = 50 * time.Millisecond
)

// RecordOverrides puts a commit made past gates in the session's log (a notice) and its state; outside a session,
// in Oriel's log. busy waits for the host's records.
func RecordOverrides(h plugin.Host, busy Counter, se core.SessionState, project string, inSession bool, hash, message string, held []registry.GateCheck) {
	subject, _, _ := strings.Cut(strings.TrimSpace(message), "\n")
	short := hash[:min(len(hash), 7)]
	for _, c := range held {
		how := "confirmed past the soft " + c.Gate.Name + " gate"
		trailer := ""
		if c.Blocks() {
			how, trailer = "committed anyway past the "+c.Gate.Name+" gate", c.Trailer
		}
		text := fmt.Sprintf("%s %q %s: %s", short, subject, how, c.Reason)
		if trailer != "" {
			text += " (" + trailer + ")"
		}
		n := core.Notice{ID: "git.commit.gate", Severity: core.SevWarning, Source: "git", Title: "Committed past the commit gate", Text: text}
		if inSession {
			n.Session = se.ID
		}
		h.Notify(n)
		if !inSession {
			continue
		}
		cl, err := SessionHost(h)
		if err != nil {
			continue
		}
		ev := core.CommitGateOverridden{Session: se.ID, Override: core.GateOverride{Project: project, Gate: c.Gate.Name, Mode: c.Mode, Unread: c.Unread,
			Trailer: trailer, Commit: hash, Subject: subject, Time: time.Now().UTC()}}
		busy.Add(1)
		go func(cl host.Access) {
			defer busy.Done()
			if err := cl.RecordOverride(ev); err != nil {
				h.Log().Warn("commit gate", "err", err)
			}
		}(cl)
	}
}
