//oriel:kind review

package worktree

import (
	"cmp"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gh"
	"github.com/liamg/oriel/gitx"
)

// addPRProject fetches the PR's head (spec.PRRef, else GitHub's pull/N/head) and pins a worktree there.
func addPRProject(ctx context.Context, root string, m *Manifest, r core.Repo, spec core.SessionSpec) (ManifestEntry, error) {
	from, ref, err := prRefs(spec.PR, spec.PRRef, root)
	if err != nil {
		return ManifestEntry{}, err
	}
	if err := fetchPR(ctx, r.Path, from, ref); err != nil {
		return ManifestEntry{}, err
	}
	p, err := addProject(ctx, root, m, r.Path, "", ref, true)
	if err != nil || spec.PRBase == "" {
		return p, err
	}
	base := spec.PRBase
	if err := (gitx.Repo{Dir: r.Path}).FetchRef(ctx, "refs/heads/"+base, "refs/remotes/origin/"+base); err != nil {
		return p, nil // Changed compares with the default branch
	}
	return p, gitx.Repo{Dir: filepath.Join(root, p.Dir)}.SetBase(ctx, "origin/"+base)
}

// prRefs are where a review's PR is fetched from (from, else GitHub's pull/N/head) and the ref it's kept at.
func prRefs(url, from, root string) (string, string, error) {
	if from = strings.TrimSpace(from); from != "" {
		return from, "refs/oriel/pr/" + filepath.Base(root), nil
	}
	_, n, err := gh.ParsePR(url)
	if err != nil {
		return "", "", err
	}
	return fmt.Sprintf("pull/%d/head", n), fmt.Sprintf("refs/oriel/pr/%d", n), nil
}

func fetchPR(ctx context.Context, repo, from, ref string) error {
	if err := (gitx.Repo{Dir: repo}).FetchRef(ctx, from, ref); err != nil {
		return fmt.Errorf("fetching %s: %w", from, err)
	}
	return nil
}

// PRMove is a review's project moved on to its pull request's new head: Commits on from From to To, one for a
// force-push that rewrote it.
type PRMove struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Commits int    `json:"commits"`
}

// UpdatePR fetches a review's pull request again and moves its pinned worktrees to the head it has now, answering
// the projects that moved.
func (x *Worktrees) UpdatePR(ctx context.Context, id core.SessionID) (map[string]PRMove, error) {
	se, err := x.st.Session(id)
	if err != nil {
		return nil, err
	}
	rd, ok := core.DataOf[core.ReviewData](se)
	if !ok || rd.PR == "" {
		return nil, fmt.Errorf("session %s isn't reviewing a pull request", id)
	}
	from, ref, err := prRefs(rd.PR, rd.PRRef, se.Dir)
	if err != nil {
		return nil, err
	}
	moved := map[string]PRMove{}
	projects := slices.Clone(se.Projects)
	for i, p := range projects {
		if !p.Detached || p.Repo == "" || p.Base != ref {
			continue
		}
		if err := fetchPR(ctx, p.Repo, from, ref); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		g := gitx.Repo{Dir: p.Dir}
		head, err := g.Rev(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		if head == p.Commit {
			continue
		}
		n := 1
		if c, err := g.CountCommits(ctx, head, cmp.Or(p.Commit, "HEAD")); err == nil && c > 0 {
			n = c
		}
		if err := g.Detach(ctx, head); err != nil {
			return nil, fmt.Errorf("%s: %w", p.Name, err)
		}
		projects[i].Commit, moved[p.Name] = head, PRMove{From: p.Commit, To: head, Commits: n}
	}
	if len(moved) == 0 {
		return moved, nil
	}
	if err := x.st.Emit(core.ProjectsChanged{ID: se.ID, Branch: se.Branch, Projects: projects, Past: se.Past, Started: se.Started}); err != nil {
		return nil, err
	}
	x.SaveProjects(se.ID)
	return moved, nil
}
