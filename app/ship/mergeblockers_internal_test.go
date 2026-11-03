package ship

import (
	"testing"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/registry"
)

// A code host whose Blockers don't count its reviewer threads (a plugin's) still has them counted, from the threads
// the session mirrors: open, not resolved on the host, on this PR of this project.
func TestMirroredThreadsBlockAMerge(t *testing.T) {
	hosted := func(id string, pr int, project string, resolved bool) core.Thread {
		return core.Thread{ID: core.ThreadID(id), Status: core.ThreadOpen, Hosted: &core.HostThread{Host: "azure", Project: project, PR: pr, Resolved: resolved}}
	}
	se := core.SessionState{Threads: []core.Thread{
		hosted("a", 7, "api", false), hosted("b", 7, "api", false), hosted("c", 7, "api", true), hosted("d", 8, "api", false),
		hosted("e", 7, "web", false), {ID: "f", Status: core.ThreadOpen},
	}}
	bs := mirroredBlockers(se, "api", 7)
	if len(bs) != 1 || bs[0].Kind != registry.BlockThreads || bs[0].Count != 2 || bs[0].Text != "2 reviewer comments unresolved" {
		t.Errorf("blockers = %+v", bs)
	}
	if bs := mirroredBlockers(se, "api", 9); len(bs) != 0 {
		t.Errorf("a PR with no threads is held up: %+v", bs)
	}
}
