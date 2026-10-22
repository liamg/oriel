package sessions

import (
	"testing"

	"github.com/liamg/oriel/registry"
)

// session.start-work from Lua or the CLI, without the dialog: {clear} and {fresh} are on unless false, {branches} is
// a table by project, and what's absent is the session's own.
func TestStartWorkArgs(t *testing.T) {
	r := startWorkReq("s", registry.Args{"clear": false, "branches": map[string]any{"pricing": "liam/x"}, "sandbox": true, "network": "open",
		"model": "opus"})
	if !r.KeepThreads || r.KeepConversation || r.Branches["pricing"] != "liam/x" || r.Sandbox == nil || !*r.Sandbox || r.Network != "open" ||
		r.Model != "opus" || r.Effort != "" {
		t.Errorf("%+v", r)
	}
	if r := startWorkReq("s", nil); r.KeepThreads || r.KeepConversation || r.Sandbox != nil || r.Branches != nil {
		t.Errorf("no args: %+v", r)
	}
}
