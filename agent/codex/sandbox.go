package codex

import (
	"archive/tar"
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/internal/atomicfile"
	"github.com/liamg/oriel/toml"
)

var _ agent.Sandboxed = (*Provider)(nil)

// githubAPI is where the Codex releases are listed, with each archive's digest.
const githubAPI = "https://api.github.com/repos/openai/codex"

// passEnv is the host environment Codex's agent inherits by default: its endpoint and key, and its own settings.
var passEnv = []string{"OPENAI_*", "CODEX_*", "RUST_LOG"}

// skipEnv are what a Codex session sets for its own processes, and the home a sandbox gives its own.
var skipEnv = []string{"CODEX_HOME", "CODEX_SANDBOX*", "CODEX_THREAD_ID", "CODEX_CI", "CODEX_MANAGED_BY_*"}

// hosts are what Codex reaches signed in with ChatGPT or an API key, and where it refreshes its login.
var hosts = []string{"chatgpt.com", "*.chatgpt.com", "api.openai.com", "auth.openai.com"}

// triples are the release's Linux targets by GOARCH.
var triples = map[string]string{"amd64": "x86_64-unknown-linux-musl", "arm64": "aarch64-unknown-linux-musl"}

// SandboxNeeds are codex in the sandbox: its binary for the sandbox's platform, a CODEX_HOME of its own (at.Dir)
// with the host's sign-in, stripped of its refresh token so the sandbox never rotates the host's, and the user's
// AGENTS.md, prompts and skills read-only.
func (p *Provider) SandboxNeeds(ctx context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	n := agent.SandboxNeeds{PassEnv: passEnv, SkipEnv: skipEnv, URLEnv: []string{"OPENAI_BASE_URL"}, Hosts: hosts,
		Env: map[string]string{"CODEX_HOME": at.Dir}}
	if n.Problems = p.sandboxProblems(at); slices.ContainsFunc(n.Problems, func(x agent.SandboxProblem) bool { return x.Blocking }) {
		return n, nil
	}
	bin, err := p.sandboxBinary(ctx, at)
	if err != nil {
		return n, err
	}
	n.Bins = map[string]string{"codex": bin}
	if err := syncLogin(home(), at.Dir); err != nil {
		return n, err
	}
	dir := home()
	for _, f := range userFiles {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			n.Mounts = append(n.Mounts, agent.Mount{Host: filepath.Join(dir, f), Sandbox: filepath.Join(at.Dir, f), ReadOnly: true})
		}
	}
	return n, nil
}

// sandboxProblems are what stops a sandboxed codex at at: not installed where the OS's sandbox runs the host's, or
// signed in nowhere a sandbox can read.
func (p *Provider) sandboxProblems(at agent.SandboxPlace) []agent.SandboxProblem {
	var out []agent.SandboxProblem
	if at.Shared {
		if _, err := exec.LookPath(p.bin()); err != nil {
			out = append(out, agent.SandboxProblem{Code: "codex-missing", Blocking: true,
				Message: "codex is not installed on this machine", Fix: "Install Codex; this sandbox runs the host's."})
		}
	}
	if _, err := os.Stat(filepath.Join(home(), "auth.json")); err != nil && at.Env("OPENAI_API_KEY") == "" {
		out = append(out, agent.SandboxProblem{Code: "codex-login-missing", Blocking: true,
			Message: "Codex isn't signed in on this machine, and sandboxes share its sign-in",
			Fix:     "Run `codex login` on this machine with its credentials in a file (cli_auth_credentials_store = \"file\", the default); sandboxes share that login.",
			SignIn:  []string{"codex", "login"}})
	}
	return out
}

