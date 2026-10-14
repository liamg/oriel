package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const sshMarker = "# written by oriel"

// writeSSH gives the sandbox's home the host's known_hosts, which is not a
// secret, and an ssh config that goes out through `oriel ssh-proxy`. A config
// the agent wrote itself is left alone.
func writeSSH(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	home, _ := os.UserHomeDir()
	if b, err := os.ReadFile(filepath.Join(home, ".ssh", "known_hosts")); err == nil {
		if err := os.WriteFile(filepath.Join(dir, "known_hosts"), b, 0o600); err != nil {
			return err
		}
	}
	cfg := filepath.Join(dir, "config")
	if cur, err := os.ReadFile(cfg); err == nil && !strings.HasPrefix(string(cur), sshMarker) {
		return nil
	}
	body := sshMarker + "\nHost *\n  ProxyCommand oriel ssh-proxy %h %p\n"
	return os.WriteFile(cfg, []byte(body), 0o600)
}

// shimDir holds commands that stand in for ones with no credentials in a
// sandbox, first on the agent's PATH.
const shimDir = ".oriel-bin"

const ghShim = `#!/bin/sh
echo "oriel sandbox: gh has no credentials here. Use the oriel-gw gateway tools instead: oriel__pr_create to open the PR, oriel__ci for checks and logs, oriel__push to push, oriel__gh_* for tags, workflow runs, compares and repo variables, or github__* when the GitHub server is installed." >&2
exit 1
`

// writeShims gives the sandbox's home a gh that says what to use instead,
// and a git config with the host's identity and a credential helper that
// explains why there are no credentials. Files the agent wrote are kept.
func writeShims(home string) error {
	bin := filepath.Join(home, shimDir)
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(bin, "gh"), []byte(ghShim), 0o700); err != nil {
		return err
	}
	cfg := filepath.Join(home, ".gitconfig")
	if cur, err := os.ReadFile(cfg); err == nil && !strings.HasPrefix(string(cur), sshMarker) {
		return nil
	}
	body := sshMarker + "\n"
	if name, email := hostGitIdentity(); name != "" || email != "" {
		body += "[user]\n"
		if name != "" {
			body += "\tname = " + name + "\n"
		}
		if email != "" {
			body += "\temail = " + email + "\n"
		}
	}
	body += "[credential]\n\thelper = !oriel git-credential\n"
	return os.WriteFile(cfg, []byte(body), 0o600)
}

func hostGitIdentity() (name, email string) {
	get := func(key string) string {
		out, err := exec.Command("git", "config", "--global", "--get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
}
