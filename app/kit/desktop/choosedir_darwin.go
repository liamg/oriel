package desktop

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/liamg/oriel/app/kit"
)

// ChooseDir asks macOS for a folder with its own chooser; it closes when ctx ends.
func ChooseDir(ctx context.Context) (string, error) {
	return choose(ctx, `POSIX path of (choose folder with prompt "Open Directory")`)
}

// ChooseFile asks macOS for a file with its own chooser, headed prompt; "" if cancelled or ctx ends.
func ChooseFile(ctx context.Context, prompt string) (string, error) {
	return choose(ctx, `POSIX path of (choose file with prompt `+quote(prompt)+`)`)
}

// ChooseSave asks macOS where to save a file called name, starting in Downloads; "" if cancelled or ctx ends.
func ChooseSave(ctx context.Context, name string) (string, error) {
	return choose(ctx, `POSIX path of (choose file name with prompt "Save as" default name `+quote(name)+` default location (path to downloads folder))`)
}

func choose(ctx context.Context, script string) (string, error) {
	out, err := exec.CommandContext(ctx, "osascript", "-e", script).Output()
	if ctx.Err() != nil {
		return "", nil
	}
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && strings.Contains(string(exit.Stderr), "-128") {
			return "", nil // cancelled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// quote is s as an AppleScript string.
func quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// ChooseDirAvailable says the OS has a folder chooser, and so a file chooser.
func ChooseDirAvailable() bool { return true }

// HasChooseDir is ChooseDirAvailable for a frame, through r.
func HasChooseDir(r *kit.Reads) bool { return true }