// syncLogin gives a sandbox's CODEX_HOME the host's auth.json without its refresh token. A sign-in made inside the
// sandbox, which has a refresh token of its own, is left alone.
func syncLogin(hostHome, dir string) error {
	b, err := os.ReadFile(filepath.Join(hostHome, "auth.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "auth.json")
	if cur, err := os.ReadFile(path); err == nil && refreshToken(cur) != "" {
		return nil
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		return fmt.Errorf("codex's auth.json: %w", err)
	}
	if tokens, ok := doc["tokens"].(map[string]any); ok {
		tokens["refresh_token"] = ""
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if cur, err := os.ReadFile(path); err == nil && bytes.Equal(cur, out) {
		return nil
	}
	return atomicfile.WriteSecret(path, out)
}

func refreshToken(authJSON []byte) string {
	var doc struct {
		Tokens struct {
			Refresh string `json:"refresh_token"`
		} `json:"tokens"`
	}
	_ = json.Unmarshal(authJSON, &doc)
	return doc.Tokens.Refresh
}

// sandboxBinary is the codex an agent at runs: under the OS's sandbox the host's own; in a container a Linux one,
// the host's when it is that, else the native build its npm package keeps, else the release's for the host's
// version, checked against the digest GitHub publishes for it.
func (p *Provider) sandboxBinary(ctx context.Context, at agent.SandboxPlace) (string, error) {
	path, err := exec.LookPath(p.bin())
	if err != nil {
		return "", errors.New("codex is not installed on this machine")
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if at.Shared {
		return real, nil
	}
	triple, ok := triples[at.Arch]
	if !ok || at.OS != "linux" {
		return "", fmt.Errorf("codex has no build for %s/%s", at.OS, at.Arch)
	}
	if runtime.GOOS == at.OS && runtime.GOARCH == at.Arch && isELF(real) {
		return real, nil
	}
	if b, ok := npmBinary(real, triple); ok {
		return b, nil
	}
	out, err := exec.CommandContext(ctx, real, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("codex --version: %w", err)
	}
	return p.download(ctx, at, parseVersion(string(out)), triple)
}

// npmBinary is the native codex for triple beside the npm package's launcher (bin/codex.js): in the package's own
// vendor/, or its platform package's.
func npmBinary(launcher, triple string) (string, bool) {
	if filepath.Base(launcher) != "codex.js" {
		return "", false
	}
	root := filepath.Dir(filepath.Dir(launcher))
	platform := "codex-linux-x64"
	if strings.HasPrefix(triple, "aarch64") {
		platform = "codex-linux-arm64"
	}
	for _, vendor := range []string{filepath.Join(root, "vendor"), filepath.Join(filepath.Dir(root), platform, "vendor"),
		filepath.Join(root, "node_modules", "@openai", platform, "vendor")} {
		b := filepath.Join(vendor, triple, "bin", "codex")
		if isELF(b) {
			return b, true
		}
	}
	return "", false
}

func isELF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	return err == nil && string(magic[:]) == "\x7fELF"
}

// download fetches version's codex-<triple>.tar.gz from its release into at.Cache, once.
func (p *Provider) download(ctx context.Context, at agent.SandboxPlace, version, triple string) (string, error) {
	if version == "" {
		return "", errors.New("can't tell which codex to download: codex --version said no version")
	}
	p.downloads.Lock()
	defer p.downloads.Unlock()
	dst := filepath.Join(at.Cache, version, triple, "codex")
	if _, err := os.Stat(dst); err == nil {
		return dst, nil
	}
	if at.Report != nil {
		at.Report("Downloading Codex "+version+" for the sandbox", "Once for each version: later sessions reuse it.")
	}
	api := cmp.Or(p.ReleasesAPI, githubAPI)
	var rel struct {
		Assets []struct {
			Name   string `json:"name"`
			URL    string `json:"browser_download_url"`
			Digest string `json:"digest"`
		} `json:"assets"`
	}
	if err := getJSON(ctx, api+"/releases/tags/rust-v"+version, &rel); err != nil {
		return "", err
	}
	name := "codex-" + triple + ".tar.gz"
	i := slices.IndexFunc(rel.Assets, func(a struct {
		Name   string `json:"name"`
		URL    string `json:"browser_download_url"`
		Digest string `json:"digest"`
	}) bool {
		return a.Name == name
	})
	if i < 0 {
		return "", fmt.Errorf("codex %s has no %s", version, name)
	}
	asset := rel.Assets[i]
	sum, ok := strings.CutPrefix(asset.Digest, "sha256:")
	if !ok || sum == "" {
		return "", fmt.Errorf("codex %s's %s has no sha256 digest to check it against", version, name)
	}
	archive, err := getVerified(ctx, asset.URL, sum)
	if err != nil {
		return "", err
	}
	bin, err := untar(archive, "codex-"+triple)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	tmp := dst + ".part"
	if err := os.WriteFile(tmp, bin, 0o755); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}

func getJSON(ctx context.Context, url string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// maxArchive bounds a release archive's download.
const maxArchive = 512 << 20

func getVerified(ctx context.Context, url, sum string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxArchive))
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(b)
	if hex.EncodeToString(got[:]) != strings.ToLower(sum) {
		return nil, errors.New("the Codex download does not match its digest")
	}
	return b, nil
}

// untar is the file named name in a .tar.gz.
func untar(archive []byte, name string) ([]byte, error) {
	zr, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(zr)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("the archive has no %s", name)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Base(h.Name) == name && h.Typeflag == tar.TypeReg {
			return io.ReadAll(io.LimitReader(tr, maxArchive))
		}
	}
}

// MCPServers are the MCP servers in the user's ~/.codex/config.toml, in Claude's mcpServers form: a command with its
// args and env, or a URL with its headers. One turned off (enabled = false) isn't carried.
func (*Provider) MCPServers() []agent.MCPServer {
	b, err := os.ReadFile(filepath.Join(home(), "config.toml"))
	if err != nil {
		return nil
	}
	doc, err := toml.Parse(b)
	if err != nil {
		return nil
	}
	servers, _ := doc.Value()["mcp_servers"].(map[string]any)
	var out []agent.MCPServer
	for _, name := range slices.Sorted(maps.Keys(servers)) {
		t, _ := servers[name].(map[string]any)
		if on, ok := t["enabled"].(bool); ok && !on {
			continue
		}
		cfg := map[string]any{}
		switch {
		case t["command"] != nil:
			cfg["command"], cfg["args"], cfg["env"] = t["command"], t["args"], t["env"]
		case t["url"] != nil:
			cfg["type"], cfg["url"], cfg["headers"] = "http", t["url"], t["http_headers"]
		default:
			continue
		}
		raw, err := json.Marshal(cfg)
		if err == nil {
			out = append(out, agent.MCPServer{Name: name, Source: "codex", Config: raw})
		}
	}
	return out
}
