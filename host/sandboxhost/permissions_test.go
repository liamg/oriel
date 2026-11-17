package sandboxhost

import (
	"testing"

	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/sandbox/gateway"
)

// A session's tool rule is stored as the gateway's own action, so the two vocabularies are the same words.
func TestToolActionsAreTheGatewaysActions(t *testing.T) {
	for a, g := range map[hostapi.ToolAction]string{hostapi.ToolAllow: gateway.Allow, hostapi.ToolAsk: gateway.Ask, hostapi.ToolDeny: gateway.Deny} {
		if string(a) != g {
			t.Errorf("%q is the gateway's %q", a, g)
		}
	}
}
