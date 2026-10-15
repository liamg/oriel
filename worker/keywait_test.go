package worker

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/liamg/oriel/core"
)

// signsSlowly makes the session's commits sign with a program that takes secs, as gpg does waiting for a key's touch.
func signsSlowly(t *testing.T, dir string, secs string) {
	t.Helper()
	prog := filepath.Join(t.TempDir(), "gpg")
	if err := os.WriteFile(prog, []byte("#!/bin/sh\nsleep "+secs+"\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, kv := range [][]string{{"commit.gpgsign", "true"}, {"gpg.program", prog}} {
		if out, err := exec.Command("git", "-C", dir, "config", kv[0], kv[1]).CombinedOutput(); err != nil {
			t.Fatalf("git config: %v: %s", err, out)
		}
	}
}

func keyAsk(se core.SessionState) (core.Ask, bool) {
	for _, a := range se.Asks {
		if a.Kind == core.AskKey {
			return a, true
		}
	}
	return core.Ask{}, false
}

// SH-9 (crit's security-key prompt): a commit that has waited on signing past KeyWait asks its session for a touch,
// and the ask goes when the commit ends.
func TestSigningAsksForASecurityKeyTouch(t *testing.T) {
	r := newRig(t)
	r.w.opts.KeyWait = 50 * time.Millisecond
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "n", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	p, _ := r.session(id).Primary()
	signsSlowly(t, p.Dir, "0.6")
	os.WriteFile(filepath.Join(p.Dir, "round.go"), []byte("package pricing\n"), 0o644)
	done := make(chan error, 1)
	go func() {
		_, err := r.w.ShipCommit(context.Background(), ShipCommitReq{Session: id, Project: p.Name, Message: "two"})
		done <- err
	}()
	se := r.eventually(id, "the key ask", func(se core.SessionState) bool { _, ok := keyAsk(se); return ok })
	if a, _ := keyAsk(se); a.Summary != "git commit" || se.Status != core.StatusNeedsYou {
		t.Errorf("asked %+v with the session %s", a, se.Status)
	}
	if err := <-done; err == nil {
		t.Error("the commit signed by a failing program went through")
	}
	r.eventually(id, "the key ask gone", func(se core.SessionState) bool { _, ok := keyAsk(se); return !ok })
}

// SH-9: Cancel on the key's card (a deny) stops the operation waiting on it.
func TestCancellingTheKeyStopsTheOperation(t *testing.T) {
	r := newRig(t)
	r.w.opts.KeyWait = 50 * time.Millisecond
	id := r.start(core.SessionSpec{Kind: core.KindWork, Prompt: "n", Repos: []core.RepoID{core.RepoID(r.repo.Dir)}})
	p, _ := r.session(id).Primary()
	signsSlowly(t, p.Dir, "5")
	os.WriteFile(filepath.Join(p.Dir, "round.go"), []byte("package pricing\n"), 0o644)
	done := make(chan error, 1)
	go func() {
		_, err := r.w.ShipCommit(context.Background(), ShipCommitReq{Session: id, Project: p.Name, Message: "two"})
		done <- err
	}()
	se := r.eventually(id, "the key ask", func(se core.SessionState) bool { _, ok := keyAsk(se); return ok })
	a, _ := keyAsk(se)
	if err := r.w.Answer(AnswerReq{Session: id, Ask: a.ID, Decision: "deny"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrKeyCancelled) {
			t.Errorf("the cancelled commit returned %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelling never stopped the commit")
	}
	r.eventually(id, "the key ask gone", func(se core.SessionState) bool { _, ok := keyAsk(se); return !ok })
}
