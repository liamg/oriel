package claudebox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/agent/release"
)

// releases is where Claude Code's own installer downloads from.
const releases = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"

var semver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// Binary is the claude an agent at runs: under the OS's sandbox the host's own; in a container a Linux one, the
// host's when it is that and no other version is pinned, else a download of the version wanted, verified against
// the release manifest.
func (x *Box) Binary(ctx context.Context, at agent.SandboxPlace, pinned string) (string, error) {
	host, hostErr := hostClaude()
	if at.Shared {
		return host, hostErr
	}
	version := strings.TrimSpace(pinned)
	if version == "" || version == "host" {
		if hostErr != nil {
			return "", hostErr
		}
		if runtime.GOOS == at.OS && runtime.GOARCH == at.Arch {
			return host, nil
		}
		v, err := claudeVersion(host)
		if err != nil {
			return "", err
		}
		version = v
	}
	if !semver.MatchString(version) {
		return "", fmt.Errorf("%q is not a Claude Code version", version)
	}
	if hostErr == nil && runtime.GOOS == at.OS && runtime.GOARCH == at.Arch {
		if v, err := claudeVersion(host); err == nil && v == version {
			return host, nil
		}
	}
	return x.download(ctx, at, version)
}

// hostClaude is the host's claude binary, symlinks resolved.
func hostClaude() (string, error) {
	p, err := exec.LookPath("claude")
	if err != nil {
		return "", errors.New("claude is not installed on this machine")
	}
	return filepath.EvalSymlinks(p)
}

func claudeVersion(bin string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("%s --version: %w", bin, err)
	}
	f := strings.Fields(string(out))
	if len(f) == 0 || !semver.MatchString(f[0]) {
		return "", fmt.Errorf("cannot read the version from %q", strings.TrimSpace(string(out)))
	}
	return f[0], nil
}

func (x *Box) download(ctx context.Context, at agent.SandboxPlace, version string) (string, error) {
	x.downloads.Lock()
	defer x.downloads.Unlock()
	dst := filepath.Join(at.Cache, version, "claude")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if at.Report != nil {
		at.Report("Downloading Claude Code "+version+" for the sandbox", "Once for each version: later sessions reuse it.")
	}
	platform := "linux-x64"
	if at.Arch == "arm64" {
		platform = "linux-arm64"
	}
	var manifest struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
		} `json:"platforms"`
	}
	if err := release.GetJSON(ctx, x.HTTP, releases+"/"+version+"/manifest.json", nil, &manifest); err != nil {
		return "", err
	}
	want := manifest.Platforms[platform].Checksum
	if want == "" {
		return "", fmt.Errorf("claude code %s has no %s build", version, platform)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	return dst, release.DownloadVerified(ctx, x.HTTP, releases+"/"+version+"/"+platform+"/claude", want, dst, 0o755, maxBinary, "Claude Code")
}

// maxBinary bounds Claude Code's download.
const maxBinary = 1 << 30
