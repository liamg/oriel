package gateway

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"time"

	"github.com/liamg/oriel/internal/tailbuf"
	"github.com/liamg/oriel/sandbox/mcp"
)

// ansi matches terminal escape sequences, stripped from output the agent reads.
var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07|\x1b[()][A-Z0-9]`)

// StripANSI removes terminal escapes, for output the agent reads.
func StripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// hostRunTimeout and hostRunCap bound a Run on host.
const (
	hostRunTimeout = 10 * time.Minute
	hostRunCap     = 64 * 1024
)

// hostShell is the shell a host command runs in: the agent writes POSIX sh,
// which the user's $SHELL need not be, and it runs without a terminal.
func hostShell() string {
	if p, err := exec.LookPath("bash"); err == nil {
		return p
	}
	return "/bin/sh"
}

// HostRun runs a command the user approved on the host in dir, with their
// full environment, and returns its output for the agent.
func HostRun(ctx context.Context, dir, command string) mcp.CallResult {
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return mcp.TextResult("no such directory on the host: "+dir, true)
	}
	ctx, cancel := context.WithTimeout(ctx, hostRunTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, hostShell(), "-c", command)
	cmd.Dir = dir
	out := &tailbuf.Buffer{Max: hostRunCap}
	cmd.Stdout, cmd.Stderr = out, out
	err := cmd.Run()
	text := StripANSI(out.String())
	if out.Cut() {
		text = "…(earlier output dropped)\n" + text
	}
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return mcp.TextResult(fmt.Sprintf("Ran on the host and timed out after %s.\n\n%s", hostRunTimeout, text), true)
	case err != nil:
		return mcp.TextResult(fmt.Sprintf("Ran on the host and failed (%v).\n\n%s", err, text), true)
	}
	return mcp.TextResult("Ran on the host.\n\n"+text, false)
}
