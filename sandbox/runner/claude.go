package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/liamg/oriel/sandbox"
)

// releases is where Claude Code's own installer downloads from.
const releases = "https://storage.googleapis.com/claude-code-dist-86c565f3-f756-42ad-8dfa-d59b1c096819/claude-code-releases"

var semver = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

// ClaudeBinary is the Linux claude the sandbox runs: the host's own when the
// host is Linux and no other version is pinned, otherwise a download of the
// version wanted, verified against the release manifest.
func ClaudeBinary(ctx context.Context, pinned string) (string, error) {
	version := strings.TrimSpace(pinned)
	if version == "" || version == "host" {
		host, err := hostClaude()
		if err != nil {
			return "", err
		}
		if runtime.GOOS == "linux" {
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
	if runtime.GOOS == "linux" {
		if host, err := hostClaude(); err == nil {
			if v, err := claudeVersion(host); err == nil && v == version {
				return host, nil
			}
		}
	}
	return downloadClaude(ctx, version)
}

// DownloadedClaude is where a version's Linux binary is kept.
func DownloadedClaude(version string) string {
	return filepath.Join(sandbox.Base(), "claude", version, "claude")
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

var downloadMu sync.Mutex

func downloadClaude(ctx context.Context, version string) (string, error) {
	downloadMu.Lock()
	defer downloadMu.Unlock()
	dst := DownloadedClaude(version)
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	sandbox.Report(ctx, sandbox.Progress{Step: "Downloading Claude Code " + version + " for the sandbox",
		Note: "Once for each version: later sessions reuse it."})
	platform := "linux-x64"
	if runtime.GOARCH == "arm64" {
		platform = "linux-arm64"
	}
	var manifest struct {
		Platforms map[string]struct {
			Checksum string `json:"checksum"`
		} `json:"platforms"`
	}
	if err := getJSON(ctx, releases+"/"+version+"/manifest.json", &manifest); err != nil {
		return "", err
	}
	want := manifest.Platforms[platform].Checksum
	if want == "" {
		return "", fmt.Errorf("claude code %s has no %s build", version, platform)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	if err := fetchVerified(ctx, releases+"/"+version+"/"+platform+"/claude", tmp, want); err != nil {
		_ = os.Remove(tmp)
		return "", err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func fetchVerified(ctx context.Context, url, dst, sum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		return fmt.Errorf("the Claude Code download does not match its checksum")
	}
	return nil
}
