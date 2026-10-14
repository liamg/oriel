//go:build docker

package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/liamg/oriel/term/pty"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// These run real containers: go test -tags docker ./sandbox/runner/

// fixture is a sandboxed workspace with one project, under a throwaway HOME.
type fixture struct {
	home, repo, root string
	cfg              sandbox.Config
}

// metaFile is the workspace's own copy of its manifest, which the agent can
// write; the host reads the one under sandbox.Dir.
const metaFile = ".oriel-workspace.json"

func newFixture(t *testing.T, cfg sandbox.Config) fixture {
	t.Helper()
	if _, err := container.Ping(context.Background()); err != nil {
		t.Skip(err)
	}
	if _, err := hostClaude(); err != nil {
		t.Skip(err)
	}
	t.Setenv("ORIEL_SANDBOX_BIN", orielForContainer(t))
	// Under the real HOME: Docker Desktop shares /Users but not the temp
	// dir's real path, and macOS caps a socket path at 104 bytes.
	home, err := os.MkdirTemp(os.Getenv("HOME"), ".oriel-rt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	// Paths under HOME must match inside and out, and the real HOME stays out
	// of it.
	t.Setenv("HOME", home)
	mustWrite(t, filepath.Join(home, ".gitconfig"), "[user]\n\tname = Fixture User\n\temail = fixture@example.com\n")
	repo := filepath.Join(home, "src", "demo")
	mustGit(t, "", "init", "-q", "-b", "main", repo)
	mustWrite(t, filepath.Join(repo, "README.md"), "demo\n")
	mustGit(t, repo, "add", ".")
	mustGit(t, repo, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "init")
	root := filepath.Join(home, ".oriel", "workspaces", "demo")
	mustGit(t, repo, "worktree", "add", "-q", "-b", "oriel/demo", filepath.Join(root, "demo"))
	mustGit(t, "", "init", "-q", "-b", "main", root)
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	m := sandbox.Manifest{Projects: []sandbox.Project{{Name: "demo", Repo: repo, Dir: "demo", Branch: "oriel/demo", Primary: true}}}
	if err := sandbox.SaveManifest(root, m); err != nil {
		t.Fatal(err)
	}
	b, _ := json.MarshalIndent(m, "", "  ")
	mustWrite(t, filepath.Join(root, metaFile), string(b))
	t.Cleanup(func() { _ = Stop(context.Background(), root) })
	standInSession(t, root)
	return fixture{home: home, repo: repo, root: root, cfg: cfg}
}

// standInSession opens what a launch waits for, the session's socket or port,
// with nothing behind it but a 404.
func standInSession(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(sandbox.RunDir(root), 0o700); err != nil {
		t.Fatal(err)
	}
	network, addr := "unix", sandbox.SocketPath(root)
	if !sandbox.SocketsMount() {
		network, addr = "tcp", "127.0.0.1:0"
	}
	ln, err := net.Listen(network, addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = http.Serve(ln, http.NotFoundHandler()) }()
	if !sandbox.SocketsMount() {
		port := strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)
		mustWrite(t, sandbox.PortPath(root), port)
	}
}

// run launches script in the sandbox under a pty and returns what it printed.
func (f fixture) run(t *testing.T, script string, environ ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	argv, err := Launch(ctx, f.root, f.root, []string{"bash", "-c", script}, Options{Environ: environ, Config: f.cfg})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	tty, err := pty.Start(cmd, 120, 40)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	_, _ = io.Copy(&out, tty)
	_ = cmd.Wait()
	_ = tty.Close()
	return strings.ReplaceAll(out.String(), "\r\n", "\n")
}

