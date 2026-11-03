package gateway

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox/mcp"
)

// RunGH runs gh in dir with env added (the sandbox's git guard), for a
// Host's GH. The error carries gh's stderr, so a 404 says "HTTP 404".
func RunGH(ctx context.Context, dir string, env []string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("gh %s: %s", strings.Join(args, " "), strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

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
	var out tailBuffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	text := StripANSI(out.String())
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		return mcp.TextResult(fmt.Sprintf("Ran on the host and timed out after %s.\n\n%s", hostRunTimeout, text), true)
	case err != nil:
		return mcp.TextResult(fmt.Sprintf("Ran on the host and failed (%v).\n\n%s", err, text), true)
	}
	return mcp.TextResult("Ran on the host.\n\n"+text, false)
}

// tailBuffer keeps the last hostRunCap bytes written to it.
type tailBuffer struct {
	mu  sync.Mutex
	b   []byte
	cut bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > hostRunCap {
		t.b = t.b[len(t.b)-hostRunCap:]
		t.cut = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cut {
		return "…(earlier output dropped)\n" + string(t.b)
	}
	return string(t.b)
}
