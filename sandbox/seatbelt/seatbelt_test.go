package seatbelt

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
)

func TestSBPLOrdersPaths(t *testing.T) {
	got := SBPL(Profile{
		Hide:    []string{"/Users/me"},
		Paths:   []Access{{Path: "/Users/me/src/.git", Write: true}, {Path: "/Users/me/src/.git/config"}},
		Sockets: []string{"/Users/me/.oriel/run/oriel.sock"},
		Ports:   []int{50123},
	})
	order := []string{
		"(version 1)\n(deny default)\n",
		"(allow file-read*)\n",
		`(deny file-read* (subpath "/Users/me"))`,
		`(allow file-read* file-write* (subpath "/Users/me/src/.git"))`,
		`(allow file-read* (subpath "/Users/me/src/.git/config"))`,
		`(deny file-write* (subpath "/Users/me/src/.git/config"))`,
		`(remote unix-socket (path-literal "/Users/me/.oriel/run/oriel.sock"))`,
		`(remote tcp "localhost:50123")`,
	}
	at := 0
	for _, want := range order {
		i := strings.Index(got[at:], want)
		if i < 0 {
			t.Fatalf("the profile lacks %s after byte %d:\n%s", want, at, got)
		}
		at += i + len(want)
	}
	for _, not := range []string{"network*", "(remote ip", "com.apple.SecurityServer", "(allow default"} {
		if strings.Contains(got, not) {
			t.Errorf("the profile has %s:\n%s", not, got)
		}
	}
}

func TestSBPLNetwork(t *testing.T) {
	if got := SBPL(Profile{Listen: true, Loopback: true}); !strings.Contains(got, `(allow network-outbound (remote ip "localhost:*"))`) ||
		!strings.Contains(got, `(allow network-bind network-inbound (local ip "localhost:*"))`) || strings.Contains(got, "network*") {
		t.Errorf("loopback:\n%s", got)
	}
	if got := SBPL(Profile{Network: true}); !strings.Contains(got, "(allow network*)") {
		t.Errorf("open network:\n%s", got)
	}
}

