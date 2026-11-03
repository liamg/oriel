package cli

import (
	"context"
	"fmt"
	"github.com/liamg/oriel/app/agents"
	"github.com/liamg/oriel/plugin"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/claude"
	"github.com/liamg/oriel/agent/hostexec"
	"github.com/liamg/oriel/agent/mcptools/mcpstdio"
	"github.com/liamg/oriel/agent/packs"
	"github.com/liamg/oriel/app"
	"github.com/liamg/oriel/host"
	"github.com/liamg/oriel/host/sysload"
	"github.com/liamg/oriel/sandbox/agentcmd"
)

// orielHome is ~/.oriel: sessions, workspaces, repos.
func orielHome() string {
	if d := os.Getenv("ORIEL_HOME"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".oriel")
}

// machineLoad reads the machine's load in-process, only while it's wanted, and says when it moves.
var machineLoad = sysload.NewSampler(5*time.Second, nil)

// newHost is the session host with the built-in prompt packs. Its agent providers are the app's (app/agents).
func newHost(home string, log *slog.Logger) (*host.Host, error) {
	kinds := &packs.Set{}
	if _, err := kinds.LoadDir(filepath.Join(app.DefaultConfigDir(), "agents")); err != nil {
		log.Warn("prompt packs", "err", err)
	}
	sandboxes := host.NewSandboxFactory(nil)
	// A built-in kind's pack is the built-in one, or the user's version the app sends (prompts.set); agents/ adds kinds.
	builtinOr := func(name string) (agent.Pack, bool) {
		if src, ok := packs.Source(name); ok {
			p, err := packs.FromText(src, name)
			return p, err == nil
		}
		return kinds.Get(name)
	}
	w, err := host.New(host.Options{Home: home, Exec: hostexec.Exec{}, Sandbox: sandboxes, Packs: builtinOr, Log: log, Version: version,
		Load: machineLoad.Last})
	if err != nil {
		return nil, err
	}
	sandboxes.Attach(w)
	return w, nil
}

// builtinAgents are the built-in agent providers' plugins: Claude's /usage probe runs in home's probe/.
func builtinAgents(home string) []plugin.Plugin { return agents.Builtins(filepath.Join(home, "probe")) }

// startHost starts the session host's background work until ctx ends: the machine's load, the sandbox sweep, the
// jobs' schedules and the prices. The plan windows are read once the app gives the host its providers.
func startHost(ctx context.Context, w *host.Host, log *slog.Logger) {
	go machineLoad.Run(ctx)
	go w.SampleLoad(ctx, machineLoad)
	go w.SweepSandboxes(ctx, time.Hour)
	go w.RunSchedules(ctx, 30*time.Second)
	go w.RefreshPrices(ctx)
}

// runHook is `oriel hook …`, which the agents' side of Oriel runs: Claude's mod, the MCP server of Oriel's tools.
func runHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 1 && args[0] == "mcp" {
		return mcpstdio.Main(stdin, stdout, stderr, os.Environ())
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
