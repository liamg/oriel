package container

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Engine is a docker-compatible CLI and the daemon it drives: Docker itself,
// or an alternative that answers to the same commands.
type Engine struct {
	// Bin is the CLI: a name on PATH or a path.
	Bin string
	// Host is the daemon's socket (docker -H); "" is the CLI's own choice.
	Host string
	// VM says the daemon runs in a VM, which can't connect to a unix socket
	// in a directory mounted from the host.
	VM bool
}

// Docker is the docker CLI with its current context.
var Docker = Engine{Bin: "docker"}

// Command is the argv that runs the CLI with args.
func (e Engine) Command(args ...string) []string {
	argv := []string{e.Bin}
	if e.Host != "" {
		argv = append(argv, "-H", "unix://"+e.Host)
	}
	return append(argv, args...)
}

// Installed reports whether the CLI can be found.
func (e Engine) Installed() bool {
	_, err := exec.LookPath(e.Bin)
	return err == nil
}

// Podman reports whether the CLI is Podman's.
func (e Engine) Podman() bool { return filepath.Base(e.Bin) == "podman" }

func (e Engine) cmd(ctx context.Context, args ...string) *exec.Cmd {
	argv := e.Command(args...)
	return exec.CommandContext(ctx, argv[0], argv[1:]...)
}

type engineKey struct{}

// WithEngine has the container calls under ctx use e.
func WithEngine(ctx context.Context, e Engine) context.Context {
	return context.WithValue(ctx, engineKey{}, e)
}

// EngineOf is the engine ctx carries; Docker if none.
func EngineOf(ctx context.Context) Engine {
	if e, ok := ctx.Value(engineKey{}).(Engine); ok {
		return e
	}
	return Docker
}

// Socket is the first of paths (relative ones under the home directory)
// that exists, or "".
func Socket(paths ...string) string {
	home, _ := os.UserHomeDir()
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			if home == "" {
				continue
			}
			p = filepath.Join(home, p)
		}
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// Within reports whether path, its links followed, lies under one of dirs
// (relative ones under the home directory).
func Within(path string, dirs ...string) bool {
	if r, err := filepath.EvalSymlinks(path); err == nil {
		path = r
	}
	home, _ := os.UserHomeDir()
	for _, d := range dirs {
		if !filepath.IsAbs(d) {
			if home == "" {
				continue
			}
			d = filepath.Join(home, d)
		}
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		if path == d || strings.HasPrefix(path, d+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
