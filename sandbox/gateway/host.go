package gateway

import (
	"context"
	"errors"
	"time"

	"github.com/liamg/oriel/sandbox/mcp"
)

// Host runs the gateway's own tools on the host, with the user's credentials.
// The worker implements it for one session. project names a project of the
// session's workspace, "" for the primary one; an unknown name is an error
// wrapping ErrNoProject (`no such project "x"`).
type Host interface {
	// Approve puts a call in front of the user: the gateway approval card.
	// It returns when they answer or ctx ends.
	Approve(ctx context.Context, r Request) (Answer, error)

	// Commit commits files in project with the user's git identity and
	// signing, every changed file when files is empty, and says how many.
	Commit(ctx context.Context, project, message string, files []string) (int, error)
	// Push, Pull and Fetch run against origin on the host. Push and Pull
	// refuse a project whose worktree is not on the workspace's own branch.
	Push(ctx context.Context, project string) error
	Pull(ctx context.Context, project string) error
	Fetch(ctx context.Context, project string) error
	// CreatePR opens the pull request and returns its URL. An empty title
	// means the saved PR draft; with none saved, ErrNoDraft. ErrVerify means
	// the user has to verify the change first.
	CreatePR(ctx context.Context, project, title, body string) (string, error)
	// GH runs the host's gh in project's checkout and returns its stdout;
	// the error carries gh's stderr (RunGH does both).
	GH(ctx context.Context, project string, args ...string) (string, error)
	// Repos are the user's repositories that could join the workspace.
	Repos(ctx context.Context) ([]Repo, error)
	// AddProject brings the repository at a host path into the workspace,
	// installs its toolchains, and restarts the agent once its turn ends.
	AddProject(ctx context.Context, repo string) (Added, error)
	// RunOnHost runs a command the user approved on the host; cwd is the
	// workspace root by default, relative to it, or absolute. HostRun is the
	// usual implementation, once the directory is resolved.
	RunOnHost(ctx context.Context, command, cwd string) mcp.CallResult
	// RunTail is the end of the terminal the user ran a command in themselves.
	RunTail() string
	// SSHAgent opens the sandbox's ssh-agent socket for d (at most five
	// minutes) and says until when.
	SSHAgent(ctx context.Context, d time.Duration) (time.Time, error)
	// RequestHost asks the user to let the session leave the sandbox. ok is
	// the outcome; message is for the agent.
	RequestHost(ctx context.Context, why string) (ok bool, message string)
}

// Repo is a repository on the host that could join the workspace.
type Repo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// Added is a project AddProject brought in.
type Added struct {
	Name string
	Dir  string
	// Tools are the toolchains installed for it; ToolsErr is why they were not.
	Tools    []string
	ToolsErr error
}

var (
	// ErrNoProject is wrapped by a Host error naming a project that is not
	// in the workspace.
	ErrNoProject = errors.New("no such project")
	// ErrNoDraft is CreatePR with no title and no saved draft.
	ErrNoDraft = errors.New("no title given and no PR draft saved: save one with mcp__oriel__pr_draft first")
	// ErrVerify is CreatePR on a change the user has not verified yet.
	ErrVerify = errors.New("the change is not verified yet")
)
