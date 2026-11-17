package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/host/hostapi"

	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// LoadManifest reads root's manifest; ok is false when root isn't a workspace.
func LoadManifest(root string) (m hostapi.Manifest, ok bool) {
	b, err := os.ReadFile(filepath.Join(root, hostapi.ManifestFile))
	if err != nil {
		return hostapi.Manifest{}, false
	}
	if json.Unmarshal(b, &m) != nil {
		return hostapi.Manifest{}, false
	}
	return m, true
}

// SaveManifest writes root's manifest.
func SaveManifest(root string, m hostapi.Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, hostapi.ManifestFile), append(b, '\n'), 0o644)
}

// newWorkspace makes an empty workspace at root: a git repo of its own, so plan docs diff as added files.
func newWorkspace(ctx context.Context, root string, m hostapi.Manifest, notes []string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if !gitx.IsRepo(root) {
		if err := gitx.Init(ctx, root, ""); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	ignore := hostapi.ManifestFile + "\n" + hostapi.ReposDir + "/\n" + strings.Join(notes, "\n") + "\n"
	if err := os.WriteFile(exclude, []byte(ignore), 0o644); err != nil {
		return err
	}
	return SaveManifest(root, m)
}

// addProject makes a worktree of repo in the workspace: on a fresh branch cut from the fetched base, or pinned
// at start when detached.
func addProject(ctx context.Context, root string, m *hostapi.Manifest, repo, branch, start string, detached bool) (hostapi.ManifestEntry, error) {
	r := gitx.Repo{Dir: repo}
	repo = r.MainClone(ctx)
	r = gitx.Repo{Dir: repo}
	if slices.ContainsFunc(m.Projects, func(p hostapi.ManifestEntry) bool { return p.Repo == repo }) {
		return hostapi.ManifestEntry{}, fmt.Errorf("%s is already a project in this session", repo)
	}
	name := uniqueProjectName(*m, repo)
	p := hostapi.ManifestEntry{Name: name, Repo: repo, Dir: filepath.Join(hostapi.ReposDir, name), Primary: len(m.Projects) == 0}
	if start == "" {
		start = r.SyncedBase(ctx)
	}
	p.Base = start
	abs := filepath.Join(root, p.Dir)
	if detached {
		if err := r.AddWorktree(ctx, abs, "", start); err != nil {
			return hostapi.ManifestEntry{}, fmt.Errorf("a worktree of %s: %w", repo, err)
		}
		p.Detached = true
		p.Commit, _ = gitx.Repo{Dir: abs}.Head(ctx)
	} else {
		p.Branch = r.FreshBranch(ctx, branch)
		if err := r.AddWorktree(ctx, abs, p.Branch, start); err != nil {
			return hostapi.ManifestEntry{}, fmt.Errorf("a worktree of %s: %w", repo, err)
		}
	}
	m.Projects = append(m.Projects, p)
	return p, nil
}

func uniqueProjectName(m hostapi.Manifest, repo string) string {
	base := hostapi.Sanitise(filepath.Base(repo))
	if base == "" {
		base = "project"
	}
	if !slices.ContainsFunc(m.Projects, func(p hostapi.ManifestEntry) bool { return p.Name == base }) {
		return base
	}
	sum := sha256.Sum256([]byte(repo))
	return base + "-" + hex.EncodeToString(sum[:3])
}

// BranchName is <you>/<ticket>-<slug>.
func BranchName(you, ticket, slug string) string {
	name := hostapi.Sanitise(slug)
	if t := hostapi.Sanitise(ticket); t != "" && !strings.HasPrefix(name, t) {
		name = strings.Trim(t+"-"+name, "-")
	}
	if name == "" {
		name = "work"
	}
	return you + "/" + hostcore.TrimWords(name, 6)
}
