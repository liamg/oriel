package agentrun

import (
	"testing"

	"github.com/liamg/oriel/core"
)

// A network rule lets through the host it names, or the subdomains a *. names, on the port it names if any; never
// a host that only looks like one.
func TestNetRulesRefuseLookAlikes(t *testing.T) {
	for _, c := range []struct {
		rule, host string
		want       bool
	}{
		{"net(api.github.com)", "api.github.com", true},
		{"net(*.github.com)", "api.github.com", true},
		{"net(*.github.com)", "github.com", true},
		{"net(*.github.com:443)", "api.github.com:443", true},
		{"net(api.github.com)", "api.github.com.evil.example", false},
		{"net(*.github.com)", "evilgithub.com", false},
		{"net(*.github.com)", "github.com.evil.example", false},
		{"net(*.github.com:443)", "api.github.com:8443", false},
		{"net(*.github.com:443)", "api.github.com", false},
		{"Bash(go test:*)", "api.github.com", false},
	} {
		if got := matchRule(c.rule, core.Ask{Kind: core.AskNetwork, Host: c.host}); got != c.want {
			t.Errorf("%s for %s = %v, want %v", c.rule, c.host, got, c.want)
		}
	}
}
