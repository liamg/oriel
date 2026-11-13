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
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/host/hostcore"
)

// The workspace layout is crit's, unchanged: a directory holding the manifest, its own git repo
// for plan docs, and one worktree per project under repos/.
const (
	ManifestFile = ".oriel-workspace.json"
	ReposDir     = "repos"
)

// Manifest is a workspace's record of itself. Its fields are crit's, so a crit workspace reads as one.
type Manifest struct {
	Name          string          `json:"name"`
	CreatedAt     time.Time       `json:"createdAt"`
	Projects      []ManifestEntry `json:"projects"`
	Kind          string          `json:"kind,omitempty"`
	Job           string          `json:"job,omitempty"`
	WorkStartedAt time.Time       `json:"workStartedAt,omitzero"`
	Ticket        string          `json:"ticket,omitempty"`
	TicketTitle   string          `json:"ticketTitle,omitempty"`
	TicketURL     string          `json:"ticketUrl,omitempty"`
	Model         string          `json:"model,omitempty"`
	Label         string          `json:"label,omitempty"`
}

// ManifestEntry is a project as the manifest records it: Dir relative to the workspace, so it can move.
type ManifestEntry struct {
	Name     string       `json:"name"`
	Repo     string       `json:"repo"`
	Dir      string       `json:"dir"`
	Branch   string       `json:"branch"`
	Primary  bool         `json:"primary"`
	Detached bool         `json:"detached,omitempty"`
	Commit   string       `json:"commit,omitempty"`
	Base     string       `json:"base,omitempty"`
	Past     []ManifestPR `json:"past,omitempty"`
}

type ManifestPR struct {
	Branch   string    `json:"branch"`
	Number   int       `json:"number"`
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	State    string    `json:"state"`
	ClosedAt time.Time `json:"closedAt"`
}

// LoadManifest reads root's manifest; ok is false when root isn't a workspace.
func LoadManifest(root string) (m Manifest, ok bool) {
	b, err := os.ReadFile(filepath.Join(root, ManifestFile))
	if err != nil {
		return Manifest{}, false
	}
	if json.Unmarshal(b, &m) != nil {
		return Manifest{}, false
	}
	return m, true
}

// SaveManifest writes root's manifest.
func SaveManifest(root string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ManifestFile), append(b, '\n'), 0o644)
}

// projects are the manifest's projects with absolute directories.
func (m Manifest) projects(root string) []core.Project {
	out := make([]core.Project, 0, len(m.Projects))
	for _, p := range m.Projects {
		out = append(out, core.Project{Name: p.Name, Repo: p.Repo, Dir: filepath.Join(root, p.Dir), Branch: p.Branch, Base: p.Base,
			Primary: p.Primary, Detached: p.Detached, Commit: p.Commit, ReadOnly: p.Detached})
	}
	return out
}

func (m Manifest) past() []core.PastPR {
	var out []core.PastPR
	for _, p := range m.Projects {
		for _, pr := range p.Past {
			out = append(out, core.PastPR{Project: p.Name, Branch: pr.Branch, Number: pr.Number, URL: pr.URL, Title: pr.Title, State: pr.State, Closed: pr.ClosedAt})
		}
	}
	return out
}

// newWorkspace makes an empty workspace at root: a git repo of its own, so plan docs diff as added files.
func newWorkspace(ctx context.Context, root string, m Manifest) error {
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
	ignore := ManifestFile + "\n" + ReposDir + "/\n" + strings.Join(hostcore.NotesFiles, "\n") + "\n"
	if err := os.WriteFile(exclude, []byte(ignore), 0o644); err != nil {
		return err
	}
	return SaveManifest(root, m)
}

// addProject makes a worktree of repo in the workspace: on a fresh branch cut from the fetched base, or pinned
// at start when detached.
func addProject(ctx context.Context, root string, m *Manifest, repo, branch, start string, detached bool) (ManifestEntry, error) {
	r := gitx.Repo{Dir: repo}
	repo = r.MainClone(ctx)
	r = gitx.Repo{Dir: repo}
	if slices.ContainsFunc(m.Projects, func(p ManifestEntry) bool { return p.Repo == repo }) {
		return ManifestEntry{}, fmt.Errorf("%s is already a project in this session", repo)
	}
	name := uniqueProjectName(*m, repo)
	p := ManifestEntry{Name: name, Repo: repo, Dir: filepath.Join(ReposDir, name), Primary: len(m.Projects) == 0}
	if start == "" {
		start = r.SyncedBase(ctx)
	}
	p.Base = start
	abs := filepath.Join(root, p.Dir)
	if detached {
		if err := r.AddWorktree(ctx, abs, "", start); err != nil {
			return ManifestEntry{}, fmt.Errorf("a worktree of %s: %w", repo, err)
		}
		p.Detached = true
		p.Commit, _ = gitx.Repo{Dir: abs}.Head(ctx)
	} else {
		p.Branch = r.FreshBranch(ctx, branch)
		if err := r.AddWorktree(ctx, abs, p.Branch, start); err != nil {
			return ManifestEntry{}, fmt.Errorf("a worktree of %s: %w", repo, err)
		}
	}
	m.Projects = append(m.Projects, p)
	return p, nil
}

func uniqueProjectName(m Manifest, repo string) string {
	base := hostcore.Sanitise(filepath.Base(repo))
	if base == "" {
		base = "project"
	}
	if !slices.ContainsFunc(m.Projects, func(p ManifestEntry) bool { return p.Name == base }) {
		return base
	}
	sum := sha256.Sum256([]byte(repo))
	return base + "-" + hex.EncodeToString(sum[:3])
}

// BranchName is <you>/<ticket>-<slug>.
func BranchName(you, ticket, slug string) string {
	name := hostcore.Sanitise(slug)
	if t := hostcore.Sanitise(ticket); t != "" && !strings.HasPrefix(name, t) {
		name = strings.Trim(t+"-"+name, "-")
	}
	if name == "" {
		name = "work"
	}
	return you + "/" + hostcore.TrimWords(name, 6)
}
