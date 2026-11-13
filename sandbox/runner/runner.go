// Package runner starts agents inside their workspace's sandbox: it assembles
// the container, its mounts and environment, and returns the command the PTY
// runs.
package runner

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/liamg/oriel/agent"
	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// ProxyPort is where the egress sidecar listens.
const ProxyPort = 3128

// Name is the agent container for root.
func Name(root string) string { return "oriel-" + sandbox.Key(root) }

// ProxyName is the egress sidecar for root.
func ProxyName(root string) string { return Name(root) + "-egress" }

// NetworkName is root's internal network.
func NetworkName(root string) string { return Name(root) + "-net" }

// GoCacheDir is where every container mounts sandbox.GoCache.
const GoCacheDir = "/cache/go"

// ContainerRunDir is where RunDir is mounted inside the container.
const ContainerRunDir = "/run/oriel"

// ContainerSocket is the session socket as the container sees it.
const ContainerSocket = ContainerRunDir + "/oriel.sock"

// SocketsMount reports whether RunDir can be mounted into a container of e.
// A daemon in a VM (Docker Desktop, OrbStack, Colima) cannot connect to a
// socket in a mounted directory, and a socket mounted on its own stops
// working when any container stops.
func SocketsMount(e container.Engine) bool { return runtime.GOOS == "linux" && !e.VM }

// Launch readies root's sandbox for an agent in cwd running argv, and returns
// the command to run under the PTY in its place, with what the agent's
// provider said it needs.
func Launch(ctx context.Context, root, cwd string, argv []string, o sandbox.LaunchOpts) ([]string, agent.SandboxNeeds, error) {
	engine := container.EngineOf(ctx)
	rec, ok := sandbox.Load(root)
	if !ok || !rec.Sandboxed() {
		return nil, agent.SandboxNeeds{}, errors.New("this workspace is not sandboxed")
	}
	if err := markLaunch(root); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st := startSteps("sandbox launch " + filepath.Base(root))
	defer st.Done()
	cfg := o.Config
	id, err := sandbox.CurrentIdentity()
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	image, err := Image(ctx, root, cfg)
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("image")
	markImageUsed(image)
	if err := writeUsers(ctx, root, image, id); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("users")
	needs, err := sandbox.Needs(ctx, root, cwd, o, agent.SandboxPlace{OS: "linux", Arch: runtime.GOARCH, Home: id.Home, UID: id.UID})
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("agent's needs")
	orielBin, err := OrielBinary()
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	if err := sandbox.Prepare(root); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	mounts, err := Mounts(root, cwd, id, needs, orielBin)
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	tools, err := agentTools(needs.Tools)
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	bins, err := Provision(ctx, id, append(workspaceTools(root), tools...))
	if err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("toolchains")
	if len(bins) > 0 {
		mounts = append(mounts, container.Mount{Source: filepath.Join(toolchainDir(), "data"), Target: MiseDir + "/data", ReadOnly: true})
	}

	spec := container.Spec{
		Name:    Name(root),
		Image:   image,
		User:    fmt.Sprintf("%d:%d", id.UID, id.GID),
		Home:    id.Home,
		Workdir: root,
		Mounts:  mounts,
		Tmpfs:   []string{"/tmp:exec,mode=1777"},
		Hosts:   []string{sandbox.HostGateway + ":host-gateway"},
		Labels:  map[string]string{"oriel.root": root},

		Memory:    cfg.Memory,
		PidsLimit: cfg.PidsLimit,
	}
	env := agentEnv(root, id, o, needs)
	if len(bins) > 0 {
		env["PATH"] = strings.Join(bins, ":") + ":" + env["PATH"]
	}
	// One container per agent run, gone when the agent exits; a leftover
	// from a run that was killed hard goes first.
	if err := container.Remove(ctx, spec.Name); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	// Every connection leaves through the sidecar, filtering on or off: it has the host dial, so what only the host
	// reaches (a VPN, a tailnet, its private DNS) reaches the sandbox too. With filtering off it allows everything.
	hostPorts := sandbox.HostPorts(sandbox.EndpointEnv(o.Environ, cfg.Env, needs))
	if err := ensureProxy(ctx, root, image, orielBin, id, hostPorts); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("egress proxy")
	spec.Network = NetworkName(root)
	proxy := fmt.Sprintf("http://%s:%d", ProxyName(root), ProxyPort)
	for _, k := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		env[k] = proxy
	}
	env["NO_PROXY"] = "localhost,127.0.0.1"
	env["no_proxy"] = env["NO_PROXY"]
	if err := container.PrepareMountpoints(spec.Mounts); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	st.Mark("files")
	envFile := launchStamp(root)
	if err := os.WriteFile(envFile, []byte(sandbox.EnvFile(env)), 0o600); err != nil {
		return nil, agent.SandboxNeeds{}, err
	}
	spec.Workdir = cwd
	spec.Entrypoint, spec.Command = argv[0], argv[1:]
	return spec.InteractiveArgv(engine, envFile), needs, nil
}

