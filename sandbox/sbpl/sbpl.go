// Package sbpl is what macOS's Seatbelt confiners share: sandbox-exec, the strings their profiles are written in, and
// paths as Seatbelt matches them. sandbox/ossandbox confines plugin processes with it, sandbox/seatbelt agents.
package sbpl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SandboxExec is the program that runs a process under a Seatbelt profile.
const SandboxExec = "/usr/bin/sandbox-exec"

// Available says whether this machine has sandbox-exec.
func Available() error {
	if _, err := os.Stat(SandboxExec); err != nil {
		return fmt.Errorf("no %s", SandboxExec)
	}
	return nil
}

// Quote is a Seatbelt string: in double quotes, with \ and " escaped.
func Quote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// Real is p with its symlinks followed, as Seatbelt matches paths (/tmp is /private/tmp); p itself if they can't be.
func Real(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}
