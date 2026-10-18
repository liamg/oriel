package tour

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Bounds on a recorded command: a demo is a document, not a build server.
const (
	RunTimeout = 60 * time.Second
	RunMaxOut  = 200 << 10
)

// RunResult is what a recorded command did.
type RunResult struct {
	Cmd       string
	Cwd       string
	Output    string
	Exit      int
	Truncated bool
	TimedOut  bool
	RanAt     time.Time
}

// RunCommand runs argv in cwd, a directory inside the tour (its root when ""), and captures what it printed. A cwd
// outside the tour is refused. ORIEL_TOUR_ROOT is the tour's root, for a harness script to find the worktrees.
func RunCommand(ctx context.Context, root, cwd string, argv []string, timeout time.Duration) (RunResult, error) {
	if len(argv) == 0 {
		return RunResult{}, fmt.Errorf("nothing to run")
	}
	dir, err := runDir(root, cwd)
	if err != nil {
		return RunResult{}, err
	}
	if timeout <= 0 {
		timeout = RunTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "ORIEL_TOUR_ROOT="+root)
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	res := RunResult{Cmd: strings.Join(argv, " "), Cwd: cwd, RanAt: time.Now().UTC()}
	runErr := cmd.Run()
	res.TimedOut = ctx.Err() != nil
	switch {
	case cmd.ProcessState != nil:
		res.Exit = cmd.ProcessState.ExitCode()
	case runErr != nil:
		res.Exit = -1
	}
	out := buf.Bytes()
	if len(out) > RunMaxOut {
		out, res.Truncated = out[:RunMaxOut], true
	}
	res.Output = string(out)
	if runErr != nil && cmd.ProcessState == nil && !res.TimedOut {
		return res, fmt.Errorf("can't run %s: %w", argv[0], runErr)
	}
	return res, nil
}

func runDir(root, cwd string) (string, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if cwd == "" {
		return rootAbs, nil
	}
	if filepath.IsAbs(cwd) {
		return "", fmt.Errorf("cwd must be inside the tour")
	}
	abs := filepath.Join(rootAbs, filepath.Clean(cwd))
	if !inside(rootAbs, abs) {
		return "", fmt.Errorf("cwd must be inside the tour")
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("%s is not a directory in this tour", cwd)
	}
	return abs, nil
}

// Fence is the run block a result is recorded as in a step.
func (r RunResult) Fence() string {
	var sb strings.Builder
	out := strings.TrimRight(r.Output, "\n")
	fence := "```"
	for strings.Contains(out, fence) {
		fence += "`"
	}
	sb.WriteString(fence + "run")
	if r.Cwd != "" {
		fmt.Fprintf(&sb, " cwd=%s", quoteAttr(r.Cwd))
	}
	fmt.Fprintf(&sb, " cmd=%s", quoteAttr(r.Cmd))
	if r.Exit != 0 {
		fmt.Fprintf(&sb, " exit=%d", r.Exit)
	}
	fmt.Fprintf(&sb, " at=%s\n", r.RanAt.Format(time.RFC3339))
	if out != "" {
		sb.WriteString(out + "\n")
	}
	if r.Truncated {
		sb.WriteString("… output truncated at 200 KB\n")
	}
	if r.TimedOut {
		sb.WriteString("… killed after the time limit\n")
	}
	sb.WriteString(fence + "\n")
	return sb.String()
}

// Append adds a block to the end of a step, named by its file or its number.
func Append(root, step, text string) error {
	f := MatchStep(root, step)
	if f == "" {
		return fmt.Errorf("no step %q in this tour", step)
	}
	p := filepath.Join(root, f)
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strings.TrimRight(string(b), "\n")+"\n\n"+text), 0o644)
}

func quoteAttr(s string) string {
	if strings.ContainsAny(s, " \t\"'") {
		return `"` + strings.ReplaceAll(s, `"`, `'`) + `"`
	}
	return s
}
