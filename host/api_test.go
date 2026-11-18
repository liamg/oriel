package host

import (
	"reflect"
	"slices"
	"testing"

	"github.com/liamg/oriel/host/hostapi"
)

// The host's exported methods are its hostapi contracts and the few the process that runs it calls, and nothing
// else: a part's own methods (worktree.New, Runner.Begin, SaveProjects…) don't leak onto it, as they did while it
// embedded its parts.
func TestTheHostsAPIIsItsContracts(t *testing.T) {
	want := map[string]bool{}
	for _, c := range []reflect.Type{
		reflect.TypeFor[hostapi.SessionCalls](), reflect.TypeFor[hostapi.Wiring](), reflect.TypeFor[hostapi.SandboxCalls](),
		reflect.TypeFor[hostapi.ShipCalls](), reflect.TypeFor[hostapi.CodeHostCalls](), reflect.TypeFor[hostapi.RepoCalls](),
		reflect.TypeFor[hostapi.JobCalls](), reflect.TypeFor[hostapi.StatsCalls](),
	} {
		for i := range c.NumMethod() {
			want[c.Method(i).Name] = true
		}
	}
	// What the process running the host calls (cmd/oriel/cli, app/shell) and tests read back.
	for _, m := range []string{"Store", "Close", "HandOver", "Logs", "SampleLoad", "SweepSandboxes", "RefreshPrices",
		"RunSchedules", "RunDue", "Prompts", "SandboxConfig"} {
		want[m] = true
	}
	var extra, missing []string
	ht := reflect.TypeFor[*Host]()
	for i := range ht.NumMethod() {
		if m := ht.Method(i).Name; !want[m] {
			extra = append(extra, m)
		}
	}
	for m := range want {
		if _, ok := ht.MethodByName(m); !ok {
			missing = append(missing, m)
		}
	}
	slices.Sort(extra)
	slices.Sort(missing)
	if len(extra) > 0 || len(missing) > 0 {
		t.Errorf("Host's methods beyond its API: %v; missing: %v", extra, missing)
	}
}
