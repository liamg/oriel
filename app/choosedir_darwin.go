package app

import (
	"os/exec"
	"strings"
)

// chooseDir asks macOS for a folder with its own chooser.
func chooseDir() (string, error) {
	out, err := exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Open Directory")`).Output()
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok && strings.Contains(string(e.Stderr), "-128") {
			return "", nil // cancelled
		}
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func chooseDirAvailable() bool { return true }
