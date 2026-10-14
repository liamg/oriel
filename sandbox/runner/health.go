package runner

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

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

// minDocker is the oldest daemon with everything a sandbox uses.
const minDocker = 20

var (
	buildMu  sync.Mutex
	building bool
	buildErr error
)

// Health checks this machine can run sandboxed agents, starting the base
// image build in the background if it is missing.
func Health(ctx context.Context, cfg sandbox.Config) []Problem {
	var out []Problem
	info, err := container.Ping(ctx)
	if err != nil {
		code, fix := "docker-down", "Start Docker (Docker Desktop, or `systemctl start docker`)."
		if strings.Contains(err.Error(), "not installed") {
			code, fix = "docker-missing", "Install Docker, or Podman with `ORIEL_DOCKER=podman`."
		}
		return append(out, Problem{Code: code, Message: err.Error(), Fix: fix, Blocking: true})
	}
	if major, _ := strconv.Atoi(strings.SplitN(info.Server, ".", 2)[0]); major > 0 && major < minDocker {
		out = append(out, Problem{Code: "docker-old", Blocking: true,
			Message: fmt.Sprintf("Docker %s is too old for sandboxes", info.Server),
			Fix:     fmt.Sprintf("Upgrade to Docker %d or newer.", minDocker)})
	}
	if cfg.Image == "" {
		if p, ok := baseImage(ctx); !ok {
			out = append(out, p)
		}
	}
	if ownAuth(os.Environ(), cfg) {
		if age, ok := sandbox.TokenAge(); ok && age > 330*24*time.Hour {
			out = append(out, Problem{Code: "claude-token-expiring",
				Message: "The sandbox's Claude token is nearly a year old and will expire soon",
				Fix:     "Run `claude setup-token` again and save the new token, or clear it with `oriel sandbox token --clear` to use this machine's own login."})
		}
	} else if login, ok := sandbox.HostLogin(); !ok {
		out = append(out, Problem{Code: "claude-token-missing", Blocking: true,
			Message: "Sandboxed agents have no way to sign in to Claude",
			Fix:     "Sign in with `claude` on this machine; sandboxes share that login."})
	} else {
		sandbox.SyncLogins(login)
		if time.Until(login.Expiry()) <= 0 {
			out = append(out, Problem{Code: "claude-login-stale",
				Message: "This machine's Claude login needs refreshing before sandboxed agents can use it",
				Fix:     "Run `claude` on this machine once; sandboxes pick the fresh login up within a minute."})
		}
	}
	if runtime.GOOS != "linux" && os.Getenv("ORIEL_SANDBOX_BIN") == "" {
		if p, ok := linuxOrielHealth(); !ok {
			out = append(out, p)
		}
	}
	for _, p := range PendingDockerfiles(cfg) {
		out = append(out, Problem{Code: "dockerfile-pending", Pending: &p,
			Message: p.Repo + " has a sandbox Dockerfile waiting for your approval; its sessions use the base image until then",
			Fix:     "Read it and approve it in Settings → Sandbox."})
	}
	return out
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

// baseImage is ok when the base image exists; otherwise it starts the build
// and reports it.
func baseImage(ctx context.Context) (Problem, bool) {
	tag := container.BaseImage()
	if container.ImageExists(ctx, tag) {
		return Problem{}, true
	}
	buildMu.Lock()
	defer buildMu.Unlock()
	if !building {
		building, buildErr = true, nil
		go func() {
			bctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			err := container.EnsureImage(bctx, tag, container.BaseDockerfile)
			buildMu.Lock()
			building, buildErr = false, err
			buildMu.Unlock()
		}()
	}
	if buildErr != nil {
		return Problem{Code: "image-failed", Blocking: true, Message: "Building the sandbox image failed: " + buildErr.Error(),
			Fix: "Check Docker can pull debian:bookworm-slim; Oriel retries on the next check."}, false
	}
	return Problem{Code: "image-building", Message: "Building the sandbox base image — the first sandboxed launch waits for it"}, false
}

// ownAuth reports credentials a sandbox is handed outright, which come before
// the host's login: a setup token, an API key or a key helper.
func ownAuth(environ []string, cfg sandbox.Config) bool {
	if sandbox.ClaudeToken() != "" {
		return true
	}
	env := sandbox.FilterEnv(environ, cfg.Env)
	if env["ANTHROPIC_API_KEY"] != "" || env["ANTHROPIC_AUTH_TOKEN"] != "" {
		return true
	}
	helper, _ := sandbox.ClaudeSettings()["apiKeyHelper"].(string)
	return strings.TrimSpace(helper) != ""
}
