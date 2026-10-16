// Package seatbelt is the sandbox.Backend that confines an agent with macOS's own sandbox (sandbox-exec): no
// container, no image, the host's toolchains. The agent reads the system but not the user's home, writes only its
// workspace and the sandbox's own directories, and reaches the network only through the egress proxy, which runs
// in-process on loopback.
package seatbelt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/agentcmd"
	"github.com/liamg/oriel/sandbox/agentsock"
)

const sandboxExec = "/usr/bin/sandbox-exec"

// Seatbelt is the backend; each sandboxed workspace has an egress proxy while its agent runs.
type Seatbelt struct {
	mu      sync.Mutex
	proxies map[string]proxy
}

type proxy struct {
	port   int
	cancel context.CancelFunc
}

func init() { sandbox.Register(&Seatbelt{}) }

func (*Seatbelt) Name() string { return "seatbelt" }

func (*Seatbelt) Available() error { return Available() }

// Available says whether this machine has Seatbelt.
func Available() error {
	if runtime.GOOS != "darwin" {
		return errors.New("seatbelt sandboxes need macOS")
	}
	if _, err := os.Stat(sandboxExec); err != nil {
		return fmt.Errorf("no %s", sandboxExec)
	}
	return nil
}

func (s *Seatbelt) Launch(ctx context.Context, root, cwd string, argv []string, o sandbox.LaunchOptions) (sandbox.Command, error) {
	if err := Available(); err != nil {
		return sandbox.Command{}, err
	}
	return s.command(root, cwd, argv, o)
}

// command writes root's profile and returns the agent's command under it.
func (s *Seatbelt) command(root, cwd string, argv []string, o sandbox.LaunchOptions) (sandbox.Command, error) {
	if rec, ok := sandbox.Load(root); !ok || !rec.Sandboxed() {
		return sandbox.Command{}, errors.New("this workspace is not sandboxed")
	}
	id, err := sandbox.CurrentIdentity()
	if err != nil {
		return sandbox.Command{}, err
	}
	if err := sandbox.Prepare(root, cwd, o); err != nil {
		return sandbox.Command{}, err
	}
	binds, err := sandbox.Binds(root, cwd, o.ModDir)
	if err != nil {
		return sandbox.Command{}, err
	}
	bin, reads, err := binDir(root)
	if err != nil {
		return sandbox.Command{}, err
	}
	home, run := sandbox.HomeDir(root), sandbox.RunDir(root)
	hostHome, _ := os.UserHomeDir()
	hostPath := sandbox.HostEnv(o.Environ, []string{"PATH"})["PATH"]
	reads = append(reads, toolchains(hostHome, hostPath)...)

	p := loopback(o.Config.Loopback)
	p.Hide, p.Network = []string{"/Users", "/Volumes", hostHome}, o.Config.NoEgressProxy
	if id.Home != hostHome {
		p.Hide = append(p.Hide, id.Home)
	}
	for _, r := range reads {
		p.Paths = append(p.Paths, Access{Path: r})
	}
	for _, b := range binds {
		if b.Agent != b.Host {
			if err := link(b.Agent, b.Host); err != nil {
				return sandbox.Command{}, err
			}
		}
		p.Paths = append(p.Paths, Access{Path: b.Host, Write: !b.ReadOnly})
	}
	for _, d := range []string{home, sandbox.TmpDir(root), sandbox.GoCache()} {
		p.Paths = append(p.Paths, Access{Path: d, Write: true})
	}
	p.Paths = append(p.Paths, Access{Path: run}, Access{Path: bin})
	for _, sock := range []string{"oriel.sock", "ssh-agent.sock", sandbox.HookSocket} {
		p.Sockets = append(p.Sockets, filepath.Join(run, sock))
	}
	p = real(p)

	env := sandbox.AgentEnv(root, o, false, map[string]string{
		"HOME":          home,
		"USER":          id.Name,
		"LOGNAME":       id.Name,
		"PATH":          strings.Join([]string{filepath.Join(home, sandbox.ShimDir), bin, hostPath}, ":"),
		"TMPDIR":        sandbox.TmpDir(root),
		"ORIEL_SOCKET":  sandbox.SocketPath(root),
		"SSH_AUTH_SOCK": filepath.Join(run, "ssh-agent.sock"),
		"GOMODCACHE":    filepath.Join(sandbox.GoCache(), "mod"),
		"GOCACHE":       filepath.Join(sandbox.GoCache(), "build"),
	})
	if !o.Config.NoEgressProxy {
		port, err := s.proxy(root)
		if err != nil {
			return sandbox.Command{}, err
		}
		p.Ports = append(p.Ports, port)
		url := fmt.Sprintf("http://127.0.0.1:%d", port)
		for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
			env[k] = url
		}
		env["NO_PROXY"] = "localhost,127.0.0.1"
		env["no_proxy"] = env["NO_PROXY"]
	}
	profile := filepath.Join(sandbox.Dir(root), "seatbelt.sb")
	if err := os.WriteFile(profile, []byte(SBPL(p)), 0o600); err != nil {
		return sandbox.Command{}, err
	}
	return sandbox.Command{Argv: append([]string{sandboxExec, "-f", profile}, argv...), Env: environ(env), Dir: cwd}, nil
}

