package sandbox

import (
	"cmp"
	"context"
	"fmt"
	"strings"

	"github.com/liamg/oriel/agent"
)

// HelperRunner is a Backend whose sandboxes run the app's sandbox helper (package sandbox/helper's: a static Linux
// oriel), handed to it by the host that holds it.
type HelperRunner interface {
	// WithHelper is the backend running helper's binary.
	WithHelper(helper func() ([]byte, error)) Backend
}

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
	Launch(ctx context.Context, root, cwd string, argv []string, o LaunchOpts) (Command, error)
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

// Describer is a backend that can say what it runs root's agent in.
type Describer interface {
	Describe(ctx context.Context, root string, cfg Config) Description
}

// Rebuilder is a backend whose image for root can be built again, its downloads fetched afresh.
type Rebuilder interface {
	Rebuild(ctx context.Context, root string, cfg Config) error
}

// Sheller is a backend that can run a shell in root's sandbox beside its agent, in cwd: what a terminal runs.
type Sheller interface {
	Shell(ctx context.Context, root, cwd string, o LaunchOpts) (Command, error)
}

// ShellArgv is the shell a sandbox's terminal runs: bash where the image has it, else sh.
var ShellArgv = []string{"sh", "-c", "command -v bash >/dev/null && exec bash -l || exec sh -l"}

// Description is what a backend runs an agent in: for a container engine, the container and its image.
type Description struct {
	Container string
	Running   bool
	Image     string
	// ImageFrom says where the image comes from: "the base image", "sandbox.image", or a repo's Dockerfile.
	ImageFrom string
	// Dockerfile is the image's source; "" for an image named in settings.
	Dockerfile string
	Built      bool
	// BuildLog is the file the image's last build wrote to; "" if it hasn't been built here.
	BuildLog string
	// Mounts are what the running container sees of the host.
	Mounts []Bind
	// Places are where the agent finds the host's paths that aren't at the same path inside, by host path.
	Places map[string]string
}

// Command is a process to start: the agent in its sandbox, and what its provider said it needs there.
type Command struct {
	Argv  []string
	Env   []string
	Dir   string
	Needs agent.SandboxNeeds
}

// LaunchOpts are the host facts a launch needs.
type LaunchOpts struct {
	// Environ is the environment the sandbox's allowlist picks from.
	Environ []string
	// Extra env the agent always gets (ORIEL_SPAWNED and friends).
	Extra map[string]string
	// Config is the sandbox's settings.
	Config Config
	// Provider names the agent's provider, and its directory in the sandbox; Agent is what says what it needs
	// there, nil for an agent that needs nothing past the workspace.
	Provider string
	Agent    agent.Sandboxed
	// StateDir is the session's directory for its provider (agent.StartSpec.StateDir).
	StateDir string
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

// Backends are the backends a host can run sandboxes in, the most preferred first.
type Backends []Backend

// Names are every backend's name, in order.
func (bs Backends) Names() []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.Name())
	}
	return out
}

// Available are the names of the backends this machine can run, in order.
func (bs Backends) Available() []string {
	var out []string
	for _, b := range bs {
		if b.Available() == nil {
			out = append(out, b.Name())
		}
	}
	return out
}

// Default runs sandboxes when sandbox.backend names none: the first this machine can run, else the first.
func (bs Backends) Default() string {
	if a := bs.Available(); len(a) > 0 {
		return a[0]
	}
	if len(bs) > 0 {
		return bs[0].Name()
	}
	return ""
}

// For is the backend cfg names, or the Default.
func (bs Backends) For(cfg Config) (Backend, error) {
	name := cmp.Or(cfg.Backend, bs.Default())
	for _, b := range bs {
		if b.Name() == name {
			return b, nil
		}
	}
	return nil, fmt.Errorf("no sandbox backend %q (have %s)", name, strings.Join(bs.Names(), ", "))
}
