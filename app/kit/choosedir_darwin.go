package kit

import (
	"os/exec"
	"strings"
)

// ChooseDir asks macOS for a folder with its own chooser.
func ChooseDir() (string, error) {
	out, err := exec.Command("osascript", "-e", `POSIX path of (choose folder with prompt "Open Directory")`).Output()
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

// HasChooseDir is ChooseDirAvailable for a frame.
func (r *Reads) HasChooseDir() bool { return true }
