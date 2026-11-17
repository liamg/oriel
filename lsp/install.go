package lsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// The tools Oriel installs servers with.
const (
	ToolNPM = "npm"
	ToolGo  = "go"
)

// Install is how Oriel installs a server it can't find: Tool (npm or go) installs Packages into a directory of the
// server's own, which Locate then looks in.
type Install struct {
	Tool     string
	Packages []string
}

func npm(pkgs ...string) *Install { return &Install{Tool: ToolNPM, Packages: pkgs} }

// String is the command a user would run to install it themselves, globally: "npm i -g typescript-language-server typescript".
func (i Install) String() string {
	if i.Tool == ToolGo {
		return "go install " + strings.Join(i.Packages, " ")
	}
	return "npm i -g " + strings.Join(i.Packages, " ")
}

// Can says whether this machine has the tool to install it with.
func (i Install) Can() bool {
	_, err := exec.LookPath(i.Tool)
	return err == nil
}

// Locate finds the spec's command for a project root: in the project's node_modules/.bin (the root's, or a
// directory's above it, as a workspace's is), then on PATH, then in the server's directory under installed (where
// Oriel installs it; none if ""). It returns the command's path; "" with ok for a spec that connects some other way.
func (s ServerSpec) Locate(root, installed string) (string, bool) {
	if s.Connect != nil {
		return "", true
	}
	if len(s.Cmd) == 0 {
		return "", false
	}
	name := s.Cmd[0]
	if filepath.IsAbs(name) {
		return name, executable(name)
	}
	for d := root; d != ""; {
		if p := filepath.Join(d, "node_modules", ".bin", name); executable(p) {
			return p, true
		}
		up := filepath.Dir(d)
		if up == d {
			break
		}
		d = up
	}
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	if installed == "" {
		return "", false
	}
	dir := s.InstallDir(installed)
	for _, p := range []string{filepath.Join(dir, "node_modules", ".bin", name), filepath.Join(dir, "bin", name)} {
		if executable(p) {
			return p, true
		}
	}
	return "", false
}

// InstallDir is the directory Oriel installs the server into, under installed.
func (s ServerSpec) InstallDir(installed string) string { return filepath.Join(installed, s.Name) }

// InstallInto installs the server into its directory under installed with its Install's tool, its output going to
// out. It fails when the spec has no Install or the tool isn't on this machine.
func (s ServerSpec) InstallInto(ctx context.Context, installed string, out *strings.Builder) error {
	if s.Install == nil {
		return fmt.Errorf("Oriel can't install %s itself", s.Name) //nolint:staticcheck // ST1005: it starts with a name
	}
	if !s.Install.Can() {
		return fmt.Errorf("installing %s needs %s, which isn't on this machine: %s", s.Name, s.Install.Tool, s.Install)
	}
	dir := s.InstallDir(installed)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch s.Install.Tool {
	case ToolNPM:
		cmd = exec.CommandContext(ctx, "npm", append([]string{"install", "--no-fund", "--no-audit", "--prefix", dir}, s.Install.Packages...)...)
	case ToolGo:
		cmd = exec.CommandContext(ctx, "go", append([]string{"install"}, s.Install.Packages...)...)
		cmd.Env = append(os.Environ(), "GOBIN="+filepath.Join(dir, "bin"))
	default:
		return fmt.Errorf("%s: no installer %q", s.Name, s.Install.Tool)
	}
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = out, out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("installing %s: %w: %s", s.Name, err, strings.TrimSpace(lastLines(out.String(), 5)))
	}
	if _, ok := s.Locate("", installed); !ok {
		return errors.New("installed " + s.Name + ", but " + s.Cmd[0] + " isn't where it should be")
	}
	return nil
}

func executable(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}
