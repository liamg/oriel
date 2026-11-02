// Package ossandbox confines a plugin's process with the OS: Landlock and a
// seccomp filter on Linux, a sandbox-exec profile on macOS. A confined process reads, writes and runs only what its
// Profile lists, beyond what any program needs from the system, and has no network unless it lists ports.
//
// On Linux the confinement is applied by this binary re-run as a helper, which locks itself down and then execs the
// program: a program that confines processes calls Main first thing in main (and in TestMain).
//
// Landlock and Seatbelt filter TCP by port, not by host: a profile's ports are what the OS enforces, and a net
// grant's hosts are enforced only where traffic goes through the egress proxy (a session's sandbox).
package ossandbox

import (
	"debug/elf"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

// Profile is what a confined process may touch. Paths are absolute.
type Profile struct {
	Read  []string `json:"read,omitempty"`  // read (files, and directories with everything under them)
	Write []string `json:"write,omitempty"` // read, write, create and remove under
	Exec  []string `json:"exec,omitempty"`  // programs it may run; each one's interpreter comes with it
	Ports []int    `json:"ports,omitempty"` // TCP ports it may connect to; with none it has no network at all
}

// ErrUnavailable is a platform that can't confine a process: an old kernel, an OS with neither mechanism.
var ErrUnavailable = errors.New("ossandbox: this system can't confine a process")

// flagProfile starts the helper's first argument, the profile after it.
const flagProfile = "--oriel-confine="

// Command is name with args, to run confined to p. Its environment, directory and files are the caller's to set,
// as with exec.Command; the caller's Path and Args are the confinement's.
func Command(p Profile, name string, args ...string) (*exec.Cmd, error) {
	if err := Available(); err != nil {
		return nil, err
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return nil, err
	}
	if path, err = filepath.Abs(path); err != nil {
		return nil, err
	}
	p.Exec = append(slices.Clone(p.Exec), path)
	return command(p, path, args)
}

// interpreters are what running path also runs: an ELF's dynamic loader, or a script's #! interpreter (and,
// through /usr/bin/env, the program it names).
func interpreters(path string) []string {
	if f, err := elf.Open(path); err == nil {
		defer f.Close()
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP {
				b := make([]byte, p.Filesz)
				if _, err := p.ReadAt(b, 0); err == nil {
					return []string{strings.TrimRight(string(b), "\x00")}
				}
			}
		}
		return nil
	}
	b := make([]byte, 256)
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	n, _ := f.Read(b)
	f.Close()
	line, ok := strings.CutPrefix(string(b[:n]), "#!")
	if !ok {
		return nil
	}
	line, _, _ = strings.Cut(line, "\n")
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return nil
	}
	out := []string{fields[0]}
	if filepath.Base(fields[0]) == "env" && len(fields) > 1 {
		if p, err := exec.LookPath(fields[1]); err == nil {
			out = append(out, p)
		}
	}
	for _, o := range slices.Clone(out) {
		out = append(out, interpreters(o)...)
	}
	return out
}

// runs is every program p.Exec lets run: each listed one, its interpreters, and their real paths.
func runs(p Profile) []string {
	var out []string
	add := func(s string) {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
		if r, err := filepath.EvalSymlinks(s); err == nil && !slices.Contains(out, r) {
			out = append(out, r)
		}
	}
	for _, e := range p.Exec {
		for _, i := range append([]string{e}, interpreters(e)...) {
			add(i)
			if _, err := os.Lstat(shVariant); err == nil && i == "/bin/sh" {
				add(shVariant)
			}
		}
	}
	return out
}

// shVariant is the shell macOS's /bin/sh runs, as a link to it.
const shVariant = "/private/var/select/sh"

func encode(p Profile) string {
	b, _ := json.Marshal(p)
	return string(b)
}
