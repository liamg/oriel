package gio

import (
	"os/exec"
	"strings"
)

// osDark asks the desktop portal's org.freedesktop.appearance color-scheme (1 prefers dark, 2 light), then GNOME's
// color-scheme setting.
func osDark() (dark, ok bool) {
	out, err := exec.Command("gdbus", "call", "--session", "--dest", "org.freedesktop.portal.Desktop",
		"--object-path", "/org/freedesktop/portal/desktop", "--method", "org.freedesktop.portal.Settings.Read",
		"org.freedesktop.appearance", "color-scheme").Output()
	if err == nil {
		switch s := string(out); {
		case strings.Contains(s, "uint32 1"):
			return true, true
		case strings.Contains(s, "uint32 2"):
			return false, true
		}
	}
	out, err = exec.Command("gsettings", "get", "org.gnome.desktop.interface", "color-scheme").Output()
	if err != nil {
		return false, false
	}
	switch strings.Trim(strings.TrimSpace(string(out)), "'") {
	case "prefer-dark":
		return true, true
	case "prefer-light":
		return false, true
	}
	return false, false
}
