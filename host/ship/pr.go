package ship

import (
	"context"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// What the code hosts say of a session's branches' pull requests. The app's code hosts make the calls themselves;
// the host keeps what they found, for the agent, and checks a PR is raised from the session's own branch.

// SawPR keeps what a code host said of a branch's pull request.
func (x *Ships) SawPR(r hostapi.PRSeenReq) (err error) {
	defer x.env.Recover("SawPR", &err)
	x.sawPR(r.Session, r.Project, r.Branch, r.Open)
	return nil
}

func (x *Ships) sawPR(id core.SessionID, project, branch string, open bool) {
	x.ship.mu.Lock()
	defer x.ship.mu.Unlock()
	key := shipKey(id, project) + "/" + branch
	if open {
		x.ship.open[key] = true
	} else {
		delete(x.ship.open, key)
	}
}

// PROpen says the last code host reading of a branch found its PR open, whose page shows the PR rather than a draft.
func (x *Ships) PROpen(id core.SessionID, project, branch string) bool {
	x.ship.mu.Lock()
	defer x.ship.mu.Unlock()
	return x.ship.open[shipKey(id, project)+"/"+branch]
}

// ShipBranch is the branch a project's PR is raised from: the session's own, refused when the worktree is off it.
func (x *Ships) ShipBranch(ctx context.Context, r hostapi.ShipRef) (_ string, err error) {
	defer x.env.Recover("ShipBranch", &err)
	_, p, _, err := hostcore.ProjectRepo(ctx, x.env.Git, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	return hostcore.OwnBranch(x.env.Git, p)
}
