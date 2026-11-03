package gio

import (
	"errors"
	"os/exec"
	"strings"
)

// osDark reads AppleInterfaceStyle, which is "Dark" in dark mode and unset in light.
func osDark() (dark, ok bool) {
	out, err := exec.Command("defaults", "read", "-g", "AppleInterfaceStyle").Output()
	if err != nil {
		// defaults exits non-zero when the key is unset: light.
		var ee *exec.ExitError
		return false, errors.As(err, &ee)
	}
	return strings.TrimSpace(string(out)) == "Dark", true
}

// appearanceSignals has no notification to listen to: gio passes on none, so the poll and refocus read it.
func appearanceSignals(func()) (stop func()) { return func() {} }
