package host

import (
	"context"
	"fmt"
	"slices"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostapi"
)

// ShipRename renames a project's branch. The app refuses first when its PR is open on it.
func (w *Host) ShipRename(ctx context.Context, r hostapi.RenameReq) (err error) {
	defer w.env.Recover("ShipRename", &err)
	if err := w.codePart.ShipRename(ctx, r); err != nil {
		return err
	}
	w.WriteNotes(r.Session)
	return nil
}

// NewBranch moves a project onto a fresh branch cut from its repo's default branch, freshly fetched, which becomes
// the session's branch for it; the one it leaves is kept. It answers the new branch's name.
func (w *Host) NewBranch(ctx context.Context, r hostapi.NewBranchReq) (_ string, err error) {
	defer w.env.Recover("NewBranch", &err)
	name, err := w.workPart.NewBranch(ctx, r)
	if err != nil {
		return "", err
	}
	w.WriteNotes(r.Session)
	return name, nil
}

// ShipSwitchBack puts a project's worktree back on the session's branch for it.
func (w *Host) ShipSwitchBack(ctx context.Context, r hostapi.ShipRef) (err error) {
	defer w.env.Recover("ShipSwitchBack", &err)
	if err := w.SwitchBack(ctx, r.Session, r.Project); err != nil {
		return err
	}
	if p, ok := w.projectOf(r); ok {
		w.Note(r.Session, fmt.Sprintf("The user switched %s back to this session's branch %s. Stay on it.", p.Name, p.Branch))
	}
	return nil
}

// ShipAdopt makes the branch a project's worktree is on the session's branch for it, when that's safe.
func (w *Host) ShipAdopt(ctx context.Context, r hostapi.ShipRef) (err error) {
	defer w.env.Recover("ShipAdopt", &err)
	if err := w.Adopt(ctx, r.Session, r.Project); err != nil {
		return err
	}
	w.WriteNotes(r.Session)
	if p, ok := w.projectOf(r); ok {
		w.Note(r.Session, fmt.Sprintf("The user made %s this session's branch for %s: Oriel pushes it now.", p.Branch, p.Name))
	}
	return nil
}

// projectOf is the project r names.
func (w *Host) projectOf(r hostapi.ShipRef) (core.Project, bool) {
	se, err := w.session(r.Session)
	if err != nil {
		return core.Project{}, false
	}
	i := slices.IndexFunc(se.Projects, func(p core.Project) bool { return p.Name == r.Project })
	if i < 0 {
		return core.Project{}, false
	}
	return se.Projects[i], true
}