func (s *Seatbelt) Stop(_ context.Context, root string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.proxies[root]; ok {
		p.cancel()
		delete(s.proxies, root)
	}
	return nil
}

func (*Seatbelt) Health(_ context.Context, cfg sandbox.Config) []sandbox.Problem {
	if err := Available(); err != nil {
		return []sandbox.Problem{{Code: "seatbelt-unavailable", Blocking: true, Message: err.Error(),
			Fix: "Set sandbox.backend to docker."}}
	}
	var out []sandbox.Problem
	if _, err := exec.LookPath("claude"); err != nil {
		out = append(out, sandbox.Problem{Code: "claude-missing", Blocking: true,
			Message: "claude is not installed on this machine", Fix: "Install Claude Code; seatbelt sandboxes run the host's."})
	}
	var unused []string
	for name, set := range map[string]bool{"sandbox.image": cfg.Image != "", "sandbox.claude_version": cfg.ClaudeVersion != "",
		"sandbox.memory": cfg.Memory != "", "sandbox.pids_limit": cfg.PidsLimit != 0} {
		if set {
			unused = append(unused, name)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		out = append(out, sandbox.Problem{Code: "settings-unused",
			Message: strings.Join(unused, ", ") + " only apply to docker sandboxes",
			Fix:     "Clear them, or set sandbox.backend to docker."})
	}
	return append(out, sandbox.LoginProblems(cfg)...)
}

// Sweep has nothing to do: a sandbox's proxy goes with Stop, or with this process.
func (*Seatbelt) Sweep(context.Context, func(string) bool) []string { return nil }

func (*Seatbelt) AgentRunDir(root string) string { return sandbox.RunDir(root) }

func (*Seatbelt) ServesPort() bool { return false }

// The ephemeral ports macOS gives a server that asks for port 0.
const ephemeralFirst, ephemeralLast = 49152, 65535

// loopback is the profile's loopback access for mode, a sandbox.Loopback* value.
func loopback(mode string) Profile {
	switch mode {
	case sandbox.LoopbackNone:
		return Profile{}
	case sandbox.LoopbackEphemeral:
		p := Profile{Listen: true}
		for port := ephemeralFirst; port <= ephemeralLast; port++ {
			p.Ports = append(p.Ports, port)
		}
		return p
	}
	return Profile{Listen: true, Loopback: true}
}

// proxy starts root's egress proxy on loopback, once, and returns its port.
func (s *Seatbelt) proxy(root string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.proxies[root]; ok {
		return p.port, nil
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	rec, _ := sandbox.Load(root)
	sock := sandbox.SocketPath(root)
	dial := func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}
	ctx, cancel := context.WithCancel(context.Background())
	px := agentcmd.NewEgressProxy(ctx, agentsock.NewSocket(sock, rec.AgentToken), dial, rec.AgentToken, nil)
	go func() { _ = px.Serve(ctx, ln) }()
	if s.proxies == nil {
		s.proxies = map[string]proxy{}
	}
	p := proxy{port: ln.Addr().(*net.TCPAddr).Port, cancel: cancel}
	s.proxies[root] = p
	return p.port, nil
}

// binDir is root's directory of the host's oriel and claude, first on the agent's PATH after the shims, with the
// paths the agent must read to run them.
func binDir(root string) (string, []string, error) {
	bin := filepath.Join(sandbox.Dir(root), "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		return "", nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return "", nil, err
	}
	targets := map[string]string{"oriel": self}
	if c, err := exec.LookPath("claude"); err == nil {
		targets["claude"] = c
	}
	var reads []string
	for name, target := range targets {
		if r, err := filepath.EvalSymlinks(target); err == nil {
			target = r
		}
		if err := link(filepath.Join(bin, name), target); err != nil {
			return "", nil, err
		}
		reads = append(reads, filepath.Dir(target))
	}
	return bin, reads, nil
}

// toolDirs are where toolchain managers keep what they install, under the home directory; none holds credentials.
var toolDirs = []string{".local/share/mise", ".config/mise", ".cargo/bin", ".rustup", ".nvm", ".volta", ".bun/bin", ".deno/bin",
	".pyenv", ".rbenv", ".asdf", ".sdkman/candidates", "go/bin", "sdk"}

// toolchains are the toolchains under home the agent may read: the managers' directories, and each directory on
// path that's under home, with a bin's parent (its lib beside it) unless that holds more than toolchains.
func toolchains(home, path string) []string {
	var out []string
	for _, d := range toolDirs {
		if _, err := os.Stat(filepath.Join(home, d)); err == nil {
			out = append(out, filepath.Join(home, d))
		}
	}
	shared := []string{home, filepath.Join(home, ".local"), filepath.Join(home, ".cargo"), filepath.Join(home, ".bun"), filepath.Join(home, ".deno")}
	for _, d := range filepath.SplitList(path) {
		if rel, err := filepath.Rel(home, d); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			continue
		}
		out = append(out, d)
		if parent := filepath.Dir(d); filepath.Base(d) == "bin" && !slices.Contains(shared, parent) {
			out = append(out, parent)
		}
	}
	return out
}

// link makes at a symlink to target, replacing a link or an empty file or directory (another backend's mount
// point).
func link(at, target string) error {
	if cur, err := os.Readlink(at); err == nil && cur == target {
		return nil
	}
	if fi, err := os.Lstat(at); err == nil {
		if fi.Mode().IsRegular() && fi.Size() > 0 {
			return fmt.Errorf("%s is in the way of a link to %s", at, target)
		}
		if err := os.Remove(at); err != nil {
			return fmt.Errorf("%s is in the way of a link to %s: %w", at, target, err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(at), 0o700); err != nil {
		return err
	}
	return os.Symlink(target, at)
}

// real is p with its paths' symlinks followed, as Seatbelt matches them.
func real(p Profile) Profile {
	r := func(s string) string {
		if e, err := filepath.EvalSymlinks(s); err == nil {
			return e
		}
		return s
	}
	for i, h := range p.Hide {
		p.Hide[i] = r(h)
	}
	for i, a := range p.Paths {
		p.Paths[i].Path = r(a.Path)
	}
	for i, s := range p.Sockets {
		p.Sockets[i] = filepath.Join(r(filepath.Dir(s)), filepath.Base(s))
	}
	return p
}

func environ(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}