func TestSandboxIsolation(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	u, _ := user.Current()
	out := f.run(t, `
echo "whoami=$(whoami)"
echo "uid=$(id -u)"
echo "ssh-keys=$(ls -A "$HOME/.ssh" 2>/dev/null | grep -v -x -e config -e known_hosts | wc -l)"
test -e "`+f.repo+`/README.md" && echo checkout=visible || echo checkout=hidden
test -e "`+filepath.Join(f.home, ".oriel", "sandbox")+`/claude-token" && echo token=visible || echo token=hidden
test -e "`+sandbox.LockPath(f.root)+`" && echo lock=visible || echo lock=hidden
touch outside-`+"$$"+` 2>/dev/null; touch /etc/nope 2>/dev/null && echo etc=writable || echo etc=readonly
echo made > made-inside.txt
echo "base=$ANTHROPIC_BASE_URL"
echo "claude=$(claude --version)"
grep -q bypassPermissions "$CLAUDE_CONFIG_DIR/settings.json" && echo mode=bypass
echo "gitname=$(git config user.name)"
gh pr list 2>&1 | grep -q 'oriel__pr_create' && echo gh=redirected
printf 'protocol=https\nhost=github.com\n\n' | GIT_TERMINAL_PROMPT=0 git credential fill 2>&1 | grep -q 'oriel__push' && echo cred=redirected
test -S /run/oriel/oriel.sock -o -d /run/oriel && echo run=mounted
`, "ANTHROPIC_BASE_URL=http://localhost:4000", "GH_TOKEN=secret")

	for _, want := range []string{
		"whoami=" + u.Username, "uid=" + u.Uid, "ssh-keys=0", "checkout=hidden",
		"token=hidden", "lock=hidden", "etc=readonly", "mode=bypass", "run=mounted",
		"gitname=Fixture User", "gh=redirected", "cred=redirected",
		"base=http://" + sandbox.HostGateway + ":4000",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	if strings.Contains(out, "secret") {
		t.Errorf("GH_TOKEN leaked into the sandbox:\n%s", out)
	}
	host, _ := claudeVersion(mustHostClaude(t))
	if !strings.Contains(out, "claude="+host) {
		t.Errorf("want claude %s in:\n%s", host, out)
	}
	fi, err := os.Stat(filepath.Join(f.root, "made-inside.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if st := fi.Sys().(*syscall.Stat_t); int(st.Uid) != os.Getuid() {
		t.Errorf("file made inside is owned by uid %d, want %d", st.Uid, os.Getuid())
	}
}

func TestSandboxGit(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	ws, _ := sandbox.LoadManifest(f.root)
	proj := filepath.Join(f.root, ws.Projects[0].Dir)
	out := f.run(t, `
cd `+proj+`
echo change >> README.md
GIT_AUTHOR_NAME=a GIT_AUTHOR_EMAIL=a@a GIT_COMMITTER_NAME=a GIT_COMMITTER_EMAIL=a@a git commit -qam inside && echo commit=ok
git config user.name evil 2>/dev/null && echo config=writable || echo config=readonly
echo 'touch /tmp/pwned' > .git-hook 2>/dev/null
cp .git-hook "$(git rev-parse --git-common-dir)/hooks/post-commit" 2>/dev/null && echo hooks=writable || echo hooks=readonly
echo "[core]
	fsmonitor = touch `+f.home+`/pwned" >> `+filepath.Join(f.root, ".git", "config")+` && echo wsconfig=planted
`)
	for _, want := range []string{"commit=ok", "config=readonly", "hooks=readonly", "wsconfig=planted"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	// The commit landed in the real repository's worktree branch.
	if log, err := sandbox.Git(proj, "log", "--format=%s", "-1"); err != nil || strings.TrimSpace(log) != "inside" {
		t.Errorf("host sees last commit %q (%v), want inside", log, err)
	}
	// Host git refuses the workspace repo whose config the agent planted.
	if _, err := sandbox.Git(f.root, "status"); err == nil || !strings.Contains(err.Error(), "core.fsmonitor") {
		t.Errorf("host git in a tampered workspace: err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.home, "pwned")); err == nil {
		t.Error("the planted fsmonitor ran on the host")
	}
}

func TestSandboxWorktreeFileIgnored(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	ws, _ := sandbox.LoadManifest(f.root)
	proj := filepath.Join(f.root, ws.Projects[0].Dir)
	// A .git file pointed somewhere else: host git keeps to the worktree
	// oriel recorded.
	evil := filepath.Join(f.home, "evil.git")
	mustGit(t, "", "init", "-q", "--bare", evil)
	mustWrite(t, filepath.Join(proj, ".git"), "gitdir: "+evil+"\n")
	out, err := sandbox.Git(proj, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "evil") {
		t.Errorf("host git followed the planted .git file to %s", out)
	}
}

// An idle sandbox's sidecar and network are swept, and the next launch makes
// them again.
func TestSweepRemovesIdleSidecars(t *testing.T) {
	f := newFixture(t, sandbox.Config{})
	ctx := context.Background()
	proxy, network := ProxyName(f.root), NetworkName(f.root)
	// With no session to ask, the proxy refuses: a 403 is the proxy answering.
	const reachProxy = "curl -s -o /dev/null -w 'code=%{http_code}' --max-time 20 http://example.com"
	hasNetwork := func() bool {
		_, err := container.Run(ctx, "network", "inspect", network)
		return err == nil
	}

	// While the agent runs, a sweep from outside leaves its sidecar alone.
	done := make(chan string, 1)
	go func() { done <- f.run(t, "sleep 6; "+reachProxy) }()
	for range 100 {
		if container.Inspect(ctx, Name(f.root)).Running {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	old := time.Now().Add(-time.Hour)
	_ = os.Chtimes(filepath.Join(sandbox.Dir(f.root), "agent.env"), old, old)
	if gone := Sweep(ctx, func(string) bool { return true }); len(gone) != 0 {
		t.Errorf("swept under a running agent: %v", gone)
	}
	if out := <-done; !strings.Contains(out, "code=403") {
		t.Errorf("the agent lost its proxy: %q", out)
	}

	// The agent has gone, but a launch was a moment ago: left alone.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(sandbox.Dir(f.root), "agent.env"), now, now)
	if gone := Sweep(ctx, func(string) bool { return false }); len(gone) != 0 || !container.Inspect(ctx, proxy).Running {
		t.Errorf("swept just after a launch: %v", gone)
	}

	// Idle: the sidecar and its network go.
	_ = os.Chtimes(filepath.Join(sandbox.Dir(f.root), "agent.env"), old, old)
	if gone := Sweep(ctx, func(string) bool { return false }); !slices.Contains(gone, proxy) {
		t.Errorf("swept %v, want %s", gone, proxy)
	}
	if container.Inspect(ctx, proxy).Exists || hasNetwork() {
		t.Errorf("left behind: proxy=%v network=%v", container.Inspect(ctx, proxy).Exists, hasNetwork())
	}
	if _, ok := sandbox.Load(f.root); !ok {
		t.Error("the sandbox's own files went with it")
	}

	// And a later launch brings them back, working.
	if out := f.run(t, reachProxy); !strings.Contains(out, "code=403") {
		t.Errorf("after a sweep the next launch has no proxy: %q", out)
	}
	if !container.Inspect(ctx, proxy).Running {
		t.Error("the next launch did not bring the sidecar back")
	}
}

func TestSandboxManifestIsHostOwned(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	f.run(t, `sed -i 's#"repo": "[^"]*"#"repo": "/etc"#' `+filepath.Join(f.root, metaFile))
	ws, _ := sandbox.LoadManifest(f.root)
	if ws.Projects[0].Repo != f.repo {
		t.Errorf("host manifest repo = %q, want %q", ws.Projects[0].Repo, f.repo)
	}
	b, _ := os.ReadFile(sandbox.ManifestPath(f.root))
	var m sandbox.Manifest
	_ = json.Unmarshal(b, &m)
	if m.Projects[0].Repo != f.repo {
		t.Errorf("authoritative manifest changed: %s", b)
	}
}

var (
	e2eOrielOnce sync.Once
	e2eOrielBin  string
	e2eOrielErr  error
)

// orielForContainer builds oriel once for the containers to run: under go test
// the running binary is the test binary, which must never be re-executed.
func orielForContainer(t *testing.T) string {
	t.Helper()
	e2eOrielOnce.Do(func() {
		dir, err := os.MkdirTemp("", "oriel-e2e-bin")
		if err != nil {
			e2eOrielErr = err
			return
		}
		e2eOrielBin = filepath.Join(dir, "oriel")
		_, file, _, _ := runtime.Caller(0)
		cmd := exec.Command("go", "build", "-o", e2eOrielBin, "./cmd/oriel-sandbox")
		cmd.Dir = filepath.Join(filepath.Dir(file), "..")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
		if out, err := cmd.CombinedOutput(); err != nil {
			e2eOrielErr = fmt.Errorf("building oriel: %v\n%s", err, out)
		}
	})
	if e2eOrielErr != nil {
		t.Fatal(e2eOrielErr)
	}
	return e2eOrielBin
}

func mustHostClaude(t *testing.T) string {
	p, err := hostClaude()
	if err != nil {
		t.Skip(err)
	}
	return p
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSandboxToolchains(t *testing.T) {
	f := newFixture(t, sandbox.Config{NoEgressProxy: true})
	ws, _ := sandbox.LoadManifest(f.root)
	mustWrite(t, filepath.Join(f.root, ws.Projects[0].Dir, "go.mod"), "module demo\n\ngo 1.23.4\n")
	mustWrite(t, filepath.Join(f.root, ws.Projects[0].Dir, "main.go"), "package main\n\nfunc main() { println(\"built in the sandbox\") }\n")
	script := `
cd ` + filepath.Join(f.root, ws.Projects[0].Dir) + `
echo "goversion=$(go version)"
echo "run=$(go run . 2>&1)"
touch /mise/data/planted 2>/dev/null && echo toolchains=writable || echo toolchains=readonly
echo "gomodcache=$(go env GOMODCACHE)"
`
	start := time.Now()
	out := f.run(t, script)
	first := time.Since(start)
	for _, want := range []string{"goversion=go version go1.23.4", "run=built in the sandbox", "toolchains=readonly", "gomodcache=" + "/"} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in:\n%s", want, out)
		}
	}
	// Installed once per machine: the second launch does not install again.
	start = time.Now()
	out = f.run(t, `go version`)
	if !strings.Contains(out, "go1.23.4") {
		t.Errorf("second launch: %s", out)
	}
	t.Logf("first launch %s (installs), second %s", first, time.Since(start))
}
