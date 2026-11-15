package desktop

import (
	"context"
	"os/exec"
	"strings"

	"github.com/liamg/oriel/app/kit"
)

// ChooseDir asks macOS for a folder with its own chooser; it closes when ctx ends.
func ChooseDir(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", `POSIX path of (choose folder with prompt "Open Directory")`).Output()
	if ctx.Err() != nil {
		return "", nil
	}
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok && strings.Contains(string(e.Stderr), "-128") {
			return "", nil // cancelled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// ChooseDirAvailable says the OS has a folder chooser.
func ChooseDirAvailable() bool { return true }

// HasChooseDir is ChooseDirAvailable for a frame, through r.
func HasChooseDir(r *kit.Reads) bool { return true }
