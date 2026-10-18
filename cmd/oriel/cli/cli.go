package cli

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/codex"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/sandbox/agentcmd"
	"github.com/liamg/oriel/worker"
)

// orielHome is ~/.oriel: sessions, workspaces, repos.
func orielHome() string {
	if d := os.Getenv("ORIEL_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oriel")
}

// newWorker is the session host with the built-in providers and prompt packs.
func newWorker(home string, log *slog.Logger) (*worker.Worker, error) {
	providers := &agent.Providers{}
	providers.Register(claude.Provider{ProbeDir: filepath.Join(home, "probe")})
	providers.Register(&codex.Provider{})
	if _, err := packs.LoadDir(filepath.Join(app.DefaultConfigDir(), "agents")); err != nil {
		log.Warn("prompt packs", "err", err)
	}
	sandboxes := worker.NewSandboxFactory(nil)
	// A built-in kind's pack is the built-in one, or the user's version the app sends (prompts.set); agents/ adds kinds.
	builtinOr := func(name string) (agent.Pack, bool) {
		if src, ok := packs.Source(name); ok {
			p, err := packs.FromText(src, name)
			return p, err == nil
		}
		return packs.Get(name)
	}
	w, err := worker.New(worker.Options{Home: home, Providers: providers, Exec: hostexec.Exec{}, Sandbox: sandboxes, Packs: builtinOr, Log: log, Version: version})
	if err != nil {
		return nil, err
	}
	sandboxes.Attach(w)
	return w, nil
}

// startWorker starts the session host's background work until ctx ends: the machine's load, the plan windows'
// /usage probe, and the jobs' schedules.
func startWorker(ctx context.Context, w *worker.Worker, log *slog.Logger) {
	go w.SampleLoad(ctx, 5*time.Second)
	go func() {
		if err := w.ReadUsage(ctx); err != nil {
			log.Warn("usage", "err", err)
		}
	}()
	go w.RunSchedules(ctx, 30*time.Second)
}

// runHook is `oriel hook …`, which the agents' side of Oriel runs: Claude's mod, Codex's MCP server.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "mcp" {
		return codex.MCPMain(stdin, stdout, os.Environ())
	}
	return claude.HookMain(args, stdin, stdout, stderr)
}

// runSandboxed runs what a sandbox's container asks of the oriel binary inside it.
func runSandboxed(args []string) (bool, int) {
	handled, err := agentcmd.Main(args)
	if !handled {
		return false, 0
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "oriel:", err)
		return true, 1
	}
	return true, 0
}
