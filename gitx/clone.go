package gitx

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CloneProgress clones url into dir, making its parents, and hands progress git's progress lines as they come.
// Git doesn't prompt for a password here: a clone that needs one fails, saying what git said.
func CloneProgress(ctx context.Context, url, dir string, progress func(line string)) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return err
	}
	args := []string{"clone", "--quiet"}
	if progress != nil {
		args = []string{"clone", "--progress"}
	}
	cmd := exec.CommandContext(ctx, "git", append(args, "--", url, dir)...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "GIT_TERMINAL_PROMPT=0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	var all bytes.Buffer
	sc := bufio.NewScanner(stderr)
	sc.Split(scanProgress)
	var last string
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		if progress != nil {
			progress(line)
		}
		if !strings.Contains(line, "%") {
			all.WriteString(line + "\n")
		}
		last = line
	}
	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("cloning %s: %w", url, ctx.Err())
		}
		msg := strings.TrimSpace(all.String())
		if msg == "" {
			msg = last
		}
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("cloning %s: %s", url, msg)
	}
	return nil
}

// scanProgress splits git's progress output at \r as well as \n.
func scanProgress(data []byte, atEOF bool) (int, []byte, error) {
	if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
		return i + 1, data[:i], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}
