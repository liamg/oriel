package app_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/app/apptest"
	"github.com/liamg/oriel/app/apptest/rig"
	"github.com/liamg/oriel/core"
)

// A session's shell commands are marked through the session's freshness manager, which the app hands the host: a
// command that changed nothing runs no git over the session's projects, and one that changed a file is still given it.
func TestShellCommandsMarkOnlyWhatChanged(t *testing.T) {
	spawned := spawns(t)
	lw := rig.NewHost(t)
	r := apptest.NewWindow(t, apptest.WindowOpts{State: t.TempDir(), Config: apptest.Connect(lw.W), Timeout: time.Minute})
	other := rig.NewRepo(t, filepath.Join(t.TempDir(), "billing"))
	id, err := lw.W.Start(context.Background(), core.SessionSpec{Kind: core.KindWork, Name: "two", Provider: "fake",
		Repos: []core.RepoID{core.RepoID(lw.Repo), core.RepoID(other)}})
	if err != nil {
		t.Fatal(err)
	}
	a := lw.Fake.WaitStarted(t)
	a.Emit(agent.TurnStart{})
	gits := func() int {
		return len(slices.DeleteFunc(spawned(), func(s string) bool { return !strings.HasPrefix(s, "git,") }))
	}
	quiet := func() int {
		for n := -1; ; time.Sleep(400 * time.Millisecond) {
			if m := gits(); m == n {
				return m
			} else {
				n = m
			}
		}
	}
	bash := func(id, cmd string) {
		in, _ := json.Marshal(map[string]string{"command": cmd})
		a.Emit(agent.ToolCall{ID: id, Tool: "Bash", Input: in})
		a.Emit(agent.ToolResult{ID: id, Output: "ok"})
	}
	// The first mark with the session's manager there subscribes to its projects, and snapshots them all once.
	bash("b0", "true")
	before := quiet()
	bash("b1", "true")
	if n := quiet() - before; n != 0 {
		t.Errorf("a command that changed nothing ran git %d times: %v", n, spawned()[len(spawned())-n:])
	}
	st, _ := r.Store.State().Session(id)
	dir := st.Snapshot().Projects[1].Dir
	if err := os.WriteFile(filepath.Join(dir, "gen.go"), []byte("package gen\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bash("b2", "python3 gen.py")
	rig.WaitState(t, r.Store, "b2's files", func(st core.State) bool {
		se, ok := st.Session(id)
		return ok && slices.ContainsFunc(se.Snapshot().Transcript, func(it core.Item) bool {
			return it.ID == "b2" && len(it.Files) == 1 && it.Files[0].Path == filepath.Join(dir, "gen.go")
		})
	})
}
