package host

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/agentrun"
	"github.com/liamg/oriel/host/hostapi"
	"github.com/liamg/oriel/internal/waitfor"
)

// signsSlowly makes the session's commits sign with a program that waits, as gpg does for a key's touch, until touch
// is called; then it fails. It waits on a pipe rather than polling, so it costs nothing while it waits, and writes its
// pid to the file pidAt names before it does.
func signsSlowly(t *testing.T, dir string) (touch func(), pidAt string) {
	t.Helper()
	tmp := t.TempDir()
	prog, fifo := filepath.Join(tmp, "gpg"), filepath.Join(tmp, "touch")
	pidAt = filepath.Join(tmp, "pid")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\necho $$ > \"" + pidAt + "\"\nread _ < \"" + fifo + "\"\nexit 1\n"
	if err := os.WriteFile(prog, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tell := func(flag int) {
		f, err := os.OpenFile(fifo, os.O_WRONLY|flag, 0)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		_, _ = f.WriteString("\n")
	}
	// The touch waits for the program to be reading: the ask can come before git has started it.
	touch = func() { tell(0) }
	// No reader once the test is done (the program never ran, or it's gone) means nothing to tell: don't wait for one.
	t.Cleanup(func() { tell(syscall.O_NONBLOCK) })
	for _, kv := range [][]string{{"commit.gpgsign", "true"}, {"gpg.program", prog}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v: %s", err, out)
		}
	}
	return touch, pidAt
}

// running says the process whose pid the file at path holds is still running.
func running(t *testing.T, path string) bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the signing program never said its pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	return syscall.Kill(pid, 0) == nil
}

func keyAsk(se core.SessionState) (core.Ask, bool) {
	for _, a := range se.Asks {
		if a.Kind == core.AskKey {
			return a, true
		}
	}
	return core.Ask{}, false
}

// A commit that has waited on signing past KeyWait asks its session for a touch,
// and the ask goes when the commit ends.
func TestSigningAsksForASecurityKeyTouch(t *testing.T) {
	r := newRigWith(t, func(o *Options) { o.KeyWait = 50 * time.Millisecond })
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "n", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	p, _ := r.session(id).Primary()
	touch, _ := signsSlowly(t, p.Dir)
	if err := os.WriteFile(filepath.Join(p.Dir, "round.go"), []byte("package pricing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.w.code.ShipCommit(context.Background(), hostapi.ShipCommitReq{Session: id, Project: p.Name, Message: "two"})
		done <- err
	}()
	se := r.eventually(id, "the key ask", func(se core.SessionState) bool { _, ok := keyAsk(se); return ok && se.Status == core.StatusNeedsYou })
	if a, _ := keyAsk(se); a.Summary != "git commit" || se.Status != core.StatusNeedsYou {
		t.Errorf("asked %+v with the session %s", a, se.Status)
	}
	touch()
	if err := <-done; err == nil {
		t.Error("the commit signed by a failing program went through")
	}
	r.eventually(id, "the key ask gone", func(se core.SessionState) bool { _, ok := keyAsk(se); return !ok })
}

// Cancel on the key's card (a deny) stops the operation waiting on it.
func TestCancellingTheKeyStopsTheOperation(t *testing.T) {
	r := newRigWith(t, func(o *Options) { o.KeyWait = 50 * time.Millisecond })
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "n", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	p, _ := r.session(id).Primary()
	_, pidAt := signsSlowly(t, p.Dir)
	if err := os.WriteFile(filepath.Join(p.Dir, "round.go"), []byte("package pricing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := r.w.code.ShipCommit(context.Background(), hostapi.ShipCommitReq{Session: id, Project: p.Name, Message: "two"})
		done <- err
	}()
	se := r.eventually(id, "the key ask", func(se core.SessionState) bool { _, ok := keyAsk(se); return ok })
	a, _ := keyAsk(se)
	// The ask comes after a while whatever git is doing; cancel once the signing program is running, so its stopping is
	// what's checked.
	waitfor.Poll(t, "the signing program running", func() bool {
		b, err := os.ReadFile(pidAt)
		return err == nil && strings.HasSuffix(string(b), "\n") // its whole pid written
	})
	if err := r.w.run.Answer(hostapi.AnswerReq{Session: id, Ask: a.ID, Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, agentrun.ErrKeyCancelled) {
			t.Errorf("the cancelled commit returned %v", err)
		}
	case <-time.After(waitTimeout):
		t.Fatal("cancelling never stopped the commit")
	}
	r.eventually(id, "the key ask gone", func(se core.SessionState) bool { _, ok := keyAsk(se); return !ok })
	// Cancelling stops what git started too: the signing program doesn't go on waiting for a key nobody will touch.
	waitfor.PollFor(t, 10*time.Second, "the signing program to stop with the cancelled commit", func() bool { return !running(t, pidAt) })
}
