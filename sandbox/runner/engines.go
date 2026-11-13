package runner

import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/liamg/oriel/sandbox"
	"github.com/liamg/oriel/sandbox/container"
)

// Engine is the sandbox.Backend that runs each agent in a container of a
// docker-compatible engine.
type Engine struct {
	// Label is the backend's name in sandbox.backend.
	Label string
	// Find is the engine on this machine, or why it can't run.
	Find func() (container.Engine, error)
	// Fix says how to install or start it.
	Fix string
}

// The engines oriel knows. Docker drives whatever daemon the docker CLI's
// context names; the others pin their own daemon's socket.
var (
	Docker = Engine{Label: "docker", Find: findDocker,
		Fix: "Install Docker (Docker Desktop, or Docker Engine and `systemctl start docker`)."}
	OrbStack = Engine{Label: "orbstack", Find: findOrbStack, Fix: "Install OrbStack (brew install orbstack) and open it."}
	Colima   = Engine{Label: "colima", Find: findColima, Fix: "Install Colima (brew install colima) and run `colima start`."}
	Podman   = Engine{Label: "podman", Find: findPodman, Fix: "Install Podman, and on macOS run `podman machine start`."}
)

func init() {
	for _, e := range []Engine{Docker, OrbStack, Colima, Podman} {
		sandbox.Register(e)
	}
}

// Where the alternatives keep their docker sockets, under the home directory.
var (
	orbStackSockets = []string{".orbstack/run/docker.sock"}
	colimaSockets   = []string{".colima/default/docker.sock", ".colima/docker.sock"}
	// Docker's own: Engine's, Docker Desktop's, and rootless Engine's.
	dockerSockets = []string{"/var/run/docker.sock", ".docker/run/docker.sock", ".docker/desktop/docker.sock"}
	// otherEngines own a docker socket that isn't Docker's.
	otherEngines = []string{".orbstack", ".colima", ".rd", ".local/share/containers"}
)

// findDocker is the docker CLI, when a Docker daemon is set up: DOCKER_HOST, or one
// of Docker's sockets that isn't another engine's under /var/run/docker.sock.
func findDocker() (container.Engine, error) {
	e := container.Docker
	if !e.Installed() {
		return e, fmt.Errorf("%s is not installed", e.Bin)
	}
	if os.Getenv("DOCKER_HOST") != "" {
		return e, nil
	}
	socks := dockerSockets
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		socks = append(socks[:len(socks):len(socks)], d+"/docker.sock")
	}
	for _, s := range socks {
		if p := container.Socket(s); p != "" && !container.Within(p, otherEngines...) {
			return e, nil
		}
	}
	return e, fmt.Errorf("no Docker daemon is set up")
}

// findOrbStack is the docker CLI on OrbStack's socket; OrbStack ships its own
// copy of the CLI for when Docker's isn't installed.
func findOrbStack() (container.Engine, error) {
	sock := container.Socket(orbStackSockets...)
	if sock == "" {
		return container.Engine{}, fmt.Errorf("OrbStack is not installed")
	}
	e := container.Engine{Bin: "docker", Host: sock, VM: true}
	if !e.Installed() {
		e.Bin = container.Socket(".orbstack/bin/docker")
	}
	if e.Bin == "" {
		return e, fmt.Errorf("OrbStack's docker CLI is missing")
	}
	return e, nil
}

func findColima() (container.Engine, error) {
	sock := container.Socket(colimaSockets...)
	if sock == "" {
		return container.Engine{}, fmt.Errorf("Colima is not installed")
	}
	e := container.Engine{Bin: "docker", Host: sock, VM: true}
	if !e.Installed() {
		return e, fmt.Errorf("docker is not installed; Colima needs its CLI")
	}
	return e, nil
}

func findPodman() (container.Engine, error) {
	e := container.Engine{Bin: "podman", VM: runtime.GOOS != "linux"}
	if !e.Installed() {
		return e, fmt.Errorf("podman is not installed")
	}
	return e, nil
}

func (b Engine) Name() string { return b.Label }

func (b Engine) Available() error {
	_, err := b.Find()
	return err
}

// with is ctx with b's engine, for every container call under it.
func (b Engine) with(ctx context.Context) (context.Context, container.Engine, error) {
	e, err := b.Find()
	return container.WithEngine(ctx, e), e, err
}

func (b Engine) Launch(ctx context.Context, root, cwd string, argv []string, o sandbox.LaunchOpts) (sandbox.Command, error) {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return sandbox.Command{}, err
	}
	cmd, needs, err := Launch(ctx, root, cwd, argv, o)
	if err != nil {
		return sandbox.Command{}, err
	}
	return sandbox.Command{Argv: cmd, Env: os.Environ(), Needs: needs}, nil
}

func (b Engine) Stop(ctx context.Context, root string) error {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return err
	}
	return Stop(ctx, root)
}

func (b Engine) Health(ctx context.Context, cfg sandbox.Config) []sandbox.Problem {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return []sandbox.Problem{{Code: "engine-missing", Message: err.Error(), Fix: b.Fix, Blocking: true}}
	}
	return Health(ctx, cfg, b.Fix)
}

func (b Engine) Sweep(ctx context.Context, attached func(root string) bool) []string {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return nil
	}
	return Sweep(ctx, attached)
}

func (b Engine) Describe(ctx context.Context, root string, cfg sandbox.Config) sandbox.Description {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return sandbox.Description{Container: Name(root)}
	}
	return Describe(ctx, root, cfg)
}

func (b Engine) Rebuild(ctx context.Context, root string, cfg sandbox.Config) error {
	ctx, _, err := b.with(ctx)
	if err != nil {
		return err
	}
	return Rebuild(ctx, root, cfg)
}

func (b Engine) Shell(ctx context.Context, root, cwd string, _ sandbox.LaunchOpts) (sandbox.Command, error) {
	ctx, e, err := b.with(ctx)
	if err != nil {
		return sandbox.Command{}, err
	}
	argv, err := Shell(ctx, e, root, cwd)
	if err != nil {
		return sandbox.Command{}, err
	}
	return sandbox.Command{Argv: argv, Env: os.Environ()}, nil
}

func (Engine) AgentRunDir(string) string { return ContainerRunDir }

func (b Engine) ServesPort() bool {
	e, _ := b.Find()
	return !SocketsMount(e)
}
