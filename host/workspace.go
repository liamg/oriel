package host

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
)

// The workspace layout is crit's, unchanged (sessions.md): a directory holding the manifest, its own git repo
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

func saveManifest(root string, m Manifest) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(root, ManifestFile), append(b, '\n'), 0o644)
}

// Projects is the manifest's projects with absolute directories.
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

// Sanitise reduces a string to a name safe for a directory and a git branch.
func Sanitise(s string) string {
	var sb strings.Builder
	for _, r := range strings.TrimSpace(strings.ToLower(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
			sb.WriteRune(r)
		case r == ' ', r == '.', r == '/':
			sb.WriteRune('-')
		}
	}
	out := sb.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return strings.Trim(out, "-")
}

// trimWords keeps the first n hyphen-separated words of a name.
func trimWords(s string, n int) string {
	parts := strings.Split(s, "-")
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[:n], "-")
}

// newWorkspace makes an empty workspace at root: a git repo of its own, so plan docs diff as added files.
func newWorkspace(ctx context.Context, root string, m Manifest) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if !gitx.IsRepo(root) {
		if _, err := runGit(ctx, root, "init", "--quiet"); err != nil {
			return fmt.Errorf("git init: %w", err)
		}
	}
	exclude := filepath.Join(root, ".git", "info", "exclude")
	if err := os.MkdirAll(filepath.Dir(exclude), 0o755); err != nil {
		return err
	}
	ignore := ManifestFile + "\n" + ReposDir + "/\n" + strings.Join(NotesFiles, "\n") + "\n"
	if err := os.WriteFile(exclude, []byte(ignore), 0o644); err != nil {
		return err
	}
	return saveManifest(root, m)
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
	base := Sanitise(filepath.Base(repo))
	if base == "" {
		base = "project"
	}
	if !slices.ContainsFunc(m.Projects, func(p ManifestEntry) bool { return p.Name == base }) {
		return base
	}
	sum := sha256.Sum256([]byte(repo))
	return base + "-" + hex.EncodeToString(sum[:3])
}

func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// whoAmI is the first part of a branch name: the user's first name from git, or their login.
func whoAmI(ctx context.Context) string {
	if out, err := exec.CommandContext(ctx, "git", "config", "--get", "user.name").Output(); err == nil {
		if f := strings.Fields(string(out)); len(f) > 0 {
			if s := Sanitise(f[0]); s != "" {
				return s
			}
		}
	}
	if u := Sanitise(os.Getenv("USER")); u != "" {
		return u
	}
	return "oriel"
}

// BranchName is <you>/<ticket>-<slug> (sessions.md → Lifecycle).
func BranchName(you, ticket, slug string) string {
	name := Sanitise(slug)
	if t := Sanitise(ticket); t != "" && !strings.HasPrefix(name, t) {
		name = strings.Trim(t+"-"+name, "-")
	}
	if name == "" {
		name = "work"
	}
	return you + "/" + trimWords(name, 6)
}
