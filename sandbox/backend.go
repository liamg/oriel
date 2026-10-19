package sandbox

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
)

// Backend confines an agent to its workspace: a container (docker, or an
// alternative engine such as orbstack), or the OS's own sandbox (seatbelt on
// macOS). Every backend gets the same egress
// proxy, gateway and session sockets; it decides only how the agent's
// process is fenced in.
type Backend interface {
	// Name is the backend's name in sandbox.backend.
	Name() string
	// Launch readies root's sandbox for an agent in cwd running argv, and
	// returns the command to run under the PTY in its place. A slow step says
	// what it's doing with Report(ctx, …).
	Launch(ctx context.Context, root, cwd string, argv []string, o LaunchOptions) (Command, error)
	// Stop ends what Launch started for root. The workspace is untouched.
	Stop(ctx context.Context, root string) error
	// Available says whether this machine can run it at all: its OS, its tools installed.
	Available() error
	// Health is what stops sandboxed agents on this machine, with its fixes.
	Health(ctx context.Context, cfg Config) []Problem
	// Sweep removes what idle sandboxes left behind; attached reports whether
	// root's agent is still running. It returns what it removed.
	Sweep(ctx context.Context, attached func(root string) bool) []string
	// AgentRunDir is where the agent finds RunDir(root).
	AgentRunDir(root string) string
	// ServesPort reports whether the agent can't reach its session's sockets,
	// so the session must also listen on the port in PortPath(root).
	ServesPort() bool
}

// Command is a process to start: the agent in its sandbox.
type Command struct {
	Argv []string
	Env  []string
	Dir  string
}

// LaunchOptions are the host facts a launch needs.
type LaunchOptions struct {
	// ModDir is the oriel mod, readable for --plugin-dir.
	ModDir string
	// Environ is the environment the sandbox's allowlist picks from.
	Environ []string
	// Extra env the agent always gets (ORIEL_SPAWNED and friends).
	Extra map[string]string
	// Config is the sandbox's settings.
	Config Config
	// ClaudeDefaults are settings the sandbox's Claude gets where the host's
	// settings leave them out.
	ClaudeDefaults map[string]any
}

// Problem is something stopping sandboxed agents on this machine, with what
// fixes it.
type Problem struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Fix     string   `json:"fix,omitempty"`
	Pending *Pending `json:"pending,omitempty"`
	// Blocking problems stop sandboxed launches; the rest are warnings.
	Blocking bool `json:"blocking"`
}

// Pending is a project's sandbox Dockerfile waiting for the user's approval.
type Pending struct {
	Repo       string `json:"repo"`
	Sum        string `json:"sum"`
	Dockerfile string `json:"dockerfile"`
}

// Blocking reports the first problem that stops a sandboxed launch.
func Blocking(ps []Problem) (Problem, bool) {
	for _, p := range ps {
		if p.Blocking {
			return p, true
		}
	}
	return Problem{}, false
}

// Preference orders the backends for DefaultBackend; those it leaves out
// come after, by name.
var Preference = []string{"docker", "orbstack", "colima", "podman", "seatbelt"}

// DefaultBackend runs sandboxes when sandbox.backend names none: the first
// this machine can run, or docker when it can run none.
func DefaultBackend() string {
	if a := AvailableBackends(); len(a) > 0 {
		return a[0]
	}
	return "docker"
}

var (
	backendsMu sync.Mutex
	backends   = map[string]Backend{}
)

// Register makes b available to sandbox.backend under its name, replacing
// any backend of that name.
func Register(b Backend) {
	backendsMu.Lock()
	defer backendsMu.Unlock()
	backends[b.Name()] = b
}

// Backends are the registered backends' names, sorted.
func Backends() []string {
	backendsMu.Lock()
	defer backendsMu.Unlock()
	out := make([]string, 0, len(backends))
	for name := range backends {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// AvailableBackends are the names of the registered backends this machine
// can run, in Preference order.
func AvailableBackends() []string {
	var out []string
	for _, name := range byPreference(Backends()) {
		backendsMu.Lock()
		b := backends[name]
		backendsMu.Unlock()
		if b.Available() == nil {
			out = append(out, name)
		}
	}
	return out
}

func byPreference(names []string) []string {
	rank := func(n string) int {
		if i := slices.Index(Preference, n); i >= 0 {
			return i
		}
		return len(Preference)
	}
	slices.SortStableFunc(names, func(a, b string) int { return rank(a) - rank(b) })
	return names
}

// BackendFor is the backend cfg names, or DefaultBackend.
func BackendFor(cfg Config) (Backend, error) {
	name := cfg.Backend
	if name == "" {
		name = DefaultBackend()
	}
	backendsMu.Lock()
	b, ok := backends[name]
	backendsMu.Unlock()
	if !ok {
		return nil, fmt.Errorf("no sandbox backend %q (have %s)", name, strings.Join(Backends(), ", "))
	}
	return b, nil
}
