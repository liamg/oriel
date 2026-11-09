package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/host/hostcore"
)

// branchTools change a project's branch the way Oriel records it, so it goes on pushing it: the agent never
// switches branches with git.
func branchTools(h *session) []agent.Tool {
	return []agent.Tool{
		{Name: "rename_branch", Description: "Rename this session's branch for a project (the primary one if project is empty). " +
			"Oriel pushes and raises the PR from the new name. Use it instead of git branch -m.",
			Schema: hostcore.Schema(`"name":`+str+`,"project":`+str, "name"), Call: h.toolRenameBranch},
		{Name: "new_branch", Description: "Start a fresh branch for a project (the primary one if project is empty), cut from its repo's default branch " +
			"freshly fetched, and switch the worktree to it; it becomes this session's branch, which Oriel pushes. The old branch is kept locally, " +
			"with its commits. name: the new branch's name (one is made if empty). Uncommitted work is refused unless carry is true, which takes it along. " +
			"Use it instead of git checkout -b or git switch -c.",
			Schema: hostcore.Schema(`"name":` + str + `,"project":` + str + `,"carry":` + bl), Call: h.toolNewBranch},
	}
}

// branchProject is the project a branch tool names, the primary one by default.
func (h *session) branchProject(name string) (core.Project, error) {
	se, err := h.r.st.Session(h.id)
	if err != nil {
		return core.Project{}, err
	}
	for _, p := range se.Projects {
		if p.Name == name || name == "" && p.Primary {
			return p, nil
		}
	}
	if name == "" && len(se.Projects) == 1 {
		return se.Projects[0], nil
	}
	if name == "" {
		return core.Project{}, errors.New("say which project")
	}
	return core.Project{}, fmt.Errorf("no project %q", name)
}

func (h *session) toolRenameBranch(ctx context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	name := strings.TrimSpace(a.Str("name"))
	if name == "" {
		return "", errors.New("say the branch's new name")
	}
	p, err := h.branchProject(a.Str("project"))
	if err != nil {
		return "", err
	}
	if h.r.deps.PROpen != nil && h.r.deps.PROpen(h.id, p.Name, p.Branch) {
		return "", fmt.Errorf("%s's pull request is open on %s: renaming would leave it behind. Keep the name", p.Name, p.Branch)
	}
	if err := h.r.deps.RenameBranch(ctx, h.id, p.Name, name); err != nil {
		return "", err
	}
	return fmt.Sprintf("Renamed %s's branch %s to %s. Oriel pushes and raises its PR from %s now.", p.Name, p.Branch, name, name), nil
}

func (h *session) toolNewBranch(ctx context.Context, raw json.RawMessage) (string, error) {
	a := hostcore.ParseArgs(raw)
	p, err := h.branchProject(a.Str("project"))
	if err != nil {
		return "", err
	}
	carry := a.Bool("carry")
	name, err := h.r.deps.NewBranch(ctx, h.id, p.Name, strings.TrimSpace(a.Str("name")), carry)
	if err != nil {
		return "", err
	}
	se, _ := h.r.st.Session(h.id)
	base := "the default branch"
	for _, q := range se.Projects {
		if q.Name == p.Name && q.Base != "" {
			base = q.Base
		}
	}
	out := fmt.Sprintf("Switched %s to a new branch %s, cut from %s. It's this session's branch now: Oriel pushes it. "+
		"The old branch %s is kept locally, with its commits; Oriel no longer pushes it.", p.Name, name, base, p.Branch)
	if carry {
		out += " Uncommitted work came along."
	}
	return out, nil
}