func TestLoopbackModes(t *testing.T) {
	for mode, want := range map[string]struct {
		listen, any bool
		ports       int
	}{
		"":                        {true, true, 0},
		sandbox.LoopbackAll:       {true, true, 0},
		sandbox.LoopbackEphemeral: {true, false, ephemeralLast - ephemeralFirst + 1},
		sandbox.LoopbackNone:      {false, false, 0},
	} {
		p := loopback(mode)
		if p.Listen != want.listen || p.Loopback != want.any || len(p.Ports) != want.ports {
			t.Errorf("%q: listen %t, any %t, %d ports", mode, p.Listen, p.Loopback, len(p.Ports))
		}
	}
	got := SBPL(loopback(sandbox.LoopbackEphemeral))
	for _, port := range []int{80, 3000, 5432, 8080, ephemeralFirst - 1} {
		if strings.Contains(got, fmt.Sprintf(`"localhost:%d"`, port)) {
			t.Errorf("ephemeral reaches %d", port)
		}
	}
	if !strings.Contains(got, fmt.Sprintf(`(remote tcp "localhost:%d")`, ephemeralFirst)) || strings.Contains(got, `(remote ip "localhost:*")`) {
		t.Error("ephemeral: wrong ports")
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{`/a`: `"/a"`, `/a"b`: `"/a\"b"`, `/a\b`: `"/a\\b"`, `/a") (allow default`: `"/a\") (allow default"`} {
		if got := quote(in); got != want {
			t.Errorf("quote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestToolchainsStayOutOfCredentials(t *testing.T) {
	home := t.TempDir()
	for _, d := range []string{".cargo/bin", ".rustup", ".local/bin", "sdk/go1.26/bin"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	path := strings.Join([]string{"/usr/bin", filepath.Join(home, ".cargo/bin"), filepath.Join(home, ".local/bin"), filepath.Join(home, "sdk/go1.26/bin"), home}, ":")
	got := toolchains(home, path)
	for _, want := range []string{".cargo/bin", ".rustup", ".local/bin", "sdk/go1.26", "sdk/go1.26/bin"} {
		if !slices.Contains(got, filepath.Join(home, want)) {
			t.Errorf("%s not readable: %v", want, got)
		}
	}
	for _, not := range []string{home, filepath.Join(home, ".cargo"), filepath.Join(home, ".local"), "/usr"} {
		if slices.Contains(got, not) {
			t.Errorf("%s readable: %v", not, got)
		}
	}
}

func TestLinkReplacesMountPoints(t *testing.T) {
	dir, target := t.TempDir(), t.TempDir()
	empty, file, stale := filepath.Join(dir, "empty"), filepath.Join(dir, "CLAUDE.md"), filepath.Join(dir, "stale")
	os.Mkdir(empty, 0o700)
	os.WriteFile(file, nil, 0o600)
	os.Symlink("/elsewhere", stale)
	for _, at := range []string{empty, file, stale, filepath.Join(dir, "new", "x")} {
		if err := link(at, target); err != nil {
			t.Fatal(err)
		}
		if got, _ := os.Readlink(at); got != target {
			t.Errorf("%s links to %q", at, got)
		}
	}
	full := filepath.Join(dir, "full")
	os.WriteFile(full, []byte("mine"), 0o600)
	if err := link(full, target); err == nil {
		t.Error("a file with content was replaced")
	}
}

func TestHealthOffMacOS(t *testing.T) {
	if Available() == nil {
		t.Skip("this is macOS")
	}
	p, ok := sandbox.Blocking((&Seatbelt{}).Health(context.Background(), sandbox.Config{}))
	if !ok || p.Code != "seatbelt-unavailable" {
		t.Errorf("health = %+v", p)
	}
}

// Live on macOS: the agent's profile hides the home directory, keeps git's own files read-only and the network
// to loopback.
func TestSandboxExecConfines(t *testing.T) {
	if err := Available(); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	git := filepath.Join(home, "repo", ".git")
	os.MkdirAll(git, 0o700)
	secret, config := filepath.Join(home, "secret"), filepath.Join(git, "config")
	os.WriteFile(secret, []byte("x"), 0o600)
	os.WriteFile(config, []byte("x"), 0o600)
	p := real(Profile{Hide: []string{home}, Paths: []Access{{Path: git, Write: true}, {Path: config}}, Listen: true, Loopback: true})
	sh := func(script string) error {
		return exec.Command("/usr/bin/sandbox-exec", "-p", SBPL(p), "/bin/sh", "-c", script).Run()
	}
	for script, ok := range map[string]bool{
		"cat " + secret:                         false,
		"cat " + config:                         true,
		"echo y > " + config:                    false,
		"echo y > " + filepath.Join(git, "ref"): true,
		"ls /usr/bin >/dev/null":                true,
		"/usr/bin/nc -z -G 2 example.com 443":   false,
	} {
		if err := sh(script); (err == nil) != ok {
			t.Errorf("%s: %v, want ok=%t", script, err, ok)
		}
	}
}

func TestRegistered(t *testing.T) {
	if b, err := sandbox.BackendFor(sandbox.Config{Backend: "seatbelt"}); err != nil || b.Name() != "seatbelt" {
		t.Errorf("BackendFor(seatbelt) = %v, %v", b, err)
	}
}

// needer is a provider whose agent needs a binary on its PATH and a host directory at another path.
type needer struct{ bin, host, at string }

func (n needer) SandboxNeeds(_ context.Context, at agent.SandboxPlace) (agent.SandboxNeeds, error) {
	return agent.SandboxNeeds{Bins: map[string]string{"agent": n.bin}, Mounts: []agent.Mount{{Host: n.host, Sandbox: filepath.Join(at.Dir, n.at)}},
		Env: map[string]string{"AGENT_HOME": at.Dir}}, nil
}

func (needer) MCPServers() []agent.MCPServer { return nil }

// The command: the workspace and its git directory writable, git's own files read-only, the rest of home hidden,
// what the agent's provider needs linked where it asked, and the network through the proxy.
func TestCommand(t *testing.T) {
	home, err := os.MkdirTemp("/tmp", "sb")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("HOME", home)
	repo, root := filepath.Join(home, "src", "repo"), filepath.Join(home, "work")
	gitT(t, home, "init", "-q", repo)
	gitT(t, repo, "commit", "-q", "--allow-empty", "-m", "x")
	gitT(t, repo, "worktree", "add", "-q", filepath.Join(root, "repo"))
	if _, err := sandbox.Create(root, sandbox.ModeSandbox); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.SaveManifest(root, sandbox.Manifest{Projects: []sandbox.Project{{Name: "repo", Repo: repo, Dir: "repo"}}}); err != nil {
		t.Fatal(err)
	}
	s := &Seatbelt{}
	defer s.Stop(context.Background(), root)
	transcripts := filepath.Join(home, ".agent", "projects")
	if err := os.MkdirAll(transcripts, 0o700); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin", "agent")
	if err := os.MkdirAll(filepath.Dir(bin), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := s.command(context.Background(), root, root, []string{"claude"}, sandbox.LaunchOptions{Environ: []string{"PATH=/usr/bin", "SECRET=x"},
		Provider: "agent", Agent: needer{bin: bin, host: transcripts, at: "projects"}})
	if err != nil {
		t.Fatal(err)
	}
	if c.Dir != root || !slices.Equal(c.Argv, []string{sandboxExec, "-f", filepath.Join(sandbox.Dir(root), "seatbelt.sb"), "claude"}) {
		t.Errorf("command %v in %s", c.Argv, c.Dir)
	}
	env := map[string]string{}
	for _, kv := range c.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = v
	}
	if env["HOME"] != sandbox.HomeDir(root) || env["ORIEL_SOCKET"] != sandbox.SocketPath(root) || env["SECRET"] != "" ||
		env["AGENT_HOME"] != sandbox.AgentDir(root, "agent") ||
		!strings.HasPrefix(env["HTTPS_PROXY"], "http://127.0.0.1:") || !strings.HasSuffix(env["PATH"], ":/usr/bin") {
		t.Errorf("env %v", env)
	}
	b, err := os.ReadFile(filepath.Join(sandbox.Dir(root), "seatbelt.sb"))
	if err != nil {
		t.Fatal(err)
	}
	profile := string(b)
	real := func(p string) string { r, _ := filepath.EvalSymlinks(p); return r }
	common := real(filepath.Join(repo, ".git"))
	for _, want := range []string{
		`(deny file-read* (subpath "/Users") (subpath "/Volumes") (subpath ` + quote(real(home)) + `)`,
		`(allow file-read* file-write* (subpath ` + quote(real(root)) + `))`,
		`(allow file-read* file-write* (subpath ` + quote(common) + `))`,
		`(deny file-write* (subpath ` + quote(filepath.Join(common, "config")) + `))`,
		`(deny file-write* (subpath ` + quote(filepath.Join(common, "hooks")) + `))`,
		`(remote tcp "localhost:` + strings.TrimPrefix(env["HTTPS_PROXY"], "http://127.0.0.1:") + `")`,
	} {
		if !strings.Contains(profile, want) {
			t.Errorf("the profile lacks %s:\n%s", want, profile)
		}
	}
	if got, _ := os.Readlink(filepath.Join(sandbox.AgentDir(root, "agent"), "projects")); got != transcripts {
		t.Errorf("the agent's projects link to %q", got)
	}
	if got, _ := os.Readlink(filepath.Join(sandbox.Dir(root), "bin", "agent")); got != real(bin) {
		t.Errorf("the agent's binary links to %q", got)
	}
}

func gitT(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}
