package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// DockerfilePath is where a repository asks for its own sandbox image.
const DockerfilePath = ".oriel/sandbox.Dockerfile"

// Image picks root's image, building it if needed: the image named in
// settings, else the primary project's approved Dockerfile, else the base.
func Image(ctx context.Context, root string, cfg sandbox.Config) (string, error) {
	if ws, ok := sandbox.LoadManifest(root); ok {
		for _, p := range ws.Projects {
			if !p.Primary {
				continue
			}
			ps := cfg.Projects[p.Repo]
			if ps.Image != "" {
				return ps.Image, nil
			}
			if df, ok := RepoDockerfile(p.Repo); ok {
				if ps.Dockerfile == container.Sum(df) {
					tag := container.ProjectImage(df)
					return tag, container.EnsureImage(ctx, tag, df)
				}
				notePending(p.Repo, df)
			}
		}
	}
	if cfg.Image != "" {
		return cfg.Image, nil
	}
	tag := container.BaseImage()
	return tag, container.EnsureImage(ctx, tag, container.BaseDockerfile)
}

// RepoDockerfile reads the repo's sandbox Dockerfile from its default branch,
// never from a worktree the agent can edit.
func RepoDockerfile(repo string) (string, bool) {
	base := defaultBranch(repo)
	out, err := sandbox.Git(repo, "show", base+":"+DockerfilePath)
	if err != nil || strings.TrimSpace(out) == "" {
		return "", false
	}
	return out, true
}

var pendingMu sync.Mutex

func pendingPath() string { return filepath.Join(sandbox.Base(), "pending-dockerfiles.json") }

func notePending(repo, df string) {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	all := loadPending()
	all[repo] = sandbox.Pending{Repo: repo, Sum: container.Sum(df), Dockerfile: df}
	savePending(all)
}

func loadPending() map[string]sandbox.Pending {
	out := map[string]sandbox.Pending{}
	if b, err := os.ReadFile(pendingPath()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func savePending(all map[string]sandbox.Pending) {
	b, _ := json.MarshalIndent(all, "", "  ")
	_ = os.MkdirAll(sandbox.Base(), 0o700)
	_ = os.WriteFile(pendingPath(), b, 0o600)
}

// PendingDockerfiles lists the Dockerfiles awaiting approval, dropping any
// that have since been approved or removed.
func PendingDockerfiles(cfg sandbox.Config) []sandbox.Pending {
	pendingMu.Lock()
	defer pendingMu.Unlock()
	all := loadPending()
	var out []sandbox.Pending
	approved := false
	for repo, p := range all {
		if cfg.Projects[repo].Dockerfile == p.Sum {
			delete(all, repo)
			approved = true
			continue
		}
		out = append(out, p)
	}
	if approved {
		savePending(all)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Repo < out[j].Repo })
	return out
}

// defaultBranch is repo's default branch: origin's HEAD, else the first of
// the usual names that exists.
func defaultBranch(repo string) string {
	if s, err := sandbox.Git(repo, "symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"); err == nil {
		ref := strings.TrimSpace(s)
		if p := strings.TrimPrefix(ref, "refs/remotes/"); p != ref && p != "" {
			return p
		}
	}
	for _, c := range []string{"origin/main", "origin/master", "main", "master", "develop", "trunk"} {
		if _, err := sandbox.Git(repo, "rev-parse", "--verify", "--quiet", c+"^{commit}"); err == nil {
			return c
		}
	}
	return "HEAD"
}
