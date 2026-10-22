package plugin

import (
	"io"
	"io/fs"
)

// Bundled is a plugin shipped inside oriel's binary: its files, and the mains its
// process binaries run as, so it needs no install and no executable per platform.
type Bundled struct {
	FS fs.FS
	// Processes name the Main each of its binaries runs, by the path its Lua spawns it by: "bin/oriel-linear": "linear".
	Processes map[string]string
}

// Main is a process plugin's main, which `oriel plugin-process <name>` runs over its stdin and stdout.
type Main func(stdin io.Reader, stdout io.Writer) error

// BundledPrograms are b's binaries as oriel itself runs them: self (oriel's own command) then plugin-process and
// the main's name, for Caller.Programs.
func BundledPrograms(b Bundled, self []string) map[string][]string {
	out := map[string][]string{}
	for bin, main := range b.Processes {
		out[bin] = append(append([]string(nil), self...), "plugin-process", main)
	}
	return out
}
