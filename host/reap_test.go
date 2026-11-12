//go:build unix

package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"github.com/liamg/oriel/core"
)

// Ending a session, archived or deleted, stops what its agent left running however it was started: here a server
// in a session of its own, as a shell's detached job is, which killing the agent's process group misses. A process
// of the user's own, not the agent's, stays.
func TestEndingASessionStopsWhatItsAgentLeftRunning(t *testing.T) {
	for _, del := range []bool{false, true} {
		r := newRig(t)
		id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "Serve it", Provider: "fake", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
		a := r.agent()
		start := func(env []string) *exec.Cmd {
			cmd := exec.Command("sleep", "600")
			cmd.Env = append(os.Environ(), env...)
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			return cmd
		}
		left, mine := start(a.Spec.Env), start(nil)
		t.Cleanup(func() { _ = mine.Process.Kill(); _ = mine.Wait() })
		if err := r.w.End(context.Background(), EndReq{Session: id, Delete: del, Discard: true}); err != nil {
			t.Fatal(err)
		}
		ended := make(chan error, 1)
		go func() { ended <- left.Wait() }()
		t.Cleanup(func() { _ = left.Process.Kill() })
		var err error
		until(t, "what the agent left running to end", func() bool {
			select {
			case err = <-ended:
				return true
			default:
				return false
			}
		})
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Errorf("delete %v: what the agent left ended with %v, not killed", del, err)
		}
		if err := mine.Process.Signal(syscall.Signal(0)); err != nil {
			t.Errorf("delete %v: the user's own process was stopped: %v", del, err)
		}
	}
}
