package host

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/host/worktree"
)

// Start starts a session of the spec's kind.
func (w *Host) Start(ctx context.Context, spec core.SessionSpec) (_ core.SessionID, err error) {
	defer w.recover("Start", &err)
	k, ok := w.kinds.Get(spec.Kind)
	if !ok {
		return "", fmt.Errorf("no kind of session %q", spec.Kind)
	}
	if k.NeedsRepo && len(spec.Repos) == 0 {
		return "", fmt.Errorf("a %s session needs a repo", strings.ToLower(k.Title))
	}
	if spec.Provider != "" {
		if _, ok := w.opts.Providers.Get(spec.Provider); !ok {
			return "", fmt.Errorf("no agent provider %q", spec.Provider)
		}
	}
	if spec.Sandbox.Container && w.opts.Sandbox == nil {
		return "", errors.New("this machine can't run a sandbox")
	}
	if b := spec.Sandbox.Backend; b != "" && spec.Sandbox.Container && !slices.Contains(w.opts.Sandbox.Backends(), b) {
		return "", fmt.Errorf("this machine can't run a %s sandbox", b)
	}
	se, err := w.work.New(ctx, spec, k)
	if err != nil {
		return "", err
	}
	se.Status = core.DeriveStatus(se)
	if err := w.emit(core.SessionSynced{Session: se}); err != nil {
		return "", err
	}
	if err := w.run.Begin(ctx, se.ID, spec, k); err != nil {
		return se.ID, err
	}
	if spec.AutoName && !w.opts.NoAutoName && spec.Title == "" && strings.TrimSpace(spec.Prompt) != "" {
		w.env.Go("naming a session", func() { w.autoName(se.ID, se.Name, spec.Prompt) })
	}
	return se.ID, nil
}

// Resume starts the session's agent again: by its own id when it had one, so the conversation carries on.
func (w *Host) Resume(ctx context.Context, id core.SessionID) (err error) {
	defer w.recover("Resume", &err)
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if se.Archived {
		if err := worktree.Reopen(ctx, se); err != nil {
			return err
		}
		if err := w.emit(core.SessionArchived{ID: id}); err != nil {
			return err
		}
	}
	if se.Agent.Provider == "" {
		w.restatus(id)
		return nil
	}
	return w.run.Resume(id)
}

// Stop stops the session's agent. Its threads wait for it.
func (w *Host) Stop(_ context.Context, id core.SessionID) (err error) {
	defer w.recover("Stop", &err)
	if _, err := w.session(id); err != nil {
		return err
	}
	w.run.Stop(id, "stopped", core.StoppedByYou)
	return nil
}

// Archive stops the agent and removes the worktrees, keeping threads, transcript and test runs. Worktrees with
// uncommitted work are refused: removing them would lose it.
func (w *Host) Archive(ctx context.Context, id core.SessionID) error {
	return w.End(ctx, EndReq{Session: id})
}

// Delete throws a session away: its agent stops, its worktrees go with whatever is in them, then its workspace and
// its log. The repos themselves are never touched. A direct session's directory isn't Oriel's to remove, so it's
// refused.
func (w *Host) Delete(ctx context.Context, id core.SessionID) error {
	return w.End(ctx, EndReq{Session: id, Delete: true})
}

