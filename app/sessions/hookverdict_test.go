package sessions

import (
	"testing"

	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/registry"
)

// A Permission hook's answer settles an ask only as allow or deny, whatever shape it comes in; anything else
// ("always", a typo) leaves it to the user.
func TestHookVerdict(t *testing.T) {
	for _, tt := range []struct {
		answer any
		want   hostapi.Decision
		note   string
	}{
		{"allow", hostapi.DecisionAllow, ""},
		{true, hostapi.DecisionAllow, ""},
		{false, hostapi.DecisionDeny, ""},
		{registry.PermissionVerdict{Decision: "deny", Note: "not on main"}, hostapi.DecisionDeny, "not on main"},
		{map[string]any{"decision": "allow", "note": "ok"}, hostapi.DecisionAllow, "ok"},
		{"always", "", ""},
		{map[string]any{"decision": "alow"}, "", ""},
		{registry.PermissionVerdict{Decision: "always"}, "", ""},
		{42, "", ""},
	} {
		v := hookVerdict(tt.answer)
		if v.Decision != tt.want || v.Note != tt.note {
			t.Errorf("hookVerdict(%#v) = %+v, want %q %q", tt.answer, v, tt.want, tt.note)
		}
	}
}
