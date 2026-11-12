package claude

import (
	"encoding/json"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/guard"
)

// NoPosting is why an agent's attempt to post to a pull request's review or conversation is refused.
const NoPosting = guard.NoPosting

// check decides a tool call for the guard rails (agent/guard): allow, deny with a reason, ask (Oriel's permission
// flow decides, through the PermissionRequest the ask fires in every mode), or "" to leave it to the mode.
func check(spec agent.StartSpec, tool string, input json.RawMessage) checkOut {
	switch d := guard.Check(spec, tool, input); {
	case d.Allow:
		return checkOut{Decision: "allow"}
	case d.Deny:
		return checkOut{Decision: "deny", Reason: d.Reason}
	case d.Ask:
		return checkOut{Decision: "ask", Reason: d.Reason}
	}
	return checkOut{}
}