// End archives or deletes a session, as r says.
func (w *Host) End(ctx context.Context, r EndReq) (err error) {
	defer w.recover("End", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if r.Delete {
		err = w.delete(ctx, se)
	} else {
		err = w.archive(ctx, se, r.Discard)
	}
	if err != nil || !r.DeleteBranch || !w.kinds.Spec(se).Can(core.OwnsBranch) {
		return err
	}
	return worktree.DeleteBranches(ctx, se)
}

func (w *Host) archive(ctx context.Context, se core.SessionState, discard bool) error {
	if !discard {
		if err := worktree.CheckClean(ctx, se); err != nil {
			return err
		}
	}
	w.run.Stop(se.ID, "archived", core.StoppedByYou)
	w.reap(se.ID)
	return w.work.Archive(ctx, se, discard)
}

// reapRounds bounds how often reap looks again for what the processes it killed started meanwhile.
const reapRounds = 5

// reap kills what session id's agents left running: every process that carries its key, however it was started
// (a shell's detached job, a server started with setsid, one re-parented when its shell exited), and what each
// started. It looks again after each kill for anything started between the look and the kill.
func (w *Host) reap(id core.SessionID) {
	key := w.env.SessionKey(id)
	for range reapRounds {
		ps, err := sysload.Keyed(key)
		if err != nil {
			w.env.Log.Warn("end: listing the session's processes", "session", id, "err", err)
			return
		}
		if len(ps) == 0 {
			return
		}
		for _, p := range ps {
			w.env.Log.Info("end: stopping a process the session left running", "session", id, "pid", p.PID, "command", p.Command)
		}
		sysload.Kill(ps)
	}
}

func (w *Host) delete(ctx context.Context, se core.SessionState) error {
	if !w.kinds.Spec(se).Can(core.Delete) {
		return fmt.Errorf("%s isn't Oriel's to delete: archive it instead", se.ID)
	}
	w.run.Stop(se.ID, "deleted", "")
	w.reap(se.ID)
	if err := w.work.Delete(ctx, se); err != nil {
		return err
	}
	w.run.Forget(se.ID)
	return w.logs.Forget(se.ID)
}

// StartWork makes a plan's worktrees and tells its agent to implement the plan (05-plan), as r asks: on the branches
// it names, clearing the plan's threads into a note unless they're kept, and with the agent restarted fresh on the
// work pack unless its conversation is kept, on the sandbox, network, model and effort given.
func (w *Host) StartWork(ctx context.Context, r StartWorkReq) (err error) {
	defer w.recover("StartWork", &err)
	se, err := w.session(r.Session)
	if err != nil {
		return err
	}
	if err := worktree.CanStartWork(ctx, se, r.Branches); err != nil {
		return err
	}
	if r.Sandbox != nil && *r.Sandbox && w.opts.Sandbox == nil {
		return errors.New("this machine can't run a sandbox")
	}
	prompt, err := w.work.StartWork(ctx, se, r.Branches, r.KeepThreads)
	if err != nil {
		return err
	}
	w.run.WriteNotes(se.ID)
	if err := w.run.WorkAgent(se.ID, r, "start work"); err != nil {
		return err
	}
	_, err = w.run.Comment(ctx, CommentReq{Session: se.ID, Thread: se.PlanThread(), Title: "Start work", Body: prompt})
	return err
}

// Configure restarts the session's agent on another model or effort, its conversation kept. It refuses mid-turn,
// which a restart would cut off.
func (w *Host) Configure(ctx context.Context, r ConfigureReq) (err error) {
	defer w.recover("Configure", &err)
	running, err := w.run.Configure(r)
	if err != nil || !running {
		return err
	}
	return w.Resume(ctx, r.Session)
}

// AddRepo brings another repo into a session, on the session's branch.
func (w *Host) AddRepo(ctx context.Context, id core.SessionID, ref core.RepoID) (err error) {
	defer w.recover("AddRepo", &err)
	se, err := w.session(id)
	if err != nil {
		return err
	}
	if !w.kinds.Spec(se).Can(core.AddProjects) {
		return fmt.Errorf("a %s session can't have repos added", se.Kind)
	}
	r, p, err := w.work.AddRepo(ctx, se, ref)
	if err != nil {
		return err
	}
	w.run.WriteNotes(id)
	w.run.Note(id, fmt.Sprintf("The repo %s joined this session at %s.", r.ID, p.Dir))
	return nil
}

// NextPR moves a project onto a fresh branch cut from its updated base, keeping the branch it leaves as a past PR.
// It refuses while that branch has uncommitted changes, or commits that are neither pushed nor on the base.
func (w *Host) NextPR(ctx context.Context, r NextPRReq) (err error) {
	defer w.recover("NextPR", &err)
	if err := w.work.NextPR(ctx, r); err != nil {
		return err
	}
	w.run.WriteNotes(r.Session)
	return nil
}

// UpdatePR is worktree.Worktrees.UpdatePR, with a panic recovered.
func (w *Host) UpdatePR(ctx context.Context, id core.SessionID) (_ map[string]PRMove, err error) {
	defer w.recover("UpdatePR", &err)
	return w.work.UpdatePR(ctx, id)
}

// Rename names a session.
func (w *Host) Rename(id core.SessionID, name string) (err error) {
	defer w.recover("Rename", &err)
	return w.emit(core.SessionRenamed{ID: id, Name: strings.TrimSpace(name)})
}

// resumeHandedOver is agentrun.Runner.ResumeHandedOver, resuming through Resume.
func (w *Host) resumeHandedOver() {
	w.run.ResumeHandedOver(func(id core.SessionID) error { return w.Resume(context.Background(), id) })
}
