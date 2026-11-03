package codex

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
)

func writeFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// The sandbox signs in with the host's auth.json, without the refresh token: refreshing there would rotate the
// host's. A sandbox's own sign-in, with a refresh token of its own, is left alone.
// ref:   codex/codex-rs/login/src/auth/storage.rs
// quote: "Expected structure for $CODEX_HOME/auth.json."
func TestSandboxLoginHasNoRefreshToken(t *testing.T) {
	host, dir := t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(host, "auth.json"), `{"auth_mode":"chatgpt","tokens":{"id_token":"i","access_token":"a","refresh_token":"r","account_id":"x"},"last_refresh":"2026-10-01T00:00:00Z"}`, 0o600)
	if err := syncLogin(host, dir); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "auth.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Tokens map[string]string `json:"tokens"`
		Mode   string            `json:"auth_mode"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Tokens["access_token"] != "a" || got.Tokens["refresh_token"] != "" || got.Mode != "chatgpt" {
		t.Errorf("the sandbox's auth.json = %s", b)
	}
	own := `{"tokens":{"id_token":"i","access_token":"mine","refresh_token":"mine","account_id":"x"}}`
	writeFile(t, filepath.Join(dir, "auth.json"), own, 0o600)
	if err := syncLogin(host, dir); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "auth.json")); string(b) != own {
		t.Errorf("the sandbox's own sign-in was overwritten: %s", b)
	}
}

// A container runs the native Linux build the npm package keeps beside its launcher.
// ref:   codex/codex-cli/bin/codex.js
// quote: "vendorRoot = path.join(__dirname, \"..\", \"vendor\");"
func TestSandboxRunsTheNpmPackagesLinuxBuild(t *testing.T) {
	root := filepath.Join(t.TempDir(), "node_modules", "@openai", "codex")
	launcher := filepath.Join(root, "bin", "codex.js")
	writeFile(t, launcher, "#!/usr/bin/env node\n", 0o755)
	native := filepath.Join(root, "vendor", "aarch64-unknown-linux-musl", "bin", "codex")
	writeFile(t, native, "\x7fELF....", 0o755)
	if got, ok := npmBinary(launcher, "aarch64-unknown-linux-musl"); !ok || got != native {
		t.Errorf("npmBinary = %q, %v", got, ok)
	}
	if _, ok := npmBinary(launcher, "x86_64-unknown-linux-musl"); ok {
		t.Error("found a build for a target the package doesn't have")
	}
}

func tarGz(t *testing.T, name, body string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	tw.Write([]byte(body))
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

// Otherwise it downloads the release's codex-<target>.tar.gz for the host's version, once, and only if it matches
// the digest GitHub publishes for it.
// ref:   codex/.github/workflows/rust-release.yml
// quote: "codex-<target>.tar.gz       (new)"
func TestSandboxDownloadsTheReleaseChecked(t *testing.T) {
	archive := tarGz(t, "codex-x86_64-unknown-linux-musl", "\x7fELF codex")
	sum := sha256.Sum256(archive)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/tags/rust-v0.98.0":
			json.NewEncoder(w).Encode(map[string]any{"assets": []map[string]string{
				{"name": "codex-x86_64-unknown-linux-musl.tar.gz", "browser_download_url": "http://" + r.Host + "/dl", "digest": digest}}})
		case "/dl":
			gets++
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := &Provider{ReleasesAPI: srv.URL}
	at := agent.SandboxPlace{Cache: t.TempDir()}
	for range 2 {
		got, err := p.download(context.Background(), at, "0.98.0", "x86_64-unknown-linux-musl")
		if err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(got); string(b) != "\x7fELF codex" {
			t.Errorf("downloaded %q", b)
		}
	}
	if gets != 1 {
		t.Errorf("downloaded %d times, want once", gets)
	}
	digest = "sha256:" + strings.Repeat("0", 64)
	if _, err := p.download(context.Background(), agent.SandboxPlace{Cache: t.TempDir()}, "0.98.0", "x86_64-unknown-linux-musl"); err == nil {
		t.Error("a download that doesn't match its digest was kept")
	}
}

// The user's servers are config.toml's, a command or a URL each; one turned off isn't carried.
// ref:   codex/codex-rs/core/config.schema.json
// quote: "Raw MCP config shape used for deserialization and supported-field JSON Schema generation."
func TestSandboxCarriesTheUsersCodexServers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeFile(t, filepath.Join(home, "config.toml"), `model = "gpt-5.5"

[mcp_servers.docs]
command = "docs-mcp"
args = ["--stdio"]

[mcp_servers.linear]
url = "https://mcp.linear.app/mcp"
http_headers = { "X-Team" = "eng" }

[mcp_servers.off]
command = "off"
enabled = false
`, 0o600)
	got := (&Provider{}).MCPServers()
	if len(got) != 2 || got[0].Name != "docs" || got[1].Name != "linear" {
		t.Fatalf("servers = %+v", got)
	}
	var docs, linear map[string]any
	json.Unmarshal(got[0].Config, &docs)
	json.Unmarshal(got[1].Config, &linear)
	if docs["command"] != "docs-mcp" || linear["type"] != "http" || linear["url"] != "https://mcp.linear.app/mcp" {
		t.Errorf("docs %v, linear %v", docs, linear)
	}
}

// With nowhere to read a sign-in from, a sandboxed codex is refused before it starts; a key handed to the sandbox
// is a sign-in.
func TestSandboxNeedsASignIn(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	none := func(string) string { return "" }
	if ps := (&Provider{}).sandboxProblems(agent.SandboxPlace{Env: none}); len(ps) != 1 || ps[0].Code != "codex-login-missing" || !ps[0].Blocking {
		t.Errorf("problems = %+v", ps)
	}
	key := func(n string) string {
		if n == "OPENAI_API_KEY" {
			return "k"
		}
		return ""
	}
	if ps := (&Provider{}).sandboxProblems(agent.SandboxPlace{Env: key}); len(ps) != 0 {
		t.Errorf("problems with a key = %+v", ps)
	}
}
