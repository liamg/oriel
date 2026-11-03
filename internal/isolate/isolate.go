// Package isolate keeps a test from reaching the tests after it: every harness that starts an app, window or host
// calls Test first.
package isolate

import (
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/liamg/oriel/internal/leaktest"
)

var started sync.Map // test name → true, while that test runs

var userHome = os.Getenv("HOME") // the HOME the test binary started with: the user's

// init names TMPDIR as the OS gives it back (macOS's /var is /private/var), so every t.TempDir in a binary that
// links this package compares equal to the paths git and the code under test read. On macOS it's under /tmp, as the
// per-user TMPDIR is long enough to cut a path in a frame and to pass the unix socket limit, in a folder Spotlight
// leaves alone (.noindex): it indexes every file the tests make otherwise, and starves them.
func init() {
	dir := os.TempDir()
	if runtime.GOOS == "darwin" {
		dir = "/tmp/o.noindex"
		if err := os.MkdirAll(dir, 0o777); err != nil {
			dir = "/tmp"
		}
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		os.Setenv("TMPDIR", real)
	}
}

// Test gives t a temporary directory (TMPDIR) and, unless a harness already gave it one, a short HOME of its own, so
// nothing it keeps under HOME reaches the user's and a socket under it fits the unix limit; it puts the XDG directories back under that HOME, keeps the Go caches where they were, and fails it if it leaves
// goroutines running once its cleanups are done (leaktest.Check). It hides the user's credentials (hideCredentials).
// A second call in the same test does nothing.
func Test(t testing.TB) {
	t.Helper()
	if _, dup := started.LoadOrStore(t.Name(), true); dup {
		return
	}
	t.Cleanup(func() { started.Delete(t.Name()) })
	leaktest.Check(t)
	keepGoCaches(t)
	t.Setenv("TMPDIR", t.TempDir())
	if os.Getenv("HOME") == userHome {
		t.Setenv("HOME", shortDir(t))
	}
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR"} {
		t.Setenv(v, "")
	}
	hideCredentials(t)
}

// credentialVars are the environment variables a sign-in or API token is read from.
var credentialVars = []string{"GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "GITLAB_TOKEN",
	"AZURE_DEVOPS_TOKEN", "LINEAR_TOKEN", "ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY", "ORIEL_TOKEN"}

// keyringTools are the commands that read the OS keyring, stood in for by ones that fail: gh keeps its token there,
// and macOS's security reads the Keychain, where Claude Code keeps its sign-in.
var keyringTools = []string{"gh", "security"}

// hideCredentials keeps the user's sign-ins out of reach: the variables they're read from are empty, the keyring
// tools fail, git reads no system config (and so no credential helper), and there's no session bus to a keyring.
func hideCredentials(t testing.TB) {
	for _, v := range credentialVars {
		t.Setenv(v, "")
	}
	bin := filepath.Join(t.TempDir(), "isolate-bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range keyringTools {
		script := "#!/bin/sh\necho \"" + name + ": not run in tests (isolate)\" >&2\nexit 1\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "disabled:")
}

// keepGoCaches names the Go caches the user's HOME gives, so a go command a test runs under its own HOME doesn't
// start them again empty.
func keepGoCaches(t testing.TB) {
	home, err := os.UserHomeDir()
	if err != nil {
		return
	}
	gopath := cmp.Or(os.Getenv("GOPATH"), filepath.Join(home, "go"))
	cache := os.Getenv("GOCACHE")
	if cache == "" {
		if dir, err := os.UserCacheDir(); err == nil {
			cache = filepath.Join(dir, "go-build")
		}
	}
	t.Setenv("GOPATH", gopath)
	t.Setenv("GOMODCACHE", cmp.Or(os.Getenv("GOMODCACHE"), filepath.Join(gopath, "pkg", "mod")))
	if cache != "" {
		t.Setenv("GOCACHE", cache)
	}
}

// shortDir is a directory of t's own under /tmp, removed when it ends: t.TempDir's paths are too long for a socket.
func shortDir(t testing.TB) string {
	tmp, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(tmp, "oh")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// Net is the HTTP client a test's code gets unless the test gives its own: loopback only, so no test reaches the
// network; its connections close when the test ends.
func Net(t testing.TB) *http.Client {
	tr := &http.Transport{DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, _, _ := net.SplitHostPort(addr)
		if ip, err := netip.ParseAddr(host); host != "localhost" && (err != nil || !ip.IsLoopback()) {
			return nil, fmt.Errorf("tests don't reach the network: %s", addr)
		}
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}}
	t.Cleanup(tr.CloseIdleConnections)
	return &http.Client{Transport: tr}
}
