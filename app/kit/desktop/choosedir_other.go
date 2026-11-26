//go:build !darwin

package desktop

import (
	"context"
	"errors"
	"os/exec"
	"strings"

	"github.com/liamg/oriel/app/kit"
)

// chooser is how a desktop's chooser is run for a folder, a file to open and a file to save.
type chooser struct {
	name            string
	dir, file, save func(arg string) []string
}

// choosers are the desktop's choosers, tried in order: GNOME's and KDE's.
var choosers = []chooser{
	{"zenity",
		func(string) []string { return []string{"--file-selection", "--directory", "--title=Open Directory"} },
		func(prompt string) []string { return []string{"--file-selection", "--title=" + prompt} },
		func(name string) []string {
			return []string{"--file-selection", "--save", "--confirm-overwrite", "--title=Save as", "--filename=" + name}
		}},
	{"kdialog",
		func(string) []string { return []string{"--getexistingdirectory"} },
		func(prompt string) []string { return []string{"--title", prompt, "--getopenfilename"} },
		func(name string) []string { return []string{"--getsavefilename", name} }},
}

// ChooseDir asks the desktop for a folder with the first chooser it has; "" if cancelled or ctx ends.
func ChooseDir(ctx context.Context) (string, error) {
	return choose(ctx, func(c chooser) []string { return c.dir("") })
}

// ChooseFile asks the desktop for a file, headed prompt; "" if cancelled or ctx ends.
func ChooseFile(ctx context.Context, prompt string) (string, error) {
	return choose(ctx, func(c chooser) []string { return c.file(prompt) })
}

// ChooseSave asks the desktop where to save a file called name; "" if cancelled or ctx ends.
func ChooseSave(ctx context.Context, name string) (string, error) {
	return choose(ctx, func(c chooser) []string { return c.save(name) })
}

func choose(ctx context.Context, args func(chooser) []string) (string, error) {
	for _, c := range choosers {
		if _, err := exec.LookPath(c.name); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, c.name, args(c)...).Output()
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return "", nil // cancelled
		}
		return strings.TrimSpace(string(out)), err
	}
	return "", exec.ErrNotFound
}

// ChooseDirAvailable says the desktop has a chooser, for folders and files alike.
func ChooseDirAvailable() bool {
	for _, c := range choosers {
		if _, err := exec.LookPath(c.name); err == nil {
			return true
		}
	}
	return false
}

// HasChooseDir is ChooseDirAvailable for a frame, through r: the choosers are looked up off the loop.
func HasChooseDir(r *kit.Reads) bool {
	for _, c := range choosers {
		if r.Has(c.name) {
			return true
		}
	}
	return false
}
