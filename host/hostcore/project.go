package hostcore

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/liamg/oriel/core"
	"github.com/liamg/oriel/gitx"
	"github.com/liamg/oriel/sandbox"
)

// NewID is n random bytes in hex.
func NewID(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
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

// TrimWords keeps the first n hyphen-separated words of a name.
func TrimWords(s string, n int) string {
	parts := strings.Split(s, "-")
	if len(parts) <= n {
		return s
	}
	return strings.Join(parts[:n], "-")
}

// Within says whether path is under dir.
func Within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Plural is "s" unless n is 1.
func Plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// WhoAmI is the first part of a branch name: the user's first name from git, or their login.
func WhoAmI(ctx context.Context) string {
	if f := strings.Fields(gitx.UserName(ctx)); len(f) > 0 {
		if s := Sanitise(f[0]); s != "" {
			return s
		}
	}
	if u := Sanitise(os.Getenv("USER")); u != "" {
		return u
	}
	return "oriel"
}

// Gate is the agent's own gate for a spec: the light gate inside a sandbox by default.
func Gate(spec core.SessionSpec) string {
	if spec.Sandbox.Gate != "" {
		return spec.Sandbox.Gate
	}
	if spec.Sandbox.Container {
		return "light"
	}
	return "ask"
}

// AgentDir is where a session's agent starts: the workspace, or the directory itself.
func AgentDir(se core.SessionState) string {
	if se.Dir != "" {
		return se.Dir
	}
	if p, ok := se.Primary(); ok {
		return p.Dir
	}
	return ""
}

// Memories are a workspace's projects' memory directories, the primary's first: Claude Code's own for each
// repository, so every session in it shares them. A direct session already runs in the repository and has none.
func Memories(se core.SessionState) []sandbox.ProjectMemory {
	if se.InPlace() || se.Dir == "" {
		return nil
	}
	primary, ok := se.Primary()
	if !ok {
		return nil
	}
	var out []sandbox.ProjectMemory
	for _, p := range append([]core.Project{primary}, se.Projects...) {
		dir := sandbox.MemoryDir(cmp.Or(p.Repo, p.Dir))
		if !slices.ContainsFunc(out, func(m sandbox.ProjectMemory) bool { return m.Dir == dir }) {
			out = append(out, sandbox.ProjectMemory{Name: p.Name, Dir: dir})
		}
	}
	return out
}

// AnchorText is the anchored lines as they read now, so the thread can follow later edits.
func AnchorText(se core.SessionState, a *core.Anchor) string {
	dir := ""
	if a.Project == core.DocsProject {
		dir = se.Dir
	}
	for _, p := range se.Projects {
		if p.Name == a.Project || (a.Project == "" && p.Primary) {
			dir = p.Dir
		}
	}
	if dir == "" {
		if p, ok := se.Primary(); ok {
			dir = p.Dir
		}
	}
	b, err := os.ReadFile(filepath.Join(dir, a.File))
	if err != nil {
		return ""
	}
	lines := strings.Split(string(b), "\n")
	end := max(a.Line, a.EndLine)
	if a.Line < 1 || a.Line > len(lines) {
		return ""
	}
	return strings.Join(lines[a.Line-1:min(end, len(lines))], "\n")
}

// ProjectRepo is a session's project and its git, guarded in a sandboxed workspace.
func ProjectRepo(ctx context.Context, st State, kinds Kinds, id core.SessionID, project string) (core.SessionState, core.Project, gitx.Repo, error) {
	se, err := st.Session(id)
	if err != nil {
		return se, core.Project{}, gitx.Repo{}, err
	}
	i := slices.IndexFunc(se.Projects, func(p core.Project) bool { return p.Name == project })
	if i < 0 {
		return se, core.Project{}, gitx.Repo{}, fmt.Errorf("session %s has no project %q", id, project)
	}
	p := se.Projects[i]
	env, err := sandbox.GitEnv(p.Dir)
	if err != nil {
		return se, p, gitx.Repo{}, err
	}
	repo := gitx.Repo{Dir: p.Dir, Env: env}
	if !kinds.Spec(se).Can(core.OwnsBranch) {
		p = DirectBranch(ctx, p, repo)
	}
	return se, p, repo, nil
}

// DirectBranch is a direct session's project on the branch its checkout is on now: it's the user's own, and they
// switch it. A detached HEAD has none.
func DirectBranch(ctx context.Context, p core.Project, repo gitx.Repo) core.Project {
	p.Branch = ""
	if b, err := repo.Branch(ctx); err == nil && b != "HEAD" {
		p.Branch = b
	}
	return p
}

// OwnBranch refuses a push, pull or PR unless the worktree is on the session's branch for it: the sandbox can
// switch branches, and "push this workspace" must not push anything else.
func OwnBranch(p core.Project) (string, error) {
	if p.Branch == "" || p.Detached || p.ReadOnly {
		return "", fmt.Errorf("%s has no branch of this session's to push", p.Name)
	}
	out, err := sandbox.Git(p.Dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if got := strings.TrimSpace(out); got != p.Branch {
		return "", fmt.Errorf("%s is on %q, not this session's branch %q: Oriel only pushes and raises PRs from %q. Switch back first",
			p.Name, got, p.Branch, p.Branch)
	}
	return p.Branch, nil
}

// NotesFiles are the agent notes generated at a workspace's root: CLAUDE.md for Claude, AGENTS.md for Codex.
var NotesFiles = []string{"CLAUDE.md", "AGENTS.md"}
