package sandbox

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The ways a session can run.
const (
	ModeSandbox = "sandbox"
	ModeHost    = "host"
)

// Record is what oriel knows about one workspace's sandbox.
type Record struct {
	Root string `json:"root"`
	Mode string `json:"mode"`
	// AgentToken authenticates the agent on the session socket. It opens the
	// agent routes only, never the browser's.
	AgentToken string    `json:"agentToken"`
	CreatedAt  time.Time `json:"createdAt"`
	// Escaped is set while request_host has the session running on the host.
	Escaped bool `json:"escaped,omitempty"`
	// HostDenied holds a refused request_host for the rest of the session.
	HostDenied bool `json:"hostDenied,omitempty"`
	// Repo is set when root is a bare worktree of Repo rather than a
	// workspace: a pull request checkout.
	Repo string `json:"repo,omitempty"`
}

// Sandboxed reports whether the agent runs in the container right now.
func (r Record) Sandboxed() bool { return r.Mode == ModeSandbox && !r.Escaped }

// Base is where every sandbox's host-side files live.
func Base() string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = os.TempDir()
	}
	return filepath.Join(home, ".oriel", "sandbox")
}

// Key names root's directory under Base: readable, and short enough that the
// socket path inside it stays under the unix limit.
func Key(root string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(root)))
	name := filepath.Base(root)
	var b strings.Builder
	for _, r := range name {
		if b.Len() >= 24 {
			break
		}
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String() + "-" + hex.EncodeToString(sum[:])[:10]
}

// Dir is root's host-side directory.
func Dir(root string) string { return filepath.Join(Base(), Key(root)) }

func recordPath(root string) string { return filepath.Join(Dir(root), "record.json") }

// ManifestPath is the authoritative copy of the workspace manifest.
func ManifestPath(root string) string { return filepath.Join(Dir(root), "manifest.json") }

// LockPath is where a sandboxed session's server lock lives, token and all.
func LockPath(root string) string { return filepath.Join(Dir(root), "session.lock") }

// RunDir holds the session sockets. On Linux the directory is mounted, so a
// restarted server's new socket is still the one the container sees.
func RunDir(root string) string { return filepath.Join(Dir(root), "run") }

// TmpDir is the host side of the agent's Claude Code temp dir, so the
// session can read its scratchpad.
func TmpDir(root string) string { return filepath.Join(Dir(root), "tmp") }

// SocketPath is the session socket on the host.
func SocketPath(root string) string { return filepath.Join(RunDir(root), "oriel.sock") }

// GrantsPath holds the gateway rules granted for this session.
func GrantsPath(root string) string { return filepath.Join(Dir(root), "grants.json") }

// CallsPath is the session's gateway call log.
func CallsPath(root string) string { return filepath.Join(Dir(root), "calls.jsonl") }

// EgressGrantsPath holds the network rules granted for this session.
func EgressGrantsPath(root string) string { return filepath.Join(Dir(root), "egress.json") }

// PortPath holds the loopback port the session also serves the sandbox on,
// for a backend that cannot reach its sockets (Backend.ServesPort).
func PortPath(root string) string { return filepath.Join(RunDir(root), "port") }

// HookSocket is the agent's hook socket in RunDir, carried to its provider's on the host.
const HookSocket = "hook.sock"

// Load reads root's record; ok=false when root has never had one.
func Load(root string) (Record, bool) {
	b, err := os.ReadFile(recordPath(root))
	if err != nil {
		return Record{}, false
	}
	var r Record
	if json.Unmarshal(b, &r) != nil || r.Root == "" {
		return Record{}, false
	}
	// A record can name a worktree as its repo. The guard finds the session's
	// git data only under the repo's own .git, so it would block every git
	// command; the clone the worktree belongs to is the repo it means.
	if clone, ok := worktreeClone(r.Repo); ok {
		r.Repo = clone
		_ = Save(r)
	}
	return r, true
}

// worktreeClone is the clone a linked worktree at dir belongs to, read from
// its .git file ("gitdir: <clone>/.git/worktrees/<name>"). ok is false for a
// clone, a folder that is gone, or anything else.
func worktreeClone(dir string) (string, bool) {
	if dir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(dir, ".git"))
	if err != nil {
		return "", false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return "", false
	}
	gitdir = filepath.Clean(strings.TrimSpace(gitdir))
	if filepath.Base(filepath.Dir(gitdir)) != "worktrees" {
		return "", false
	}
	common := filepath.Dir(filepath.Dir(gitdir))
	if filepath.Base(common) != ".git" {
		return "", false
	}
	return filepath.Dir(common), true
}

// Save writes the record, readable by the user only: it holds a token.
func Save(r Record) error {
	if r.Root == "" {
		return errors.New("sandbox record has no root")
	}
	dir := Dir(r.Root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	tmp := recordPath(r.Root) + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, recordPath(r.Root))
}

// Update applies edit to root's record and saves it.
func Update(root string, edit func(*Record)) (Record, error) {
	r, ok := Load(root)
	if !ok {
		return Record{}, errors.New("this workspace has no sandbox record")
	}
	edit(&r)
	return r, Save(r)
}

// Create records how root runs. An existing record keeps its token.
func Create(root, mode string) (Record, error) {
	if mode != ModeSandbox && mode != ModeHost {
		return Record{}, errors.New(`mode must be "sandbox" or "host"`)
	}
	r, ok := Load(root)
	if !ok {
		tok, err := newToken()
		if err != nil {
			return Record{}, err
		}
		r = Record{Root: filepath.Clean(root), AgentToken: tok, CreatedAt: time.Now().UTC()}
	}
	r.Mode = mode
	return r, Save(r)
}

// Enabled reports whether root's agent runs in the container.
func Enabled(root string) bool {
	r, ok := Load(root)
	return ok && r.Sandboxed()
}

// Managed reports whether root was ever launched sandboxed: its lock and
// manifest live under Dir from then on, even while escaped to the host.
func Managed(root string) bool {
	r, ok := Load(root)
	return ok && r.Mode == ModeSandbox
}

// Remove deletes root's host-side files, with the workspace.
func Remove(root string) error { return os.RemoveAll(Dir(root)) }

func newToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// StateDir is where oriel keeps a sandboxed session's own files (review
// state, launch record) instead of the workspace's .git/oriel, which the agent
// can write. ok=false for any other workspace.
func StateDir(root string) (string, bool) {
	if !Managed(root) {
		return "", false
	}
	return filepath.Join(Dir(root), "state"), true
}

// CreateWorktree records how a bare worktree of repo at root runs.
func CreateWorktree(root, repo, mode string) (Record, error) {
	r, err := Create(root, mode)
	if err != nil {
		return r, err
	}
	r.Repo = filepath.Clean(repo)
	return r, Save(r)
}