// Stop removes root's containers and network. The workspace is untouched.
func Stop(ctx context.Context, root string) error {
	err := container.Remove(ctx, Name(root))
	if perr := container.Remove(ctx, ProxyName(root)); err == nil {
		err = perr
	}
	container.RemoveNetwork(ctx, NetworkName(root))
	container.RemoveVolume(ctx, RunVolumeName(root))
	return err
}

func agentEnv(root string, id sandbox.Identity, o sandbox.LaunchOpts, n agent.SandboxNeeds) map[string]string {
	return sandbox.AgentEnv(root, o, n, true, map[string]string{
		"HOME":          id.Home,
		"USER":          id.Name,
		"LOGNAME":       id.Name,
		"PATH":          filepath.Join(id.Home, sandbox.ShimDir) + ":/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"ORIEL_SOCKET":  ContainerSocket,
		"SSH_AUTH_SOCK": ContainerRunDir + "/ssh-agent.sock",
		"GOMODCACHE":    GoCacheDir + "/mod",
		"GOCACHE":       GoCacheDir + "/build",
	})
}

func mustToken(root string) string {
	r, _ := sandbox.Load(root)
	return r.AgentToken
}

// Mounts is everything root's container sees of the host: n's binaries in /usr/local/bin beside oriel.
func Mounts(root, cwd string, id sandbox.Identity, n agent.SandboxNeeds, orielBin string) ([]container.Mount, error) {
	binds, err := sandbox.Binds(root, cwd, n)
	if err != nil {
		return nil, err
	}
	dir := sandbox.Dir(root)
	m := []container.Mount{
		{Source: sandbox.HomeDir(root), Target: id.Home},
		{Source: filepath.Join(dir, "etc", "passwd"), Target: "/etc/passwd", ReadOnly: true},
		{Source: filepath.Join(dir, "etc", "group"), Target: "/etc/group", ReadOnly: true},
		{Source: orielBin, Target: "/usr/local/bin/oriel", ReadOnly: true},
		{Source: sandbox.GoCache(), Target: GoCacheDir},
	}
	for _, name := range slices.Sorted(maps.Keys(n.Bins)) {
		m = append(m, container.Mount{Source: n.Bins[name], Target: "/usr/local/bin/" + name, ReadOnly: true})
	}
	run, err := runMounts(context.Background(), root, id, false)
	if err != nil {
		return nil, err
	}
	m = append(m, run...)
	for _, b := range binds {
		m = append(m, container.Mount{Source: b.Host, Target: b.Agent, ReadOnly: b.ReadOnly})
	}
	return m, nil
}

// writeUsers mirrors the host user into the image's passwd and group files.
func writeUsers(ctx context.Context, root, image string, id sandbox.Identity) error {
	etc := filepath.Join(sandbox.Dir(root), "etc")
	if err := os.MkdirAll(etc, 0o700); err != nil {
		return err
	}
	passwd, group, err := imageUsers(ctx, image)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(etc, "passwd"), []byte(sandbox.Passwd(passwd, id)), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(etc, "group"), []byte(sandbox.Group(group, id)), 0o644)
}

// imageUsers is an image's own /etc/passwd and /etc/group. They are fixed for
// a given image, so they are read from a throwaway container once per image id
// and kept beside the sandboxes, rather than costing every launch a container.
func imageUsers(ctx context.Context, image string) (passwd, group string, err error) {
	imageID, _ := container.ImageID(ctx, image)
	cache := ""
	if imageID != "" {
		cache = filepath.Join(sandbox.Base(), "image-users", strings.NewReplacer(":", "-", "/", "-").Replace(imageID))
		if b, err := os.ReadFile(cache); err == nil {
			if p, g, ok := strings.Cut(string(b), "\x00"); ok {
				return p, g, nil
			}
		}
	}
	out, err := container.Output(ctx, image, "sh", "-c", `cat /etc/passwd; printf '\0'; cat /etc/group`)
	if err != nil {
		return "", "", err
	}
	p, g, ok := strings.Cut(out, "\x00")
	if !ok {
		return "", "", errors.New("the image's passwd and group files could not be read")
	}
	if cache != "" && os.MkdirAll(filepath.Dir(cache), 0o700) == nil {
		_ = os.WriteFile(cache, []byte(out), 0o600)
	}
	return p, g, nil
}
