package github

import (
	"context"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// What the code hosts say of a session's branches' pull requests. The app's code hosts make the calls themselves;
// the host keeps what they found, for the agent, and checks a PR is raised from the session's own branch.

// PRSeenReq is what a code host last said of a project's branch: whether its pull request is open.
type PRSeenReq struct {
	Session core.SessionID `json:"session"`
	Project string         `json:"project"`
	Branch  string         `json:"branch"`
	Open    bool           `json:"open,omitempty"`
}

// SawPR keeps what a code host said of a branch's pull request.
func (x *GitHub) SawPR(r PRSeenReq) (err error) {
	defer x.env.Recover("SawPR", &err)
	x.sawPR(r.Session, r.Project, r.Branch, r.Open)
	return nil
}

func (x *GitHub) sawPR(id core.SessionID, project, branch string, open bool) {
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
func (x *GitHub) PROpen(id core.SessionID, project, branch string) bool {
	x.ship.mu.Lock()
	defer x.ship.mu.Unlock()
	return x.ship.open[shipKey(id, project)+"/"+branch]
}

// ShipBranch is the branch a project's PR is raised from: the session's own, refused when the worktree is off it.
func (x *GitHub) ShipBranch(ctx context.Context, r ShipRef) (_ string, err error) {
	defer x.env.Recover("ShipBranch", &err)
	_, p, _, err := hostcore.ProjectRepo(ctx, x.st, x.deps.Kinds, r.Session, r.Project)
	if err != nil {
		return "", err
	}
	return hostcore.OwnBranch(p)
}
