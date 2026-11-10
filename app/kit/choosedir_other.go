//go:build !darwin

package kit

import (
	"context"
	"os/exec"
	"strings"
)

// dirChoosers are the desktop's folder choosers, tried in order: GNOME's and KDE's.
var dirChoosers = [][]string{
	{"zenity", "--file-selection", "--directory", "--title=Open Directory"},
	{"kdialog", "--getexistingdirectory"},
}

// ChooseDir asks the desktop for a folder with the first chooser it has; "" if cancelled or ctx ends.
func ChooseDir(ctx context.Context) (string, error) {
	for _, c := range dirChoosers {
		if _, err := exec.LookPath(c[0]); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, c[0], c[1:]...).Output()
		if _, ok := err.(*exec.ExitError); ok {
			return "", nil // cancelled
		}
		return strings.TrimSpace(string(out)), err
	}
	return "", exec.ErrNotFound
}

// ChooseDirAvailable says the desktop has a folder chooser.
func ChooseDirAvailable() bool {
	for _, c := range dirChoosers {
		if _, err := exec.LookPath(c[0]); err == nil {
			return true
		}
	}
	return false
}

// HasChooseDir is ChooseDirAvailable for a frame: the choosers are looked up off the loop.
func (r *Reads) HasChooseDir() bool {
	for _, c := range dirChoosers {
		if r.Has(c[0]) {
			return true
		}
	}
	return false
}
